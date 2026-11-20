package certgen

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/spinner"
)

type SignalState struct {
	value   atomic.Int32
	stop    func()
	Context context.Context
}

func WatchSignals() *SignalState {
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan os.Signal, 4)
	done := make(chan struct{})
	signal.Notify(received, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	state := &SignalState{Context: ctx}
	state.stop = func() { signal.Stop(received); cancel(); close(done) }
	go func() {
		for {
			select {
			case number := <-received:
				if number, ok := number.(syscall.Signal); ok {
					state.value.CompareAndSwap(0, int32(number))
					cancel()
				}
			case <-done:
				return
			}
		}
	}()
	return state
}
func (s *SignalState) Status() int {
	n := s.value.Load()
	if n == 0 {
		return 0
	}
	return 128 + int(n)
}
func TimeLimit(names int, propagation uint64) (uint64, error) {
	if names < 1 || names > MaxCertificateNames {
		return 0, errors.New("name count must be between 1 and 100")
	}
	if propagation > 2147483647 {
		return 0, errors.New("propagation seconds are out of range")
	}
	estimate := uint64(120) + uint64(names)*(propagation+60)
	return min((estimate*3+1)/2, uint64(7200)), nil
}

type ProcessOptions struct {
	Command                 []string
	Seconds                 uint64
	Log, Certificate, Group string
	Names                   []string
	Output, Error           *os.File
	Capture                 io.Writer
	Diagnostics             *Diagnostics
	Signals                 *SignalState
}
type outputChunk struct {
	data [4096]byte
	size int
	err  error
}

func killGroup(pid int, number syscall.Signal) {
	if pid > 0 {
		if err := syscall.Kill(-pid, number); err != nil {
			_ = syscall.Kill(pid, number)
		}
	}
}
func nonemptyFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}
func processExit(err error) int {
	if err == nil {
		return 0
	}
	var failure *exec.ExitError
	if errors.As(err, &failure) {
		if status, ok := failure.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return failure.ExitCode()
	}
	return 127
}
func Supervise(options ProcessOptions) (int, error) {
	if options.Seconds == 0 || options.Seconds > uint64((1<<63-1)/int64(time.Second)) || len(options.Command) == 0 {
		return 2, errors.New("expected a positive time limit and COMMAND")
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	if options.Error == nil {
		options.Error = os.Stderr
	}
	var logFile *os.File
	var err error
	if options.Log != "" {
		logFile, err = os.OpenFile(options.Log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			return 2, fmt.Errorf("cannot open the lego log: %w", err)
		}
		defer logFile.Close()
	}
	var view *LegoView
	if len(options.Names) > 0 {
		view = NewLegoView(options.Names, options.Seconds, options.Output)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return 1, err
	}
	defer reader.Close()
	defer writer.Close()
	command := exec.Command(options.Command[0], options.Command[1:]...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Stdout = writer
	command.Stderr = writer
	if err = command.Start(); err != nil {
		fmt.Fprintf(options.Error, "%s: %v\n", options.Command[0], err)
		return 127, err
	}
	writer.Close()
	if options.Diagnostics != nil {
		options.Diagnostics.Event(fmt.Sprintf("Started lego process group %d for %s", command.Process.Pid, options.Group))
		if recorder, ok := options.Diagnostics.Recorder.(interface {
			ProcessStarted(int)
			ProcessFinished(int)
		}); ok {
			recorder.ProcessStarted(command.Process.Pid)
			defer recorder.ProcessFinished(command.Process.Pid)
		}
	}
	completed := make(chan error, 1)
	go func() { completed <- command.Wait() }()
	chunks := make(chan outputChunk, 4)
	stopReading := make(chan struct{})
	defer close(stopReading)
	go func() {
		for {
			var chunk outputChunk
			chunk.size, chunk.err = reader.Read(chunk.data[:])
			select {
			case chunks <- chunk:
			case <-stopReading:
				return
			}
			if chunk.err != nil {
				return
			}
		}
	}()
	if view != nil {
		view.Start()
		defer view.Close()
	}
	deadline := time.NewTimer(time.Duration(options.Seconds) * time.Second)
	defer deadline.Stop()
	var ticks <-chan time.Time
	if view != nil && view.ansi {
		ticker := time.NewTicker(spinner.Interval)
		defer ticker.Stop()
		ticks = ticker.C
	}
	var contextDone <-chan struct{}
	if options.Signals != nil {
		contextDone = options.Signals.Context.Done()
	}
	var grace <-chan time.Time
	var graceTimer *time.Timer
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()
	startGrace := func(wait time.Duration) {
		if graceTimer == nil {
			graceTimer = time.NewTimer(wait)
			grace = graceTimer.C
		}
	}
	pending := make([]byte, 0, 16384)
	legoLog := LegoLog{}
	finished := false
	pipeClosed := false
	killed := false
	timedOut := false
	interruption := 0
	status := 0
	observe := func(text string) {
		line := parseLegoLine(text)
		wasFailed := legoLog.Cause != causeNone
		legoLog.Observe(line)
		if !wasFailed && legoLog.Cause != causeNone {
			startGrace(250 * time.Millisecond)
		}
		if view != nil {
			view.Feed(line)
		}
	}
	relay := func(chunk outputChunk) error {
		bytes := chunk.data[:chunk.size]
		if options.Diagnostics != nil && chunk.size > 0 {
			if recorder, ok := options.Diagnostics.Recorder.(interface{ RecordProcessOutput(int, string) }); ok {
				recorder.RecordProcessOutput(command.Process.Pid, string(bytes))
			}
		}
		if logFile != nil {
			if _, err := logFile.Write(bytes); err != nil {
				return fmt.Errorf("cannot write the lego log: %w", err)
			}
		}
		if options.Capture != nil {
			if _, err := options.Capture.Write(bytes); err != nil {
				return err
			}
		} else if view == nil {
			if _, err := options.Error.Write(bytes); err != nil {
				return err
			}
		}
		for _, value := range bytes {
			if value == '\n' {
				observe(string(pending))
				pending = pending[:0]
			} else if len(pending) < 16384 {
				pending = append(pending, value)
			}
		}
		// A broken child can flush a definitive error without its final newline.
		// Match complete fatal prefixes; ordinary partial progress stays active.
		if len(pending) > 0 && parseLegoLine(string(pending)).fatalHeader() {
			observe(string(pending))
		}
		if chunk.err != nil {
			pipeClosed = true
			if chunk.err != io.EOF && !errors.Is(chunk.err, os.ErrClosed) {
				return chunk.err
			}
		}
		return nil
	}
	var drainTimer *time.Timer
	var drain <-chan time.Time
	defer func() {
		if drainTimer != nil {
			drainTimer.Stop()
		}
	}()
	for !finished || !pipeClosed {
		select {
		case chunk := <-chunks:
			if err = relay(chunk); err != nil {
				killGroup(command.Process.Pid, syscall.SIGKILL)
				if !finished {
					<-completed
				}
				return 1, err
			}
		case waitErr := <-completed:
			finished = true
			status = processExit(waitErr)
			completed = nil
			// A descendant can inherit the log pipe. Bound draining after the leader exits.
			drainTimer = time.NewTimer(100 * time.Millisecond)
			drain = drainTimer.C
			if interruption != 0 {
				killGroup(command.Process.Pid, syscall.SIGKILL)
			}
		case <-contextDone:
			interruption = 143
			if options.Signals != nil && options.Signals.Status() != 0 {
				interruption = options.Signals.Status()
			}
			killGroup(command.Process.Pid, syscall.Signal(interruption-128))
			contextDone = nil
			if graceTimer != nil {
				graceTimer.Stop()
				graceTimer = nil
			}
			startGrace(2 * time.Second)
		case <-deadline.C:
			if !finished {
				timedOut = legoLog.Cause == causeNone
				killGroup(command.Process.Pid, syscall.SIGKILL)
				killed = true
			}
		case <-grace:
			grace = nil
			if !finished {
				killGroup(command.Process.Pid, syscall.SIGKILL)
				killed = true
			}
		case <-drain:
			drain = nil
			killGroup(command.Process.Pid, syscall.SIGKILL)
			reader.Close()
			pipeClosed = true
		case <-ticks:
			if view != nil {
				view.Tick()
			}
		}
	}
	if len(pending) > 0 {
		observe(string(pending))
	}
	failure := ""
	switch {
	case interruption != 0:
		status = interruption
		failure = fmt.Sprintf("lego was interrupted by signal %d.", interruption-128)
	case timedOut && legoLog.Cause == causeNone:
		status = 124
		failure = fmt.Sprintf("lego did not finish within %ds and was stopped with SIGKILL.", options.Seconds)
	case legoLog.Cause != causeNone || status != 0:
		if legoLog.Cause == causeRateLimit {
			status = 75
		} else if status == 0 {
			status = 1
		}
		failure = legoLog.Diagnostic(status)
		if killed {
			failure += " (lego stopped)"
		}
	case options.Certificate != "" && (!nonemptyFile(options.Certificate+".crt") || !nonemptyFile(options.Certificate+".key")):
		status = 1
		failure = "lego finished, but no certificate and key pair was saved."
	}
	if failure != "" && options.Group != "" {
		failure = options.Group + ": " + failure
	}
	if view != nil {
		view.Finish(failure)
	} else if failure != "" {
		fmt.Fprintln(options.Error, "❌ "+failure)
	}
	if failure != "" && options.Diagnostics != nil {
		options.Diagnostics.Failure(failure)
	}
	return status, nil
}

// A bounded version capture keeps a broken executable from exhausting memory.
type LimitedCapture struct {
	strings.Builder
	Limit int
}

func (capture *LimitedCapture) Write(value []byte) (int, error) {
	remaining := max(0, capture.Limit-capture.Len())
	if remaining > 0 {
		_, _ = capture.Builder.Write(value[:min(remaining, len(value))])
	}
	return len(value), nil
}
func (s *SignalState) Close() { s.stop() }
