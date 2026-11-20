// Temporary CI-only measurement wrapper, mounted rather than shipped in scratch.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: probe METRICS_FILE COMMAND [ARGS...]")
		os.Exit(2)
	}
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	start := time.Now()
	err := cmd.Run()
	metrics := struct {
		ElapsedSeconds float64 `json:"elapsed_seconds"`
		MaxRSSKiB      int64   `json:"wait4_max_rss_kib"`
		ExitCode       int     `json:"exit_code"`
		Scope          string  `json:"scope"`
	}{ElapsedSeconds: time.Since(start).Seconds(), ExitCode: 1, Scope: "Linux wait4 maximum task RSS including waited children; not simultaneous cgroup peak"}
	if cmd.ProcessState != nil {
		metrics.ExitCode = cmd.ProcessState.ExitCode()
		if usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			metrics.MaxRSSKiB = int64(usage.Maxrss)
		}
	}
	data, e := json.MarshalIndent(metrics, "", "  ")
	if e != nil || os.WriteFile(os.Args[1], append(data, '\n'), 0644) != nil {
		fmt.Fprintln(os.Stderr, "Could not save runtime metrics")
		os.Exit(1)
	}
	if err != nil {
		if metrics.ExitCode > 0 {
			os.Exit(metrics.ExitCode)
		}
		os.Exit(1)
	}
}
