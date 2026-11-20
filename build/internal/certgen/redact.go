package certgen

import (
	"io"
	"os"
	"sort"
	"strings"
	"sync"
)

// Redactor protects credentials in ordinary error messages and console output.
// It records no events, stacks or log files.
type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

func NewRedactor() *Redactor {
	r := &Redactor{}
	for _, key := range []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_API_KEY", "CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN"} {
		r.AddSecret(os.Getenv(key))
	}
	return r
}

func (r *Redactor) AddSecret(secret string) {
	if r == nil || secret == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.secrets {
		if existing == secret {
			return
		}
	}
	r.secrets = append(r.secrets, secret)
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

func (r *Redactor) Safe(text string) string {
	if r == nil {
		return text
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, secret := range r.secrets {
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	return text
}

type redactedWriter struct {
	output  io.Writer
	secrets []string
	longest int
	pending string
}

func (r *Redactor) writer(output io.Writer) *redactedWriter {
	r.mu.RLock()
	defer r.mu.RUnlock()
	w := &redactedWriter{output: output, secrets: append([]string(nil), r.secrets...), longest: 1}
	for _, secret := range w.secrets {
		w.longest = max(w.longest, len(secret))
	}
	return w
}

func (w *redactedWriter) Write(data []byte) (int, error) {
	w.pending += string(data)
	if err := w.flush(false); err != nil {
		return 0, err
	}
	return len(data), nil
}

func (w *redactedWriter) Close() error { return w.flush(true) }

func (w *redactedWriter) flush(final bool) error {
	var safe strings.Builder
	position := 0
	for position < len(w.pending) && (final || len(w.pending)-position >= w.longest) {
		matched := false
		for _, secret := range w.secrets {
			if strings.HasPrefix(w.pending[position:], secret) {
				safe.WriteString("[REDACTED]")
				position += len(secret)
				matched = true
				break
			}
		}
		if !matched {
			safe.WriteByte(w.pending[position])
			position++
		}
	}
	w.pending = w.pending[position:]
	if safe.Len() == 0 {
		return nil
	}
	_, err := io.WriteString(w.output, safe.String())
	return err
}
