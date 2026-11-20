package licenseaudit

import (
	"bytes"
	"crypto/sha512"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type progressWriter chan string

func (w progressWriter) Write(data []byte) (int, error) {
	select {
	case w <- string(data):
	default:
	}
	return len(data), nil
}

func TestProgressEmitsHeartbeatAndClosesIdempotently(t *testing.T) {
	messages := make(progressWriter, 32)
	logf, stop := progressLog(messages, "linux/arm/v6", 10*time.Millisecond)
	defer stop()
	logf("HTTP GET https://mirror.example.test/source.tar.gz")
	deadline := time.After(2 * time.Second)
	for {
		select {
		case message := <-messages:
			if !strings.Contains(message, "[license-audit linux/arm/v6 +") {
				t.Fatal("target platform or elapsed time missing", message)
			}
			if strings.Contains(message, "still running; last event") && strings.Contains(message, "source.tar.gz") {
				stop()
				stop()
				return
			}
		case <-deadline:
			t.Fatal("quiet network work produced no heartbeat")
		}
	}
}

func TestProgressRedactsCredentialsAndQueryTokens(t *testing.T) {
	var output bytes.Buffer
	logf, stop := progressLog(&output, "linux/amd64", time.Hour)
	logf("download failed\nhttps://private:password@proxy.example.test/archive?token=secret#fragment\x1b")
	stop()
	message := output.String()
	for _, secret := range []string{"private", "password", "token=secret", "fragment", "\x1b"} {
		if strings.Contains(message, secret) {
			t.Fatal("sensitive or control text leaked", secret)
		}
	}
	if strings.Count(message, "\n") != 1 || !strings.Contains(message, "REDACTED") {
		t.Fatal("diagnostic must remain one readable redacted line", message)
	}
	commit := strings.Repeat("a", 40)
	if !strings.Contains(safeDiagnostic("https://git.alpinelinux.org/aports/plain/APKBUILD?id="+commit), commit) {
		t.Fatal("pinned commit was unnecessarily hidden")
	}
}

func TestModuleFailureIncludesRedactedGoDiagnostic(t *testing.T) {
	t.Setenv("GOSUMDB", "sum.golang.org")
	work := t.TempDir()
	goPath := filepath.Join(work, "go-fixture")
	put(t, goPath, "#!/bin/sh\nprintf '%s\\n' '{\"Error\":\"HTTP 403 from https://private:password@proxy.example.test/module?token=secret\"}'\nexit 1\n")
	if err := os.Chmod(goPath, 0755); err != nil {
		t.Fatal(err)
	}
	sources := NativeSources{Go: goPath, Work: work}
	_, err := sources.Module("example.test/module", "v1.0.0", "")
	if err == nil || !strings.Contains(err.Error(), "HTTP 403") || !strings.Contains(err.Error(), "example.test/module@v1.0.0") {
		t.Fatal("Go command failure lost its useful diagnostic", err)
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "token=secret") {
		t.Fatal("Go command diagnostic leaked URL credentials", err)
	}
}

func TestModuleDownloadBoundsInheritedOutputPipes(t *testing.T) {
	t.Setenv("GOSUMDB", "sum.golang.org")
	work := t.TempDir()
	goPath, pidPath := filepath.Join(work, "go-fixture"), filepath.Join(work, "child.pid")
	t.Setenv("CERTGEN_TEST_CHILD_PID", pidPath)
	put(t, goPath, "#!/bin/sh\nsleep 30 &\nprintf '%s\\n' \"$!\" > \"$CERTGEN_TEST_CHILD_PID\"\nprintf '{}\\n'\nexit 0\n")
	if err := os.Chmod(goPath, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if data, err := os.ReadFile(pidPath); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				if process, err := os.FindProcess(pid); err == nil {
					_ = process.Kill()
				}
			}
		}
	})
	started := time.Now()
	sources := NativeSources{Go: goPath, Work: work}
	_, err := sources.Module("example.test/module", "v1.0.0", "")
	if err == nil || !strings.Contains(err.Error(), "WaitDelay") || time.Since(started) > 10*time.Second {
		t.Fatal("orphaned stdout pipe was not bounded", err)
	}
}

func TestPreferredSourceMirrorStillRejectsHTTP200HTML(t *testing.T) {
	work := t.TempDir()
	put(t, filepath.Join(work, "etc/alpine-release"), "3.24.4\n")
	payload := []byte("checksum-bound source archive fixture")
	sum := sha512.Sum512(payload)
	entry := [3]string{"source.tar.gz", "https://source.example.test/source.tar.gz", fmt.Sprintf("%x", sum)}
	var hosts []string
	tampered := false
	client := &http.Client{Transport: sourceTransport(func(request *http.Request) (*http.Response, error) {
		hosts = append(hosts, request.URL.Host)
		data := payload
		if request.URL.Host == "distfiles.alpinelinux.org" || tampered {
			data = []byte("<html>challenge</html>")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
	})}
	sources := NativeSources{DataRoot: work, Client: client}
	data, origin, err := sources.sourceArchive(entry)
	if err != nil || !bytes.Equal(data, payload) || origin != entry[1] || len(hosts) != 2 || hosts[0] != "distfiles.alpinelinux.org" {
		t.Fatal("preferred mirror did not enforce the recipe checksum and fallback", origin, hosts, err)
	}
	tampered = true
	if _, _, err = sources.sourceArchive(entry); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatal("invalid HTTP 200 archives were accepted", err)
	}
}
