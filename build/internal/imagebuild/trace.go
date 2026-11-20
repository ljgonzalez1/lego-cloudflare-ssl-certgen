package imagebuild

import (
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Progress is installed by the single-command build CLI. Library callers and
// offline fixtures remain quiet unless they explicitly install a reporter.
var Progress func(string, ...any)

func Tracef(format string, args ...any) {
	if Progress != nil {
		Progress(format, args...)
	}
}

var diagnosticURL = regexp.MustCompile(`https?://[^\s<>"']+`)

// Diagnostics never print URL credentials, signed query strings or control codes.
func safeDiagnostic(message string) string {
	message = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, message)
	message = diagnosticURL.ReplaceAllStringFunc(message, func(value string) string {
		u, err := url.Parse(value)
		if err != nil {
			return "[redacted URL]"
		}
		if u.User != nil {
			u.User = url.User("REDACTED")
		}
		if u.RawQuery != "" {
			u.RawQuery = "REDACTED"
		}
		u.Fragment = ""
		return u.String()
	})
	return strings.Join(strings.Fields(message), " ")
}

// NewProgressLog prefixes build events with the target platform and elapsed
// time. A heartbeat distinguishes quiet work from a completed operation.
func NewProgressLog(output io.Writer, platform string) (func(string, ...any), func()) {
	return progressLog(output, platform, 15*time.Second)
}

func progressLog(output io.Writer, platform string, interval time.Duration) (func(string, ...any), func()) {
	started := time.Now()
	lastAt, last := started, "starting"
	platform = safeDiagnostic(platform)
	var mu sync.Mutex
	write := func(message string) {
		fmt.Fprintf(output, "[certgen-build %s +%s] %s\n", platform, time.Since(started).Round(time.Millisecond), message)
	}
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		last, lastAt = safeDiagnostic(fmt.Sprintf(format, args...)), time.Now()
		write(last)
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	ticker := time.NewTicker(interval)
	go func() {
		defer close(stopped)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				mu.Lock()
				write(fmt.Sprintf("still running; last event %s ago: %s", time.Since(lastAt).Round(time.Second), last))
				mu.Unlock()
			}
		}
	}()
	var once sync.Once
	return logf, func() {
		once.Do(func() {
			close(stop)
			<-stopped
		})
	}
}
