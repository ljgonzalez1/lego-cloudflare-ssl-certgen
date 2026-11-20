package certgen

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestPulseViewStopsBeforeFinalSummary(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	path := filepath.Join(t.TempDir(), "terminal")
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	v := NewLegoView([]string{"example.com"}, 60, output)
	v.ansi = true // Exercise the renderer deterministically without a real PTY.
	v.Start()
	v.started = time.Now().Add(-260 * time.Millisecond)
	v.Tick()
	v.Finish("interrupted")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "⢾⣿⡷") || !strings.Contains(string(before), "No certificate") {
		t.Fatalf("pulse or final result missing: %q", before)
	}
	v.Tick()
	v.Close()
	after, err := os.ReadFile(path)
	if err != nil || string(before) != string(after) {
		t.Fatal("animation wrote after the final result")
	}
}

func TestPulseAppearsOnCheckingDomainAndStopsWhenVerified(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	t.Setenv("LINES", "30")
	path := filepath.Join(t.TempDir(), "terminal")
	output, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	v := NewLegoView([]string{"example.com", "example.org"}, 60, output)
	v.ansi = true
	v.Start()
	defer v.Close()
	v.Feed(parseLegoLine("2026/11/19 12:00:00 [INFO] [example.com] acme: Trying to solve DNS-01"))
	v.Feed(parseLegoLine("2026/11/19 12:00:00 [INFO] Wait for propagation [timeout: 1m0s, interval: 2s]"))
	v.started = time.Now().Add(-260 * time.Millisecond)
	v.Tick()
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plain := regexp.MustCompile("\x1b\\[[0-9;]*m").ReplaceAllString(string(bytes), "")
	if !strings.Contains(plain, "⢾⣿⡷ example.com  waiting for DNS propagation") {
		t.Fatalf("checking-domain row did not animate: %q", plain)
	}
	if strings.Contains(v.render(1, 100), "⢾⣿⡷") {
		t.Fatal("queued domain animated before its check started")
	}
	v.Feed(parseLegoLine("2026/11/19 12:00:01 [INFO] [example.com] The server validated our request"))
	if row := v.render(0, 100); strings.Contains(row, "⢾⣿⡷") || !strings.Contains(row, "✅") {
		t.Fatalf("verified row kept animating: %q", row)
	}
	v.set(0, 3, "certificate failed", 1)
	if row := v.render(0, 100); strings.Contains(row, "⢾⣿⡷") || !strings.Contains(row, "❌") {
		t.Fatalf("failed row kept animating: %q", row)
	}
}

func TestPulseViewEarlyCloseAndRedirectedOutput(t *testing.T) {
	for _, ansi := range []bool{false, true} {
		t.Run(map[bool]string{false: "redirected", true: "terminal"}[ansi], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "output")
			output, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			defer output.Close()
			v := NewLegoView([]string{"example.com"}, 60, output)
			v.ansi = ansi
			v.Start()
			v.Close()
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			v.Tick()
			v.Close()
			after, err := os.ReadFile(path)
			if err != nil || string(before) != string(after) {
				t.Fatal("closed view continued drawing")
			}
			if !ansi && len(before) != 0 {
				t.Fatalf("redirected view emitted animation: %q", before)
			}
			if ansi && !strings.HasSuffix(string(before), clearLine) {
				t.Fatal("unfinished status line was not cleared")
			}
		})
	}
}
