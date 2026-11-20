package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/certgen"
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/imagebuild"
)

func run() (status int) {
	defer func() {
		if failure := recover(); failure != nil {
			fmt.Fprintf(os.Stderr, "Build panic: %v\n%s", failure, debug.Stack())
			status = 1
		}
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
	case len(args) == 2 && args[0] == "verify-licenses":
		err = imagebuild.VerifyLicenses(args[1])
	default:
		fmt.Fprintln(os.Stderr, "Usage: certgen-build licenses ROOT INPUTS | verify-licenses ROOT | download OUTPUT | prepare ROOT LICENSES | compile OUTPUT | delve ROOT | verify-debug BINARY")
		return 2
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Build error: %v\n%s", err, debug.Stack())
		return 1
	}
	return 0
}
func main() { os.Exit(run()) }
