package certgen

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func writeConfiguration(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "domains.yaml")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDomainsPreservesGroupsAndNormalizesNames(t *testing.T) {
	path := writeConfiguration(t, "first: &names [EXAMPLE.COM., '*.example.org']\nsecond: *names\n")
	groups, err := LoadDomains(path, []string{"second", "first", "second"}, false)
	if err != nil || len(groups) != 2 || groups[0].Name != "first" || groups[1].Name != "second" {
		t.Fatalf("groups=%+v error=%v", groups, err)
	}
	if got := strings.Join(groups[0].Names, ","); got != "example.com,*.example.org" {
		t.Fatalf("normalized names=%q", got)
	}
}

func TestLoadDomainsRejectsMalformedInput(t *testing.T) {
	for name, contents := range map[string]string{
		"empty": "", "scalar": "example.com", "empty mapping": "{}", "empty list": "x: []",
		"null": "x: null", "bad syntax": "x: [", "multiple documents": "x: [example.com]\n---\ny: [example.net]",
		"duplicate group": "x: [example.com]\nx: [example.org]", "duplicate name": "x: [example.com, EXAMPLE.COM.]",
		"traversal": "../escape: [example.com]", "unknown alias": "x: *absent", "recursive alias": "x: &x [*x]",
		"numeric group": "123: [example.com]", "boolean group": "yes: [example.com]", "explicit integer": "x: [!!int 12]",
		"no TLD": "x: [localhost]", "IP": "x: [127.0.0.1]", "bad wildcard": "x: ['a.*.example.com']",
		"unicode": "x: [éxample.com]", "control": "x: [\"example.com\\u001b\"]", "blank": "x: ['']",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadDomains(writeConfiguration(t, contents), nil, false); err == nil {
				t.Fatal("malformed configuration was accepted")
			}
		})
	}
}

func TestLoadDomainsEnforcesLimitsAndValidatesUnselectedGroups(t *testing.T) {
	var contents strings.Builder
	contents.WriteString("large:\n")
	for i := 0; i < 101; i++ {
		fmt.Fprintf(&contents, "  - name%d.example.com\n", i)
	}
	path := writeConfiguration(t, contents.String())
	if _, err := LoadDomains(path, nil, false); err == nil {
		t.Fatal("preview accepted more than 100 names")
	}
	if groups, err := LoadDomains(path, nil, true); err != nil || groups[0].Count != 101 {
		t.Fatalf("issuance must report an oversized group as a failure: %v", err)
	}
	path = writeConfiguration(t, "good: [example.com]\nbad: [127.0.0.1]\n")
	if _, err := LoadDomains(path, []string{"good"}, true); err == nil {
		t.Fatal("invalid unselected group was accepted")
	}
	path = writeConfiguration(t, strings.Repeat(" ", maxConfigurationBytes+1))
	if _, err := LoadDomains(path, nil, false); err == nil {
		t.Fatal("oversized input was accepted")
	}
}

func TestLoadDomainsRejectsNamedPipesWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "domains.yaml")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := LoadDomains(path, nil, false); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("accepted a named pipe")
		}
	case <-time.After(time.Second):
		t.Fatal("opening a configuration named pipe blocked")
	}
}

func TestParseOptionsRejectsConflictsAndMissingValues(t *testing.T) {
	for _, args := range [][]string{
		{"--production", "--staging"}, {"--test", "--production"}, {"--list", "--dry-run"},
		{"--only"}, {"--output", "--test"}, {"--config", ""}, {"--typo"}, {"--testing"}, {"--override"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := ParseOptions(args); err == nil {
				t.Fatal("invalid invocation was accepted")
			}
		})
	}
	for _, args := range [][]string{nil, {"--test"}, {"--staging"}, {"--production"}, {"--only", "a", "--only", "b"}} {
		if _, err := ParseOptions(args); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadIdentityPreservesUIDRangeOn32BitPlatforms(t *testing.T) {
	for _, key := range []string{"UID", "GID", "CERT_UID", "CERT_GID"} {
		t.Setenv(key, "")
	}
	for _, value := range []string{"1", "1000", "2147483648", "4294967294"} {
		t.Setenv("UID", value)
		identity, err := ReadIdentity()
		if err != nil || idText(identity.UID) != value {
			t.Fatalf("UID %s: %+v %v", value, identity, err)
		}
	}
	for _, value := range []string{"0", "-1", "1.0", "4294967295", "1000000000000", " 1000", "abc"} {
		t.Setenv("UID", value)
		if _, err := ReadIdentity(); err == nil {
			t.Fatalf("accepted UID %q", value)
		}
	}
}

func TestValidateResolvers(t *testing.T) {
	for _, text := range []string{"1.1.1.1:53", "dns.example.com:5353", "[2606:4700:4700::1111]:53", "[::1]:1"} {
		if !validResolver(text) {
			t.Errorf("valid resolver rejected: %q", text)
		}
	}
	for _, text := range []string{"", ":53", "1.1.1.1", "1.1.1.1:0", "1.1.1.1:65536", "999.1.1.1:53", "::1:53", "[wrong]:53", "dns: 53", "dns:53\n"} {
		if validResolver(text) {
			t.Errorf("invalid resolver accepted: %q", text)
		}
	}
}

func TestTimeLimitIsPerCertificateAndCapped(t *testing.T) {
	for _, item := range []struct {
		names      int
		wait, want uint64
	}{{1, 0, 270}, {1, 360, 810}, {5, 360, 3330}, {100, 2147483647, 7200}} {
		got, err := TimeLimit(item.names, item.wait)
		if err != nil || got != item.want {
			t.Fatalf("TimeLimit(%d,%d)=%d error=%v", item.names, item.wait, got, err)
		}
	}
	for _, names := range []int{0, -1, 101} {
		if _, err := TimeLimit(names, 360); err == nil {
			t.Fatal("invalid name count accepted")
		}
	}
}

func TestDiagnoseFatalLegoErrorsAndIgnoreProgress(t *testing.T) {
	for _, item := range []struct {
		detail string
		cause  int
	}{
		{"cloudflare: invalid access token (9109)", causeToken},
		{"cloudflare: authentication error (10000: forbidden)", causeAccess},
		{"cloudflare: could not find zone for domain example.com", causeZone},
		{"acme: too many identifiers", causeNameLimit},
		{"propagation: time limit exceeded", causePropagation},
		{"acme: error: 429" + rateLimitMarker + "weekly limit", causeRateLimit},
		{"acme: error: 400 :: urn:ietf:params:acme:error:unauthorized :: denied", causeRejected},
	} {
		log := LegoLog{}
		log.Observe(parseLegoLine("2026/11/18 10:20:30 Could not obtain certificates:"))
		log.Observe(parseLegoLine(item.detail))
		if log.Cause != item.cause || log.Diagnostic(1) == "" {
			t.Errorf("detail=%q cause=%d wanted=%d", item.detail, log.Cause, item.cause)
		}
	}
	log := LegoLog{}
	for _, line := range []string{
		"2026/11/18 10:20:30 [INFO] [example.com] acme: Waiting for DNS record propagation.",
		"2026/11/18 10:20:31 [WARN] [example.com] propagation: time limit exceeded; retrying",
	} {
		log.Observe(parseLegoLine(line))
	}
	if log.Cause != causeNone {
		t.Fatal("progress was mistaken for a fatal error")
	}
}

func TestMakeOutputPathIsExclusiveAndRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	paths := make(chan string, 20)
	var writers sync.WaitGroup
	for i := 0; i < cap(paths); i++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			path, err := MakeOutputPath(true, root, "group", time.UTC)
			if err != nil {
				t.Error(err)
				return
			}
			paths <- path
		}()
	}
	writers.Wait()
	close(paths)
	seen := map[string]bool{}
	for path := range paths {
		if seen[path] {
			t.Fatal("concurrent runs reused a folder")
		}
		seen[path] = true
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("incorrect output permissions: %v", err)
		}
	}
	if err := os.Symlink(root, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := MakeOutputPath(true, root, "linked", time.UTC); err == nil {
		t.Fatal("followed a certificate group symlink")
	}
	preview, err := MakeOutputPath(false, root, "preview", time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(preview); !os.IsNotExist(err) {
		t.Fatal("preview created files")
	}
}

func TestSupervisorChild(t *testing.T) {
	mode := os.Getenv("CERTGEN_GO_CHILD")
	if mode == "" {
		return
	}
	switch mode {
	case "success":
		fmt.Println("completed")
		os.Exit(0)
	case "nonzero":
		fmt.Fprintln(os.Stderr, "ordinary failure")
		os.Exit(7)
	case "fatal":
		fmt.Fprintln(os.Stderr, "Could not obtain certificates:\ncloudflare: invalid access token (9109)")
	case "unterminated":
		fmt.Fprint(os.Stderr, "Could not obtain certificates:")
	case "rate":
		fmt.Fprintln(os.Stderr, "retry: acme: error: 429"+rateLimitMarker+"too many certificates")
	case "progress":
		fmt.Fprintln(os.Stderr, "2026/11/18 10:20:30 [INFO] Working on a valid challenge")
		time.Sleep(350 * time.Millisecond)
		os.Exit(0)
	case "writer-error":
		fmt.Fprintln(os.Stdout, os.Getpid())
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestSuperviseStopsFailuresAndKeepsWorkingChildren(t *testing.T) {
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	for _, item := range []struct {
		mode   string
		status int
	}{
		{"success", 0}, {"nonzero", 7}, {"fatal", 137}, {"unterminated", 137}, {"rate", 75}, {"progress", 0}, {"timeout", 124},
	} {
		t.Run(item.mode, func(t *testing.T) {
			t.Setenv("CERTGEN_GO_CHILD", item.mode)
			var capture LimitedCapture
			capture.Limit = 1024
			started := time.Now()
			limit := uint64(10)
			if item.mode == "timeout" {
				limit = 1
			}
			status, err := Supervise(ProcessOptions{Command: []string{executable, "-test.run=^TestSupervisorChild$"}, Seconds: limit, Output: output, Error: output, Capture: &capture})
			if err != nil || status != item.status {
				t.Fatalf("exit=%d error=%v wanted=%d", status, err, item.status)
			}
			if item.mode == "fatal" && time.Since(started) > 5*time.Second {
				t.Fatal("fatal error waited for the timeout")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	t.Setenv("CERTGEN_GO_CHILD", "timeout")
	status, err := Supervise(ProcessOptions{Command: []string{executable, "-test.run=^TestSupervisorChild$"}, Seconds: 60, Output: output, Error: output, Signals: &SignalState{Context: ctx}})
	if err != nil || status != 143 {
		t.Fatalf("cancellation exit=%d error=%v", status, err)
	}
}

type brokenCapture struct {
	pid          int
	panicOnWrite bool
}

func (capture *brokenCapture) Write(data []byte) (int, error) {
	capture.pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	if capture.panicOnWrite {
		panic("fixture output panic")
	}
	return 0, errors.New("fixture output failure")
}

func TestSuperviseReapsChildOnOutputFailureOrPanic(t *testing.T) {
	t.Setenv("CERTGEN_GO_CHILD", "writer-error")
	t.Setenv("GORACE", "atexit_sleep_ms=0")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	for _, panicOnWrite := range []bool{false, true} {
		capture := &brokenCapture{panicOnWrite: panicOnWrite}
		started := time.Now()
		status := recoverStatus(NewRedactor(), output, func() int {
			status, err := Supervise(ProcessOptions{Command: []string{executable, "-test.run=^TestSupervisorChild$"},
				Seconds: 10, Output: output, Error: output, Capture: capture})
			if !panicOnWrite && (err == nil || status != 1) {
				t.Errorf("status=%d err=%v", status, err)
			}
			return status
		})
		if panicOnWrite && status != 2 {
			t.Fatalf("panic status=%d", status)
		}
		if capture.pid <= 0 || syscall.Kill(capture.pid, 0) != syscall.ESRCH || time.Since(started) > 5*time.Second {
			t.Fatalf("child was not promptly reaped: %d", capture.pid)
		}
	}
}

func TestDurationAndBoundedVersionCapture(t *testing.T) {
	for seconds, want := range map[float64]string{-1: "0s", 0: "0s", 1.6: "2s", 60: "1m0s", 103: "1m43s", 7200: "120m0s"} {
		if got := duration(seconds); got != want {
			t.Errorf("duration=%s wanted=%s", got, want)
		}
	}
	for seconds, want := range map[float64]string{0: "0:00", 59: "0:59", 60: "1:00", 3600: "1:00:00", 7200: "2:00:00"} {
		if got := clock(seconds); got != want {
			t.Errorf("clock=%s wanted=%s", got, want)
		}
	}
	if got := fit("\x1b[31mabcdef\x1b[0m", 3); got != "\x1b[31mabc\x1b[0m" {
		t.Fatalf("truncation leaked terminal styling: %q", got)
	}
	capture := LimitedCapture{Limit: 5}
	if n, err := capture.Write([]byte("0123456789")); n != 10 || err != nil || capture.String() != "01234" {
		t.Fatal("capture was not bounded")
	}
}

func FuzzParseOptions(f *testing.F) {
	for _, seed := range []string{"--test", "--production", "--only|first", "--config|file|--list", "--typo"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 8192 {
			return
		}
		_, _ = ParseOptions(strings.Split(text, "|"))
	})
}

func FuzzLegoLog(f *testing.F) {
	for _, seed := range []string{"", "Could not obtain certificates:", "[INFO] propagation", "retry: acme: error: 429" + rateLimitMarker + "limit"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 16384 {
			return
		}
		log := LegoLog{}
		for _, line := range strings.Split(text, "\n") {
			log.Observe(parseLegoLine(line))
		}
		_ = log.Diagnostic(1)
	})
}

func FuzzLoadDomains(f *testing.F) {
	for _, seed := range []string{"x: [example.com]", "x: &x [*x]", "x: [*.example.com]", "", "x: !!seq [!!str example.com]"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, contents string) {
		if len(contents) > 16384 {
			return
		}
		_, _ = LoadDomains(writeConfiguration(t, contents), nil, false)
	})
}
