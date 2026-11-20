//go:build linux

package certgen

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

func terminalAttributes(file *os.File) (syscall.Termios, error) {
	var attributes syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&attributes)))
	if errno != 0 {
		return attributes, errno
	}
	return attributes, nil
}

// Linux asm-generic termios requests shared by x86, ARM and ARM64.
func setTerminal(file *os.File, attributes *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(0x5404), uintptr(unsafe.Pointer(attributes)))
	if errno != 0 {
		return errno
	}
	return nil
}
func isTerminal(file *os.File) bool { _, err := terminalAttributes(file); return err == nil }
func flushTerminal(file *os.File) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), uintptr(0x540B), syscall.TCIFLUSH)
	if errno != 0 {
		return errno
	}
	return nil
}
func terminalSize(file *os.File) (rows, columns int) {
	var size struct{ Row, Column, X, Y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)))
	if errno == 0 {
		rows = int(size.Row)
		columns = int(size.Column)
	}
	if n, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && n > 0 && n <= 10000 {
		columns = n
	}
	if n, err := strconv.Atoi(os.Getenv("LINES")); err == nil && n > 0 && n <= 10000 {
		rows = n
	}
	if columns == 0 {
		columns = 80
	}
	return
}
func readyInput(file *os.File, wait time.Duration) (bool, error) {
	var set syscall.FdSet
	fd := int(file.Fd())
	wordBits := int(unsafe.Sizeof(set.Bits[0])) * 8
	if fd < 0 || fd >= len(set.Bits)*wordBits {
		return false, fmt.Errorf("terminal descriptor is out of range")
	}
	set.Bits[fd/wordBits] |= 1 << uint(fd%wordBits)
	timeout := syscall.NsecToTimeval(wait.Nanoseconds())
	count, err := syscall.Select(fd+1, &set, nil, nil, &timeout)
	if err == syscall.EINTR {
		return false, nil
	}
	return count > 0, err
}
func dropPrivileges(identity Identity) error {
	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("Cannot clear supplementary groups: %w", err)
	}
	if err := syscall.Setgid(identity.GID); err != nil {
		return fmt.Errorf("Cannot switch group: %w", err)
	}
	if err := syscall.Setuid(identity.UID); err != nil {
		return fmt.Errorf("Cannot switch user: %w", err)
	}
	return nil
}
func syscallMenuLocalFlags() uint32 {
	return syscall.ICANON | syscall.ECHO | syscall.ISIG | syscall.IEXTEN
}
func syscallMenuInputFlags() uint32 { return syscall.IXON | syscall.IXOFF }
func setMenuControlBytes(term *syscall.Termios) {
	term.Cc[syscall.VMIN] = 1
	term.Cc[syscall.VTIME] = 0
}
