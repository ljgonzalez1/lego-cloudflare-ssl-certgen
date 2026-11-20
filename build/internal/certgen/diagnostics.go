package certgen

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Diagnostics keeps bounded human-readable events in memory. Failures emit the
// event history and goroutine stacks to stderr without creating trace files.
type Diagnostics struct {
	mu       sync.Mutex
	events   [64]string
	next     int
	count    int
	secrets  []string
	output   io.Writer
	started  time.Time
	mode     string
	Recorder interface{ Record(string, string) }
}

func NewDiagnostics(output io.Writer) *Diagnostics {
	mode := env("CERTGEN_TRACE", "errors")
	if mode != "errors" && mode != "always" && mode != "off" {
		mode = "errors"
	}
	d := &Diagnostics{output: output, started: time.Now(), mode: mode}
	for _, key := range []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_API_KEY", "CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN"} {
		d.AddSecret(os.Getenv(key))
	}
	debug.SetTraceback("system")
	if os.Getenv("GOGC") == "" {
		debug.SetGCPercent(-1)
	}
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(256 << 20)
	}
	return d
}
func (d *Diagnostics) AddSecret(secret string) {
	if secret != "" {
		d.mu.Lock()
		d.secrets = append(d.secrets, secret)
		d.mu.Unlock()
	}
}
func (d *Diagnostics) redactLocked(text string) string {
	for _, secret := range d.secrets {
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	return text
}
func (d *Diagnostics) Safe(text string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.redactLocked(text)
}
func (d *Diagnostics) Event(message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	message = d.redactLocked(message)
	line := fmt.Sprintf("+%s %s", time.Since(d.started).Round(time.Millisecond), message)
	d.events[d.next] = line
	d.next = (d.next + 1) % len(d.events)
	if d.count < len(d.events) {
		d.count++
	}
	if d.mode == "always" {
		fmt.Fprintf(d.output, "[debug trace] %s\n", line)
	}
	if d.Recorder != nil {
		d.Recorder.Record("event", line)
	}
}
func (d *Diagnostics) Failure(message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	stack := make([]byte, 512<<10)
	n := runtime.Stack(stack, true)
	if d.Recorder != nil {
		d.Recorder.Record("failure", d.redactLocked(message)+"\n"+d.redactLocked(string(stack[:n])))
	}
	if d.mode == "off" {
		return
	}
	fmt.Fprintf(d.output, "\n[debug trace] %s\n", d.redactLocked(message))
	for i := 0; i < d.count; i++ {
		fmt.Fprintln(d.output, d.events[(d.next-d.count+i+len(d.events))%len(d.events)])
	}
	// A fixed upper bound prevents diagnostics from consuming an unbounded heap.
	fmt.Fprintln(d.output, d.redactLocked(string(stack[:n])))
	if n == len(stack) {
		fmt.Fprintln(d.output, "[debug trace] Goroutine stack output reached the 512 KiB limit.")
	}
}
