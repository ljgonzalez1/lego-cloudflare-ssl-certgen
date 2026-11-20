package debugrun

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"unsafe"
)

func ioctl(fd uintptr, request uintptr, pointer unsafe.Pointer) error {
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(pointer))
	if err != 0 {
		return err
	}
	return nil
}
func terminal(file *os.File) bool {
	var state syscall.Termios
	return ioctl(file.Fd(), syscall.TCGETS, unsafe.Pointer(&state)) == nil
}

type outputRelay struct {
	destination *os.File
	child       *os.File
	reader      *os.File
	stream      *streamWriter
	done        chan struct{}
}

func newRelay(destination *os.File, session *Session, source string) (*outputRelay, error) {
	relay := &outputRelay{destination: destination, stream: session.Stream(source), done: make(chan struct{})}
	var err error
	if terminal(destination) {
		relay.reader, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
		if err != nil {
			return nil, err
		}
		unlock := int32(0)
		if err = ioctl(relay.reader.Fd(), 0x40045431, unsafe.Pointer(&unlock)); err != nil {
			relay.reader.Close()
			return nil, err
		}
		var number uint32
		if err = ioctl(relay.reader.Fd(), 0x80045430, unsafe.Pointer(&number)); err != nil {
			relay.reader.Close()
			return nil, err
		}
		relay.child, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|syscall.O_NOCTTY, 0)
		if err != nil {
			relay.reader.Close()
			return nil, err
		}
		var state syscall.Termios
		if err = ioctl(relay.child.Fd(), syscall.TCGETS, unsafe.Pointer(&state)); err != nil {
			relay.close()
			return nil, err
		}
		// The real terminal already applies newline processing. Avoid CRCRLF.
		state.Oflag &^= syscall.OPOST
		if err = ioctl(relay.child.Fd(), 0x5402, unsafe.Pointer(&state)); err != nil {
			relay.close()
			return nil, err
		}
		relay.resize()
	} else {
		relay.reader, relay.child, err = os.Pipe()
		if err != nil {
			return nil, err
		}
	}
	return relay, nil
}
func (r *outputRelay) resize() {
	if !terminal(r.destination) {
		return
	}
	var size [4]uint16
	if ioctl(r.destination.Fd(), syscall.TIOCGWINSZ, unsafe.Pointer(&size)) == nil {
		_ = ioctl(r.reader.Fd(), syscall.TIOCSWINSZ, unsafe.Pointer(&size))
	}
}
func (r *outputRelay) start() {
	go func() {
		defer close(r.done)
		defer r.stream.Flush()
		buffer := make([]byte, 4096)
		for {
			n, err := r.reader.Read(buffer)
			if n > 0 {
				_, _ = r.destination.Write(buffer[:n])
				_, _ = r.stream.Write(buffer[:n])
			}
			if err != nil {
				return
			}
		}
	}()
}
func (r *outputRelay) close() {
	if r.child != nil {
		_ = r.child.Close()
	}
	if r.reader != nil {
		_ = r.reader.Close()
	}
}
func (r *outputRelay) childStarted() { _ = r.child.Close() }

type terminalRestore struct {
	file  *os.File
	state syscall.Termios
	group int32
	valid bool
}

func captureTerminal(file *os.File) terminalRestore {
	saved := terminalRestore{file: file}
	if ioctl(file.Fd(), syscall.TCGETS, unsafe.Pointer(&saved.state)) == nil {
		saved.valid = true
		_ = ioctl(file.Fd(), syscall.TIOCGPGRP, unsafe.Pointer(&saved.group))
	}
	return saved
}
func (s terminalRestore) restore() {
	if !s.valid {
		return
	}
	signal.Ignore(syscall.SIGTTOU)
	if s.group > 0 {
		_ = ioctl(s.file.Fd(), syscall.TIOCSPGRP, unsafe.Pointer(&s.group))
	}
	_ = ioctl(s.file.Fd(), 0x5402, unsafe.Pointer(&s.state))
}

var _ io.Writer = (*streamWriter)(nil)
