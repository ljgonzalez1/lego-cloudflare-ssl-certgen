// This executable is an offline crash fixture. It is never copied into images.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/debugrun"
)

func run() int {
	if len(os.Args) > 1 && os.Args[1] == "descendant" {
		for {
			time.Sleep(time.Hour)
		}
	}
	if !debugrun.IsWorker() {
		path, _ := os.Executable()
		return debugrun.Run(debugrun.Options{Executable: path, Version: "6.0.1-beta-1", Debugger: filepath.Join(os.TempDir(), "nonexistent-certgen-delve")})
	}
	bridge, err := debugrun.ConnectWorker()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if bridge.Probe {
		bridge.Finish(0)
		return 0
	}
	if os.Getenv("CERTGEN_FIXTURE_RAW") == "1" {
		var state syscall.Termios
		_, _, err := syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&state)))
		if err == 0 {
			state.Lflag &^= syscall.ICANON | syscall.ECHO | syscall.ISIG
			_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, os.Stdin.Fd(), 0x5402, uintptr(unsafe.Pointer(&state)))
		}
	}
	stack := make([]byte, 64<<10)
	n := runtime.Stack(stack, true)
	bridge.Record("snapshot", string(stack[:n]))
	fmt.Fprintln(os.Stdout, "fixture stdout", os.Getenv("CLOUDFLARE_API_TOKEN"))
	fmt.Fprintln(os.Stderr, "fixture stderr")
	if path := os.Getenv("CERTGEN_FIXTURE_PID"); path != "" {
		_ = os.WriteFile(path, []byte(fmt.Sprint(os.Getpid())), 0600)
	}
	switch os.Getenv("CERTGEN_FIXTURE_MODE") {
	case "panic":
		go func() { panic("unrecovered fixture panic") }()
		for {
			time.Sleep(time.Hour)
		}
	case "exit":
		os.Exit(7)
	case "kill":
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		for {
			time.Sleep(time.Hour)
		}
	case "descendant":
		executable, _ := os.Executable()
		child := exec.Command(executable, "descendant")
		child.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			panic(err)
		}
		bridge.ProcessStarted(child.Process.Pid)
		if path := os.Getenv("CERTGEN_FIXTURE_DESCENDANT"); path != "" {
			_ = os.WriteFile(path, []byte(fmt.Sprint(child.Process.Pid)), 0600)
		}
		os.Exit(8)
	}
	bridge.Finish(0)
	return 0
}
func main() { os.Exit(run()) }
