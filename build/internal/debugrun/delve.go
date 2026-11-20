package debugrun

import (
	"encoding/json"
	"fmt"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type debuggerState struct {
	PID           int  `json:"Pid"`
	Exited        bool `json:"exited"`
	ExitStatus    int  `json:"exitStatus"`
	CurrentThread *struct {
		Breakpoint *struct {
			Name string `json:"name"`
		} `json:"breakPoint"`
	} `json:"currentThread"`
}
type stateReply struct{ State debuggerState }

func removeSocket(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *supervisor) runDelve(probe bool) (int, bool, error) {
	socket := filepath.Join(s.work, "delve.sock")
	// The prior probe has shut down and its socket belongs to this private dir.
	_ = removeSocket(socket)
	// The root supervisor controls a debugger running under the target UID.
	// Access is limited by the private 0700 directory, not matching peer UIDs.
	args := []string{"exec", s.options.Executable, "--headless=true", "--api-version=2", "--accept-multiclient=true", "--only-same-user=false", "--listen=unix:" + socket, "--log", "--log-output=debugger,rpc,debuglineerr", "--log-dest=2", "--"}
	if !probe {
		args = append(args, s.options.Arguments...)
	}
	process, err := s.start(exec.Command(s.options.Debugger, args...), probe, false)
	if err != nil {
		return 1, false, err
	}
	defer process.close()
	var connection net.Conn
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		if s.dial != nil {
			connection, err = s.dial(socket)
		} else {
			connection, err = net.DialTimeout("unix", socket, 100*time.Millisecond)
		}
		if err == nil {
			break
		}
		select {
		case <-process.done:
			return 1, false, fmt.Errorf("Delve exited before opening its local API: %v", process.err)
		case <-deadline.C:
			return 1, false, fmt.Errorf("Delve startup timed out")
		case <-time.After(25 * time.Millisecond):
		}
		if s.signal.Load() != 0 {
			return 128 + int(s.signal.Load()), false, fmt.Errorf("debugger startup interrupted")
		}
	}
	client := jsonrpc.NewClient(connection)
	defer client.Close()
	call := func(method string, args, result any, timeout time.Duration) error {
		_ = connection.SetDeadline(time.Now().Add(timeout))
		err := client.Call("RPCServer."+method, args, result)
		if err != nil {
			s.session.Record("Delve API", method+": "+err.Error())
		}
		return err
	}
	var version struct{}
	if err = call("SetApiVersion", map[string]int{"APIVersion": 2}, &version, 5*time.Second); err != nil {
		return 1, false, err
	}
	var target struct{ Pid int }
	if err = call("ProcessPid", struct{}{}, &target, 5*time.Second); err != nil {
		return 1, false, err
	}
	if target.Pid <= 0 {
		return 1, false, fmt.Errorf("Delve returned an invalid target PID")
	}
	s.workerPID.Store(int64(target.Pid))
	targetAlive := true
	defer s.workerPID.Store(0)
	defer func() {
		// Never leave a paused target or lego descendants behind when an RPC dies.
		var reply struct{}
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		_ = client.Call("RPCServer.Detach", map[string]bool{"Kill": true}, &reply)
		if targetAlive {
			_ = syscall.Kill(target.Pid, syscall.SIGKILL)
		}
		s.killGroups()
	}()
	if !probe {
		s.collectDelveSnapshot(call, "initial stopped target")
	}
	started := false
	probeTimer := time.Now().Add(20 * time.Second)
	for {
		// Once continue has been sent the application might have made an ACME order.
		// An RPC failure after this point must never trigger a second invocation.
		started = true
		_ = connection.SetDeadline(time.Time{})
		request := client.Go("RPCServer.Command", map[string]string{"name": "continue"}, &stateReply{}, make(chan *rpc.Call, 1))
		var completed *rpc.Call
		for completed == nil {
			select {
			case completed = <-request.Done:
			case <-process.done:
				if status, ok := s.reportedStatus(target.Pid); ok {
					s.session.MarkFailure("Delve terminated after the target reported completion; application status is preserved")
					return status, started, nil
				}
				return 1, started, fmt.Errorf("Delve terminated while the application was running: %v", process.err)
			case <-time.After(100 * time.Millisecond):
				if probe && time.Now().After(probeTimer) {
					return 1, started, fmt.Errorf("Delve could not finish the execution probe")
				}
			}
		}
		if completed.Error != nil {
			if status, ok := s.reportedStatus(target.Pid); ok {
				s.session.MarkFailure("Delve API failed after target completion; application status is preserved: " + completed.Error.Error())
				return status, started, nil
			}
			// Some Delve exits are reported as an RPC error. Query the actual state,
			// never infer a successful application exit from the debugger's own status.
			var reply stateReply
			if err = call("State", map[string]bool{"NonBlocking": true}, &reply, 5*time.Second); err == nil && reply.State.Exited {
				targetAlive = false
				return reply.State.ExitStatus, started, nil
			}
			return 1, started, completed.Error
		}
		state := completed.Reply.(*stateReply).State
		if state.Exited {
			if probe {
				if status, ok := s.reportedStatus(target.Pid); !ok || status != 0 {
					return 1, started, fmt.Errorf("Delve did not confirm execution of the Go probe")
				}
			}
			targetAlive = false
			s.session.Record("Delve", fmt.Sprintf("Target exited with status %d", state.ExitStatus))
			return state.ExitStatus, started, nil
		}
		if !probe {
			reason := "target stopped"
			if state.CurrentThread != nil && state.CurrentThread.Breakpoint != nil {
				reason = state.CurrentThread.Breakpoint.Name
			}
			s.collectDelveSnapshot(call, reason)
			lower := strings.ToLower(reason)
			if strings.Contains(lower, "panic") || strings.Contains(lower, "fatal") {
				s.session.MarkFailure("Delve captured the target before an unrecovered failure: " + reason)
			}
		}
	}
}

func (s *supervisor) collectDelveSnapshot(call func(string, any, any, time.Duration) error, reason string) {
	deadline := time.Now().Add(10 * time.Second)
	s.session.Record("Delve snapshot", reason)
	var goroutines struct {
		Goroutines []struct {
			ID int64 `json:"id"`
		}
		Nextg int
	}
	if err := call("ListGoroutines", map[string]int{"Start": 0, "Count": 64}, &goroutines, 5*time.Second); err != nil {
		return
	}
	if goroutines.Nextg > 0 {
		s.session.Record("Delve snapshot", "Goroutine list capped at 64 to keep failure collection bounded")
	}
	for _, goroutine := range goroutines.Goroutines {
		if time.Now().After(deadline) {
			s.session.Record("Delve snapshot", "Stack collection reached its ten-second time budget")
			break
		}
		var reply json.RawMessage
		if err := call("Stacktrace", map[string]any{"Id": goroutine.ID, "Depth": 64, "Full": false, "Defers": true}, &reply, 3*time.Second); err != nil {
			continue
		}
		s.session.Record("Delve stack", fmt.Sprintf("goroutine %d: %s", goroutine.ID, string(reply)))
	}
}
