package debugrun

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Session struct {
	mu             sync.Mutex
	file           *os.File
	directory      *os.File
	filename       string
	Path, DumpPath string
	secrets        []string
	longest        int
	failed         bool
	warned         bool
	errors         io.Writer
}

func ReadSecrets() []string {
	var secrets []string
	for _, name := range []string{"CLOUDFLARE_API_TOKEN", "CLOUDFLARE_API_KEY", "CF_DNS_API_TOKEN", "CLOUDFLARE_DNS_API_TOKEN", "CERTGEN_INTERNAL_TOKEN"} {
		if value := os.Getenv(name); value != "" {
			secrets = append(secrets, value)
		}
	}
	paths := []string{"/run/secrets/cloudflare_api_token", os.Getenv("CF_DNS_API_TOKEN_FILE"), os.Getenv("CLOUDFLARE_DNS_API_TOKEN_FILE")}
	for _, path := range paths {
		if path == "" {
			continue
		}
		file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			continue
		}
		info, err := file.Stat()
		if err == nil && info.Mode().IsRegular() {
			value, err := io.ReadAll(io.LimitReader(file, 4097))
			if err == nil && len(value) <= 4096 {
				token := strings.TrimRight(string(value), "\r\n")
				if token != "" {
					secrets = append(secrets, token)
				}
			}
		}
		file.Close()
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return secrets
}

func openDirectory(path string, ownerUID, ownerGID int) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Never follow caller-controlled links while opening a diagnostic directory.
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(absolute, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		err = os.Mkdir(current, 0700)
		if err == nil && os.Geteuid() == 0 {
			_ = os.Chown(current, ownerUID, ownerGID)
		}
		if err != nil && !os.IsExist(err) {
			return nil, err
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("diagnostic path must contain ordinary directories: %s", current)
		}
	}
	fd, err := syscall.Open(absolute, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), absolute), nil
}

func OpenSession(directory, version, architecture string, ownerUID, ownerGID int, errors io.Writer) (*Session, error) {
	if strings.ContainsAny(version+architecture, "/\\\x00\r\n") {
		return nil, fmt.Errorf("invalid diagnostic filename")
	}
	folder, err := openDirectory(directory, ownerUID, ownerGID)
	if err != nil {
		return nil, err
	}
	if os.Geteuid() == 0 {
		_ = folder.Chown(ownerUID, ownerGID)
	}
	closeError := func(err error) (*Session, error) { folder.Close(); return nil, err }
	zone, err := time.LoadLocation(os.Getenv("TZ"))
	if err != nil || zone == nil {
		zone = time.UTC
	}
	stamp := time.Now().In(zone)
	for attempt := 0; attempt < 3600; attempt++ {
		// The requested order is YEAR-DAY-MONTH, deliberately not YEAR-MONTH-DAY.
		name := "v" + strings.TrimPrefix(version, "v") + "." + formatTimestamp(stamp) + "." + architecture + ".log"
		fd, err := syscall.Openat(int(folder.Fd()), name, syscall.O_RDWR|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
		if err == syscall.EEXIST {
			stamp = stamp.Add(time.Second)
			continue
		}
		if err != nil {
			return closeError(err)
		}
		file := os.NewFile(uintptr(fd), filepath.Join(directory, name))
		if os.Geteuid() == 0 {
			_ = file.Chown(ownerUID, ownerGID)
		}
		session := &Session{file: file, directory: folder, filename: name, Path: file.Name(), DumpPath: filepath.Join(directory, "dump."+name), secrets: ReadSecrets(), errors: errors}
		for _, secret := range session.secrets {
			session.longest = max(session.longest, len(secret))
		}
		session.Record("supervisor", fmt.Sprintf("Started v%s on %s; PID %d; timestamp uses YYYY-DD-MM", strings.TrimPrefix(version, "v"), architecture, os.Getpid()))
		return session, nil
	}
	return closeError(fmt.Errorf("could not reserve a unique diagnostic timestamp"))
}

func formatTimestamp(stamp time.Time) string {
	return fmt.Sprintf("%04d-%02d-%02d_%02d.%02d.%02d", stamp.Year(), stamp.Day(), int(stamp.Month()), stamp.Hour(), stamp.Minute(), stamp.Second())
}

func (s *Session) Safe(text string) string {
	for _, secret := range s.secrets {
		text = strings.ReplaceAll(text, secret, "[REDACTED]")
	}
	return text
}
func (s *Session) write(data []byte) {
	if s.file == nil {
		return
	}
	if _, err := s.file.Write(data); err != nil && !s.warned {
		s.warned = true
		fmt.Fprintf(s.errors, "Warning: diagnostic output could not be saved: %v\n", err)
	}
}
func (s *Session) Record(source, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.write([]byte(fmt.Sprintf("[%s] [%s] %s\n", time.Now().UTC().Format(time.RFC3339Nano), source, s.Safe(text))))
}
func (s *Session) MarkFailure(text string) {
	s.mu.Lock()
	s.failed = true
	s.mu.Unlock()
	s.Record("failure", text)
}

// streamWriter emits whole lines when possible, retaining a possible secret
// suffix. Console output bypasses it to preserve menu responsiveness.
type streamWriter struct {
	s       *Session
	pending []byte
	source  string
}

func (s *Session) Stream(source ...string) *streamWriter {
	name := "output"
	if len(source) > 0 {
		name = source[0]
	}
	return &streamWriter{s: s, source: name}
}
func (w *streamWriter) Write(data []byte) (int, error) {
	w.pending = append(w.pending, data...)
	keep := max(0, w.s.longest-1)
	amount := max(0, len(w.pending)-keep)
	// Emit a whole replacement atomically; never split original secret bytes.
	if amount > 0 {
		boundary := amount
		if at := bytes.LastIndexByte(w.pending[:boundary], '\n'); at >= 0 {
			boundary = at + 1
		} else if len(w.pending) < 64<<10 {
			return len(data), nil
		}
		for _, secret := range w.s.secrets {
			from := 0
			for from < len(w.pending) {
				at := bytes.Index(w.pending[from:], []byte(secret))
				if at < 0 {
					break
				}
				at += from
				if at < boundary && at+len(secret) > boundary {
					boundary = at
				}
				from = at + len(secret)
			}
		}
		if boundary > 0 {
			w.s.Record(w.source, string(w.pending[:boundary]))
			w.pending = append(w.pending[:0], w.pending[boundary:]...)
		}
	}
	return len(data), nil
}
func (w *streamWriter) Flush() {
	if len(w.pending) > 0 {
		w.s.Record(w.source, string(w.pending))
	}
	w.pending = nil
}

func (s *Session) Finalize(status int) {
	if s.file == nil {
		return
	}
	s.Record("supervisor", fmt.Sprintf("Execution finished with exit status %d", status))
	s.mu.Lock()
	defer s.mu.Unlock()
	if status != 0 || s.failed {
		fd, err := syscall.Openat(int(s.directory.Fd()), "dump."+s.filename, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
		if err == nil {
			dump := os.NewFile(uintptr(fd), s.DumpPath)
			if info, e := s.file.Stat(); e == nil {
				if owner, ok := info.Sys().(*syscall.Stat_t); ok && os.Geteuid() == 0 {
					_ = dump.Chown(int(owner.Uid), int(owner.Gid))
				}
				fmt.Fprintf(dump, "Diagnostic dump before supervisor shutdown; target exit status %d\nThis is a text stack/event dump, not a memory core.\n\n", status)
				_, err = io.Copy(dump, io.NewSectionReader(s.file, 0, info.Size()))
			}
			stack := make([]byte, 128<<10)
			n := runtime.Stack(stack, true)
			fmt.Fprintf(dump, "\nSupervisor goroutines after target completion:\n%s\n", s.Safe(string(stack[:n])))
			_ = dump.Sync()
			_ = dump.Close()
		}
		if err != nil {
			fmt.Fprintf(s.errors, "Warning: diagnostic dump could not be saved: %v\n", err)
		}
	}
	_ = s.file.Sync()
	_ = s.file.Close()
	_ = s.directory.Close()
}
