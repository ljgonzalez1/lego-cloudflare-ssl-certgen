package debugrun

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

type Options struct {
	Executable           string
	Arguments            []string
	Version              string
	LogDirectory         string
	Debugger             string
	Input, Output, Error *os.File
	UID, GID             int
	IdentityValid        bool
	PrepareOutput        bool
	ProbeOnly            bool
}

type supervisor struct {
	options             Options
	session             *Session
	work, token, socket string
	listener            *net.UnixListener
	connections         sync.WaitGroup
	workerPID           atomic.Int64
	signal              atomic.Int32
	mu                  sync.Mutex
	groups              map[int]bool
	finished            bool
	reported            map[int]int
	streams             map[int]*streamWriter
	dial                func(string) (net.Conn, error)
}

func Run(options Options) int {
	if options.Input == nil {
		options.Input = os.Stdin
	}
	if options.Output == nil {
		options.Output = os.Stdout
	}
	if options.Error == nil {
		options.Error = os.Stderr
	}
	if options.LogDirectory == "" {
		options.LogDirectory = os.Getenv("CERTGEN_LOG_DIR")
	}
	if options.LogDirectory == "" {
		options.LogDirectory = "/ssl-certs/debug"
	}
	if options.Debugger == "" {
		options.Debugger = "/usr/local/bin/dlv"
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 && options.IdentityValid {
		uid = options.UID
		gid = options.GID
	}
	session, err := OpenSession(options.LogDirectory, options.Version, Architecture(), uid, gid, options.Error)
	if err != nil {
		fallback, mkdirErr := os.MkdirTemp("", "certgen-logs-")
		if mkdirErr == nil {
			session, mkdirErr = OpenSession(fallback, options.Version, Architecture(), uid, gid, options.Error)
		}
		if mkdirErr != nil {
			fmt.Fprintf(options.Error, "Warning: persistent diagnostics are unavailable (%v; fallback: %v).\n", err, mkdirErr)
			session = &Session{errors: options.Error, secrets: ReadSecrets()}
			for _, secret := range session.secrets {
				session.longest = max(session.longest, len(secret))
			}
		} else {
			fmt.Fprintf(options.Error, "Warning: diagnostic directory is unavailable (%v). Logs: %s\n", err, session.Path)
		}
	}
	s := &supervisor{options: options, session: session, groups: make(map[int]bool), reported: make(map[int]int)}
	status := 1
	defer func() { session.Finalize(status) }()
	saved := captureTerminal(options.Input)
	defer saved.restore()
	s.work, err = os.MkdirTemp("", "certgen-debug-")
	if err != nil {
		s.work, err = os.MkdirTemp(filepath.Dir(session.Path), ".debug-session-")
		if err != nil {
			session.Record("debugger", "Temporary debugger workspace unavailable; using portable diagnostics")
			s.work = ""
		}
	}
	if s.work != "" {
		defer os.RemoveAll(s.work)
	}
	if s.work != "" && os.Geteuid() == 0 && options.IdentityValid {
		_ = os.Chown(s.work, uid, gid)
	}
	random := make([]byte, 24)
	if _, err = rand.Read(random); err != nil {
		session.MarkFailure(err.Error())
		return status
	}
	s.token = hex.EncodeToString(random)
	session.secrets = append(session.secrets, s.token)
	session.longest = max(session.longest, len(s.token))
	if s.work != "" {
		s.socket = filepath.Join(s.work, "worker.sock")
		s.listener, err = net.ListenUnix("unix", &net.UnixAddr{Name: s.socket, Net: "unix"})
	}
	if err != nil {
		session.Record("debugger", "Local sockets are unavailable; native diagnostics use inherited pipes: "+err.Error())
	}
	_ = os.Chmod(s.socket, 0600)
	if os.Geteuid() == 0 && options.IdentityValid {
		_ = os.Chown(s.socket, uid, gid)
	}
	if s.listener != nil {
		acceptDone := make(chan struct{})
		go func() { defer close(acceptDone); s.acceptWorkers() }()
		defer func() { _ = s.listener.Close(); <-acceptDone; s.connections.Wait() }()
	} else {
		defer s.connections.Wait()
	}
	received := make(chan os.Signal, 8)
	signal.Notify(received, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	stopSignals := make(chan struct{})
	go func() {
		for {
			select {
			case value := <-received:
				number, ok := value.(syscall.Signal)
				if !ok {
					continue
				}
				s.signal.CompareAndSwap(0, int32(number))
				pid := int(s.workerPID.Load())
				if pid > 0 {
					_ = syscall.Kill(pid, number)
				}
				session.Record("signal", fmt.Sprintf("Forwarded signal %d to target PID %d", number, pid))
				go func() {
					timer := time.NewTimer(4 * time.Second)
					defer timer.Stop()
					select {
					case <-stopSignals:
						return
					case <-timer.C:
					}
					if pid > 0 && int(s.workerPID.Load()) == pid {
						_ = syscall.Kill(pid, syscall.SIGKILL)
						s.killGroups()
					}
				}()
			case <-stopSignals:
				return
			}
		}
	}()
	defer func() { signal.Stop(received); close(stopSignals) }()
	canDebug := SupportsDelve(runtime.GOOS, runtime.GOARCH) && s.listener != nil
	if _, err = os.Stat(options.Debugger); err != nil {
		canDebug = false
		session.Record("debugger", "Delve is not installed; using Go diagnostics")
	}
	if !SupportsDelve(runtime.GOOS, runtime.GOARCH) {
		session.Record("debugger", "This architecture does not support Delve; using Go diagnostics")
	}
	if canDebug {
		probeStatus, started, probeErr := s.runDelve(true)
		if probeErr != nil || probeStatus != 0 {
			canDebug = false
			session.Record("debugger", fmt.Sprintf("Execution probe failed (exit %d; started=%t): %v. Using Go diagnostics", probeStatus, started, probeErr))
		} else {
			session.Record("debugger", "Delve executed the Go probe successfully")
		}
	}
	if options.ProbeOnly {
		if canDebug {
			status = 0
		} else {
			status = 3
		}
		return status
	}
	if s.signal.Load() != 0 {
		status = 128 + int(s.signal.Load())
		return status
	}
	if canDebug {
		code, started, debugErr := s.runDelve(false)
		if debugErr == nil {
			status = code
			return status
		}
		session.Record("debugger", fmt.Sprintf("Delve failed: %v", debugErr))
		if started {
			session.MarkFailure("The target had already been resumed. It will not be restarted; this prevents duplicate ACME orders.")
			fmt.Fprintln(options.Error, "❌ Debugger failed after the application started. The run will not be repeated. Dump:", session.DumpPath)
			status = 1
			if s.signal.Load() != 0 {
				status = 128 + int(s.signal.Load())
			}
			return status
		}
		session.Record("debugger", "Target was never resumed; running it once with Go diagnostics")
	}
	status = s.runNative()
	return status
}

func exitStatus(err error) int {
	if err == nil {
		return 0
	}
	var failure *exec.ExitError
	if errors.As(err, &failure) {
		if value, ok := failure.Sys().(syscall.WaitStatus); ok && value.Signaled() {
			return 128 + int(value.Signal())
		}
		return failure.ExitCode()
	}
	return 1
}
func (s *supervisor) acceptWorkers() {
	for {
		connection, err := s.listener.AcceptUnix()
		if err != nil {
			return
		}
		s.connections.Add(1)
		go func() { defer s.connections.Done(); defer connection.Close(); s.serveWorker(connection) }()
	}
}
func (s *supervisor) serveWorker(connection connection) {
	decoder, encoder := json.NewDecoder(connection), json.NewEncoder(connection)
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello packet
	if decoder.Decode(&hello) != nil || hello.Token != s.token || hello.Operation != "hello" {
		return
	}
	if hello.PID <= 0 {
		return
	}
	if !hello.Probe {
		s.workerPID.Store(int64(hello.PID))
	}
	_ = connection.SetReadDeadline(time.Time{})
	rootOwner := os.Geteuid() == 0 && s.options.IdentityValid
	if encoder.Encode(response{RootOwner: rootOwner}) != nil {
		return
	}
	for {
		var message packet
		if decoder.Decode(&message) != nil {
			return
		}
		answer := response{}
		switch message.Operation {
		case "prepare-output":
			if !rootOwner || !s.options.PrepareOutput {
				answer.Error = "output ownership was not authorized for this invocation"
			} else if err := os.Chown("/ssl-certs", s.options.UID, s.options.GID); err != nil {
				answer.Error = "Cannot set output ownership: " + err.Error()
			}
		case "process-start":
			if message.PID > 0 {
				s.mu.Lock()
				s.groups[message.PID] = true
				if s.streams == nil {
					s.streams = make(map[int]*streamWriter)
				}
				s.streams[message.PID] = s.session.Stream(fmt.Sprintf("lego %d", message.PID))
				s.mu.Unlock()
				s.session.Record("lego output", fmt.Sprintf("Started process %d", message.PID))
			}
		case "process-output":
			s.mu.Lock()
			if stream := s.streams[message.PID]; stream != nil {
				_, _ = stream.Write([]byte(message.Text))
			}
			s.mu.Unlock()
		case "process-end":
			s.mu.Lock()
			if stream := s.streams[message.PID]; stream != nil {
				stream.Flush()
				delete(s.streams, message.PID)
			}
			delete(s.groups, message.PID)
			s.mu.Unlock()
		case "failure":
			s.session.MarkFailure(message.Text)
		case "finish":
			s.mu.Lock()
			if s.reported == nil {
				s.reported = make(map[int]int)
			}
			s.reported[hello.PID] = message.Status
			s.mu.Unlock()
			if !hello.Probe {
				s.session.Record("target final snapshot", message.Text)
				s.mu.Lock()
				s.finished = true
				s.mu.Unlock()
				if message.Status != 0 {
					s.session.MarkFailure("Target reported exit status " + strconv.Itoa(message.Status))
				}
			}
		default:
			if !hello.Probe {
				s.session.Record(message.Operation, message.Text)
			}
		}
		if encoder.Encode(answer) != nil {
			return
		}
	}
}
func (s *supervisor) killGroups() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for pid := range s.groups {
		if stream := s.streams[pid]; stream != nil {
			stream.Flush()
			delete(s.streams, pid)
		}
		if pid > 0 {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	}
	s.groups = make(map[int]bool)
}

func (s *supervisor) reportedStatus(pid int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	status, ok := s.reported[pid]
	return status, ok
}

type runningCommand struct {
	command        *exec.Cmd
	stdout, stderr *outputRelay
	done           chan struct{}
	err            error
}

func (s *supervisor) start(command *exec.Cmd, probe, pipe bool) (*runningCommand, error) {
	out, err := newRelay(s.options.Output, s.session, "stdout")
	if err != nil {
		return nil, err
	}
	errorOut, err := newRelay(s.options.Error, s.session, "stderr")
	if err != nil {
		out.close()
		return nil, err
	}
	process := &runningCommand{command: command, stdout: out, stderr: errorOut, done: make(chan struct{})}
	command.Stdin = s.options.Input
	command.Stdout = out.child
	command.Stderr = errorOut.child
	command.Env = workerEnvironment(s.socket, s.token, probe, pipe)
	var childDescriptors []*os.File
	if pipe {
		requestRead, requestWrite, e := os.Pipe()
		if e != nil {
			out.close()
			errorOut.close()
			return nil, e
		}
		replyRead, replyWrite, e := os.Pipe()
		if e != nil {
			requestRead.Close()
			requestWrite.Close()
			out.close()
			errorOut.close()
			return nil, e
		}
		childDescriptors = []*os.File{requestWrite, replyRead}
		command.ExtraFiles = childDescriptors
		parent := &pipeConnection{read: requestRead, write: replyWrite}
		s.connections.Add(1)
		go func() { defer s.connections.Done(); defer parent.Close(); s.serveWorker(parent) }()
		defer func() {
			for _, file := range childDescriptors {
				_ = file.Close()
			}
		}()
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var foreground int32
	if terminal(s.options.Input) && ioctl(s.options.Input.Fd(), syscall.TIOCGPGRP, unsafe.Pointer(&foreground)) == nil && foreground > 0 {
		command.SysProcAttr.Foreground = true
		command.SysProcAttr.Ctty = int(s.options.Input.Fd())
	}
	if os.Geteuid() == 0 && s.options.IdentityValid {
		command.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(s.options.UID), Gid: uint32(s.options.GID), Groups: []uint32{}}
	}
	if err = command.Start(); err != nil {
		out.close()
		errorOut.close()
		return nil, err
	}
	out.start()
	errorOut.start()
	out.childStarted()
	errorOut.childStarted()
	go func() { process.err = command.Wait(); close(process.done) }()
	return process, nil
}
func (p *runningCommand) close() {
	select {
	case <-p.done:
	default:
		_ = p.command.Process.Kill()
		<-p.done
	}
	for _, relay := range []*outputRelay{p.stdout, p.stderr} {
		select {
		case <-relay.done:
		case <-time.After(time.Second):
			relay.close()
			<-relay.done
		}
		relay.close()
	}
}
func (s *supervisor) runNative() int {
	command := exec.Command(s.options.Executable, s.options.Arguments...)
	process, err := s.start(command, false, true)
	if err != nil {
		s.session.MarkFailure(err.Error())
		fmt.Fprintln(s.options.Error, "Error: could not start the application:", err)
		return 1
	}
	s.workerPID.Store(int64(command.Process.Pid))
	<-process.done
	s.workerPID.Store(0)
	s.killGroups()
	process.close()
	status := exitStatus(process.err)
	s.mu.Lock()
	finished := s.finished
	s.mu.Unlock()
	if status != 0 && !finished {
		s.session.MarkFailure("Target terminated before its final snapshot; captured output and the last available snapshot are retained")
	}
	if s.signal.Load() != 0 {
		return 128 + int(s.signal.Load())
	}
	return status
}
