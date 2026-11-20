package certgen

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

func TestConsoleRedactionSurvivesEveryChunkBoundary(t *testing.T) {
	r := NewRedactor()
	r.AddSecret("private-test-token")
	r.AddSecret("private")
	text := "prefix:private-test-token:private:tail"
	for chunk := 1; chunk <= len(text); chunk++ {
		var output bytes.Buffer
		writer := r.writer(&output)
		for at := 0; at < len(text); at += chunk {
			if _, err := writer.Write([]byte(text[at:min(at+chunk, len(text))])); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(output.String(), "private-test-token") {
				t.Fatal("a split credential leaked")
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if output.String() != "prefix:[REDACTED]:[REDACTED]:tail" {
			t.Fatalf("chunk=%d output=%q", chunk, output.String())
		}
	}
}

func TestRedactorConcurrentAccessAndPanicRecovery(t *testing.T) {
	r := NewRedactor()
	r.AddSecret("private-test-token")
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				r.AddSecret("private-test-token")
				if got := r.Safe("error: private-test-token"); got != "error: [REDACTED]" {
					t.Errorf("unsafe error: %q", got)
				}
			}
		}()
	}
	workers.Wait()
	var output bytes.Buffer
	status := recoverStatus(r, &output, func() int { panic("private-test-token") })
	if status != 2 || output.String() != "Internal error: [REDACTED]\n" {
		t.Fatalf("panic status=%d output=%q", status, output.String())
	}
}
