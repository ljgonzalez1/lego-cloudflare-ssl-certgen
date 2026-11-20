package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/certgen"
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/imagebuild"
)

func run() (status int) {
	platform := os.Getenv("TARGETARCH")
	if platform == "" {
		platform = runtime.GOARCH
	}
	if variant := os.Getenv("TARGETVARIANT"); variant != "" {
		platform += "/" + variant
	}
	progress, stop := imagebuild.NewProgressLog(os.Stderr, "linux/"+platform)
	defer stop()
	imagebuild.Progress = progress
	started := time.Now()
	progress("revision=beta7 START command=%q toolchain=%s", os.Args[1:], runtime.Version())
	defer func() {
		if failure := recover(); failure != nil {
			fmt.Fprintf(os.Stderr, "Build panic: %v\n%s", failure, debug.Stack())
			status = 1
		}
		progress("END command=%q status=%d duration=%s", os.Args[1:], status, time.Since(started).Round(time.Millisecond))
	}()
	args := os.Args[1:]
	var err error
	switch {
	case len(args) == 2 && args[0] == "download":
		err = imagebuild.DownloadFromEnvironment(args[1])
	case len(args) == 2 && args[0] == "prepare":
		err = imagebuild.PrepareRuntime(args[1])
	case len(args) == 2 && args[0] == "compile":
		err = imagebuild.Compile(args[1])
	case len(args) == 2 && args[0] == "delve":
		err = imagebuild.BuildDelve(args[1])
	case len(args) == 2 && args[0] == "verify-debug":
		err = certgen.CheckDebugBinary(args[1])
	default:
		fmt.Fprintln(os.Stderr, "Usage: certgen-build download OUTPUT | prepare ROOT | compile OUTPUT | verify-debug BINARY | delve ROOT")
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Build error: %v\n%s", err, debug.Stack())
		return 1
	}
	return 0
}
func main() { os.Exit(run()) }
