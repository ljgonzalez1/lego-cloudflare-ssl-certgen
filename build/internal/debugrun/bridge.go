package debugrun

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

const socketEnvironment = "CERTGEN_INTERNAL_SOCKET"
const tokenEnvironment = "CERTGEN_INTERNAL_TOKEN"
const probeEnvironment = "CERTGEN_INTERNAL_PROBE"
const pipeEnvironment = "CERTGEN_INTERNAL_PIPE"

type connection interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
	SetReadDeadline(time.Time) error
}
type pipeConnection struct{ read, write *os.File }

func (p *pipeConnection) Read(b []byte) (int, error)  { return p.read.Read(b) }
func (p *pipeConnection) Write(b []byte) (int, error) { return p.write.Write(b) }
func (p *pipeConnection) Close() error                { _ = p.write.Close(); return p.read.Close() }
func (p *pipeConnection) SetDeadline(t time.Time) error {
	_ = p.write.SetWriteDeadline(t)
	return p.read.SetReadDeadline(t)
}
func (p *pipeConnection) SetReadDeadline(t time.Time) error { return p.read.SetReadDeadline(t) }

type packet struct {
	Token     string
	Operation string
	Text      string
	PID       int
	Status    int
	Probe     bool
}
type response struct {
	RootOwner bool
	Error     string
}

type Bridge struct {
	mu         sync.Mutex
	connection connection
	encoder    *json.Encoder
	decoder    *json.Decoder
	RootOwner  bool
	Probe      bool
	broken     bool
}

func IsWorker() bool {
	return (os.Getenv(socketEnvironment) != "" || os.Getenv(pipeEnvironment) == "1") && os.Getenv(tokenEnvironment) != ""
}
func ConnectWorker() (*Bridge, error) {
	var stream connection
	var err error
	if os.Getenv(pipeEnvironment) == "1" {
		syscall.CloseOnExec(3)
		syscall.CloseOnExec(4)
		_ = syscall.SetNonblock(3, true)
		_ = syscall.SetNonblock(4, true)
		stream = &pipeConnection{read: os.NewFile(4, "supervisor replies"), write: os.NewFile(3, "supervisor requests")}
	} else {
		stream, err = net.DialTimeout("unix", os.Getenv(socketEnvironment), 3*time.Second)
	}
	if err != nil {
		return nil, fmt.Errorf("could not contact the diagnostic supervisor: %w", err)
	}
	bridge := &Bridge{connection: stream, encoder: json.NewEncoder(stream), decoder: json.NewDecoder(stream), Probe: os.Getenv(probeEnvironment) == "1"}
	answer, err := bridge.exchange(packet{Token: os.Getenv(tokenEnvironment), Operation: "hello", PID: os.Getpid(), Probe: bridge.Probe})
	if err != nil {
		stream.Close()
		return nil, err
	}
	bridge.RootOwner = answer.RootOwner
	// Descendant lego processes must not inherit the private worker handshake.
	for _, name := range []string{socketEnvironment, tokenEnvironment, probeEnvironment, pipeEnvironment} {
		_ = os.Unsetenv(name)
	}
	return bridge, nil
}
func (b *Bridge) exchange(message packet) (response, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var answer response
	if b.broken {
		return answer, fmt.Errorf("diagnostic supervisor connection was lost")
	}
	_ = b.connection.SetDeadline(time.Now().Add(3 * time.Second))
	err := b.encoder.Encode(message)
	if err == nil {
		err = b.decoder.Decode(&answer)
	}
	if err != nil {
		b.broken = true
		return answer, err
	}
	if answer.Error != "" {
		return answer, fmt.Errorf("%s", answer.Error)
	}
	return answer, nil
}
func (b *Bridge) Record(kind, text string) { _, _ = b.exchange(packet{Operation: kind, Text: text}) }
func (b *Bridge) PrepareOutput() error {
	_, err := b.exchange(packet{Operation: "prepare-output"})
	return err
}
func (b *Bridge) ProcessStarted(pid int) {
	_, _ = b.exchange(packet{Operation: "process-start", PID: pid})
}
func (b *Bridge) ProcessFinished(pid int) {
	_, _ = b.exchange(packet{Operation: "process-end", PID: pid})
}
func (b *Bridge) RecordProcessOutput(pid int, text string) {
	_, _ = b.exchange(packet{Operation: "process-output", PID: pid, Text: text})
}
func (b *Bridge) Finish(status int) {
	stack := make([]byte, 128<<10)
	n := runtime.Stack(stack, true)
	_, _ = b.exchange(packet{Operation: "finish", Status: status, Text: string(stack[:n])})
	_ = b.connection.Close()
}
func (b *Bridge) RecordPeriodic(done <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			stack := make([]byte, 64<<10)
			n := runtime.Stack(stack, true)
			b.Record("snapshot", string(stack[:n]))
		}
	}
}

func workerEnvironment(path, token string, probe, pipe bool) []string {
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != socketEnvironment && key != tokenEnvironment && key != probeEnvironment && key != pipeEnvironment {
			environment = append(environment, entry)
		}
	}
	environment = append(environment, socketEnvironment+"="+path, tokenEnvironment+"="+token)
	if pipe {
		environment = append(environment, pipeEnvironment+"=1")
	}
	if probe {
		environment = append(environment, probeEnvironment+"=1")
	}
	return environment
}
