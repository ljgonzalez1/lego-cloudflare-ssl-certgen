package debugrun

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"net/rpc/jsonrpc"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv("DEBUGRUN_TEST_HELPER"); mode != "" {
		if path := os.Getenv("DEBUGRUN_TEST_ENVIRONMENT"); path != "" {
			value, set := os.LookupEnv(configHomeEnvironment)
			record := fmt.Sprintf("%t\n%s\n%s\n", set, value, os.Getenv(savedConfigEnvironment))
			_ = os.WriteFile(path+".tmp", []byte(record), 0600)
			_ = os.Rename(path+".tmp", path)
		}
		fmt.Fprintln(os.Stderr, "fake debugger output")
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(m.Run())
}
func newSession(t *testing.T) *Session {
	t.Helper()
	session, err := OpenSession(t.TempDir(), "6.0.1-beta-1", "amd64", os.Geteuid(), os.Getegid(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return session
}
func TestOnlySupportedArchitecturesSelectDelve(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64", "386", "arm", "armv6", "armv7", "mips", "riscv64"} {
		want := arch == "amd64" || arch == "arm64" || arch == "386"
		if got := SupportsDelve("linux", arch); got != want {
			t.Errorf("%s support=%t", arch, got)
		}
		if SupportsDelve("windows", arch) {
			t.Fatal("enabled unsupported operating system")
		}
	}
}
func TestSuccessLogsHaveExactNamesAndPrivatePermissions(t *testing.T) {
	if got := formatTimestamp(time.Date(2026, time.November, 18, 13, 14, 15, 0, time.UTC)); got != "2026-18-11_13.14.15" {
		t.Fatal("timestamp must be year-day-month:", got)
	}
	session := newSession(t)
	if !regexp.MustCompile(`^v6\.0\.1-beta-1\.\d{4}-\d{2}-\d{2}_\d{2}\.\d{2}\.\d{2}\.amd64\.log$`).MatchString(filepath.Base(session.Path)) {
		t.Fatal(session.Path)
	}
	session.Record("test", "successful execution")
	session.Finalize(0)
	info, err := os.Stat(session.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe log permissions", err)
	}
	if _, err := os.Stat(session.DumpPath); !os.IsNotExist(err) {
		t.Fatal("success created a dump")
	}
}
func TestErrorsWriteDumpBeforeReturning(t *testing.T) {
	session := newSession(t)
	session.Record("test", "last target snapshot")
	session.MarkFailure("fatal failure")
	session.Finalize(2)
	text, err := os.ReadFile(session.DumpPath)
	if err != nil || !bytes.Contains(text, []byte("last target snapshot")) || !bytes.Contains(text, []byte("goroutine ")) {
		t.Fatalf("missing failure dump: %v", err)
	}
}
func TestConcurrentReservationsNeverOverwrite(t *testing.T) {
	directory := t.TempDir()
	paths := make(chan string, 16)
	var writers sync.WaitGroup
	for i := 0; i < 16; i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			session, err := OpenSession(directory, "6.0.1-beta-1", "armv6", os.Geteuid(), os.Getegid(), io.Discard)
			if err != nil {
				t.Error(err)
				return
			}
			paths <- session.Path
			session.Finalize(0)
		}()
	}
	writers.Wait()
	close(paths)
	seen := map[string]bool{}
	for path := range paths {
		if seen[path] {
			t.Fatal("overwrote a concurrent log")
		}
		seen[path] = true
	}
	if len(seen) != 16 {
		t.Fatal(len(seen))
	}
}
func TestRedactionSpansArbitraryWrites(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "private-test-token-012345")
	for split := 0; split <= len("private-test-token-012345"); split++ {
		session := newSession(t)
		stream := session.Stream()
		secret := "private-test-token-012345"
		_, _ = stream.Write([]byte("before " + secret[:split]))
		_, _ = stream.Write([]byte(secret[split:] + " after"))
		stream.Flush()
		session.Finalize(1)
		for _, path := range []string{session.Path, session.DumpPath} {
			data, err := os.ReadFile(path)
			if err != nil || bytes.Contains(data, []byte(secret)) || !bytes.Contains(data, []byte("[REDACTED]")) {
				t.Fatalf("split %d exposed credentials: %v", split, err)
			}
		}
	}
}
func TestLogDirectoryRejectsLinksAndFilenames(t *testing.T) {
	directory := t.TempDir()
	linked := filepath.Join(directory, "link")
	if err := os.Symlink(directory, linked); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSession(linked, "6.0.1-beta-1", "amd64", os.Geteuid(), os.Getegid(), io.Discard); err == nil {
		t.Fatal("followed a directory symlink")
	}
	if _, err := OpenSession(directory, "../../escape", "amd64", os.Geteuid(), os.Getegid(), io.Discard); err == nil {
		t.Fatal("accepted unsafe version")
	}
}

type FakeAPI struct {
	PID   int
	Mode  string
	Calls int
}
type VersionInput struct{ APIVersion int }
type Empty struct{}
type PIDOutput struct{ Pid int }
type CommandInput struct {
	Name string `json:"name"`
}
type GoroutineOutput struct {
	Goroutines []FakeGoroutine
	Nextg      int
}
type FakeGoroutine struct {
	ID int64 `json:"id"`
}
type StackOutput struct{ Locations []map[string]any }
type DebuggerReply struct{ State map[string]any }

func (f *FakeAPI) SetApiVersion(in VersionInput, out *Empty) error {
	if f.Mode == "startup-failure" {
		return fmt.Errorf("unsupported debugger")
	}
	return nil
}
func (f *FakeAPI) ProcessPid(in Empty, out *PIDOutput) error   { out.Pid = f.PID; return nil }
func (f *FakeAPI) Detach(in map[string]bool, out *Empty) error { return nil }
func (f *FakeAPI) ListGoroutines(in map[string]int, out *GoroutineOutput) error {
	out.Goroutines = []FakeGoroutine{{ID: 1}}
	return nil
}
func (f *FakeAPI) Stacktrace(in map[string]any, out *StackOutput) error {
	out.Locations = []map[string]any{{"file": "fixture.go", "line": 12, "function": map[string]string{"name": "fixture.work"}}}
	return nil
}
func (f *FakeAPI) State(in map[string]bool, out *DebuggerReply) error {
	out.State = map[string]any{"Pid": f.PID, "Running": true}
	return nil
}
func (f *FakeAPI) Command(in CommandInput, out *DebuggerReply) error {
	f.Calls++
	if f.Mode == "rpc-failure" {
		return fmt.Errorf("debugger lost its target after continue")
	}
	if f.Mode == "panic" && f.Calls == 1 {
		out.State = map[string]any{"Pid": f.PID, "currentThread": map[string]any{"breakPoint": map[string]any{"name": "unrecovered-panic"}}}
		return nil
	}
	status := 0
	if f.Mode == "nonzero" {
		status = 7
	}
	if f.Mode == "panic" {
		status = 2
	}
	out.State = map[string]any{"Pid": f.PID, "exited": true, "exitStatus": status}
	return nil
}

func TestDelveUsesPrivateConfigurationAndWorkerRestoresCaller(t *testing.T) {
	for _, original := range []*string{nil, new(string), func() *string { value := "/caller/config"; return &value }()} {
		name := "unset"
		if original != nil {
			name = "set:" + *original
		}
		t.Run(name, func(t *testing.T) {
			if original == nil {
				t.Setenv(configHomeEnvironment, "placeholder")
				if err := os.Unsetenv(configHomeEnvironment); err != nil {
					t.Fatal(err)
				}
			} else {
				t.Setenv(configHomeEnvironment, *original)
			}
			t.Setenv(savedConfigEnvironment, "1/stale/outer/value")
			work := t.TempDir()
			extra := delveConfigEnvironment(work)
			environment := workerEnvironment("/socket", "token", true, false, extra...)
			values := map[string][]string{}
			for _, entry := range environment {
				key, value, _ := strings.Cut(entry, "=")
				values[key] = append(values[key], value)
			}
			if got := values[configHomeEnvironment]; len(got) != 1 || got[0] != filepath.Join(work, "delve-config") {
				t.Fatalf("Delve configuration home=%q", got)
			}
			if got := values[savedConfigEnvironment]; len(got) != 1 {
				t.Fatalf("saved caller state=%q", got)
			}
			// Simulate the debugged worker receiving Delve's environment.
			t.Setenv(configHomeEnvironment, values[configHomeEnvironment][0])
			t.Setenv(savedConfigEnvironment, values[savedConfigEnvironment][0])
			restoreConfigEnvironment()
			value, set := os.LookupEnv(configHomeEnvironment)
			if original == nil && set {
				t.Fatalf("worker kept Delve's configuration home %q", value)
			}
			if original != nil && (!set || value != *original) {
				t.Fatalf("worker restored %q (set=%t), want %q", value, set, *original)
			}
			if _, leaked := os.LookupEnv(savedConfigEnvironment); leaked {
				t.Fatal("internal configuration marker reached descendants")
			}
		})
	}
}
func TestDelveProcessReceivesPrivateConfigurationHome(t *testing.T) {
	t.Setenv("DEBUGRUN_TEST_HELPER", "fake")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	t.Setenv(configHomeEnvironment, "/caller/config")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// Only the fake debugger records its environment; start the target first.
	target := exec.Command(executable)
	target.Env = os.Environ()
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Process.Kill(); _ = target.Wait() }()
	record := filepath.Join(t.TempDir(), "debugger-environment")
	t.Setenv("DEBUGRUN_TEST_ENVIRONMENT", record)
	output, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	session := newSession(t)
	backend := &FakeAPI{PID: target.Process.Pid, Mode: "success"}
	work := t.TempDir()
	s := &supervisor{options: Options{Executable: executable, Debugger: executable, Input: os.Stdin, Output: output, Error: output}, session: session, work: work, groups: make(map[int]bool)}
	s.dial = func(_ string) (net.Conn, error) {
		// Answer only after the fake debugger has recorded its environment.
		if _, err := os.Stat(record); err != nil {
			return nil, err
		}
		server := rpc.NewServer()
		if err := server.RegisterName("RPCServer", backend); err != nil {
			return nil, err
		}
		client, remote := net.Pipe()
		go server.ServeCodec(jsonrpc.NewServerCodec(remote))
		return client, nil
	}
	if status, _, err := s.runDelve(false); status != 0 || err != nil {
		t.Fatalf("status=%d err=%v", status, err)
	}
	session.Finalize(0)
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	want := "true\n" + filepath.Join(work, "delve-config") + "\n1/caller/config\n"
	if string(data) != want {
		t.Fatalf("debugger environment=%q, want %q", data, want)
	}
}
func TestDelveAPIExecutionAndNoReplayDecision(t *testing.T) {
	t.Setenv("DEBUGRUN_TEST_HELPER", "fake")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		mode            string
		status          int
		started, failed bool
	}{
		{"success", 0, true, false}, {"nonzero", 7, true, false}, {"panic", 2, true, false}, {"rpc-failure", 1, true, true}, {"startup-failure", 1, false, true}, {"unconfirmed-probe", 1, true, true},
	} {
		t.Run(item.mode, func(t *testing.T) {
			target := exec.Command(executable)
			target.Env = os.Environ()
			if err := target.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = target.Process.Kill(); _ = target.Wait() }()
			output, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			session := newSession(t)
			backend := &FakeAPI{PID: target.Process.Pid, Mode: item.mode}
			s := &supervisor{options: Options{Executable: executable, Debugger: executable, Input: os.Stdin, Output: output, Error: output}, session: session, work: t.TempDir(), groups: make(map[int]bool)}
			s.dial = func(_ string) (net.Conn, error) {
				server := rpc.NewServer()
				if err := server.RegisterName("RPCServer", backend); err != nil {
					return nil, err
				}
				client, remote := net.Pipe()
				go server.ServeCodec(jsonrpc.NewServerCodec(remote))
				return client, nil
			}
			status, started, err := s.runDelve(item.mode == "unconfirmed-probe")
			if status != item.status || started != item.started || (err != nil) != item.failed {
				t.Fatalf("status=%d started=%t err=%v", status, started, err)
			}
			// Once resumed, the supervisor is prohibited from retrying the user action.
			if item.mode == "rpc-failure" && backend.Calls != 1 {
				t.Fatal("continued a failed action more than once")
			}
			session.Finalize(status)
			data, _ := os.ReadFile(session.Path)
			if item.mode == "panic" && !strings.Contains(string(data), "fixture.work") {
				t.Fatal("missing pre-crash debugger stack")
			}
		})
	}
}
