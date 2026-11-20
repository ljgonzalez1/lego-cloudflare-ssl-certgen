package main

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/certgen"
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/imagebuild"
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/licenseaudit"
)

func run() (status int) {
	platform := os.Getenv("TARGETARCH")
	if platform == "" {
		platform = runtime.GOARCH
	}
	if variant := os.Getenv("TARGETVARIANT"); variant != "" {
		platform += "/" + variant
	}
	progress, stop := licenseaudit.NewProgressLog(os.Stderr, "build linux/"+platform)
	defer stop()
	imagebuild.Progress = progress
	started := time.Now()
	progress("revision=beta5 START command=%q toolchain=%s", os.Args[1:], runtime.Version())
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
	case len(args) == 3 && args[0] == "prepare":
		err = imagebuild.PrepareRuntime(args[1], args[2])
	case len(args) == 2 && args[0] == "compile":
		err = imagebuild.Compile(args[1])
	case len(args) == 2 && args[0] == "delve":
		err = imagebuild.BuildDelve(args[1])
	case len(args) == 2 && args[0] == "verify-debug":
		err = certgen.CheckDebugBinary(args[1])
	case len(args) == 3 && args[0] == "licenses":
		err = imagebuild.CollectLicenses(args[1], args[2])
	case len(args) == 4 && args[0] == "prepare-license-bundle":
		err = imagebuild.PrepareLicenseBundle(args[1], args[2], args[3])
	case len(args) == 2 && args[0] == "verify-licenses":
		err = imagebuild.VerifyLicenses(args[1])
	default:
		fmt.Fprintln(os.Stderr, "Usage: certgen-build licenses ROOT INPUTS | prepare-license-bundle ROOT INPUTS OUTPUT | verify-licenses ROOT | download OUTPUT | prepare ROOT LICENSES | compile OUTPUT | delve ROOT | verify-debug BINARY")
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Build error: %v\n%s", err, debug.Stack())
		return 1
	}
	return 0
}
func main() { os.Exit(run()) }
