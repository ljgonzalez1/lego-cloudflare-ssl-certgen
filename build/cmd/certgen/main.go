package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/certgen"
)

func run() (status int) {
	diagnostics := certgen.NewDiagnostics(os.Stderr)
	defer func() {
		if failure := recover(); failure != nil {
			diagnostics.Failure(fmt.Sprintf("panic: %v", failure))
			status = 2
		} else if status != 0 {
			diagnostics.Failure(fmt.Sprintf("certgen exited with status %d", status))
		}
	}()
	if len(os.Args) > 1 && os.Args[1] == "image-self-test" {
		return certgen.ImageSelfTest(os.Stdout, os.Stderr)
	}
	if filepath.Base(os.Args[0]) == "certgen-core" {
		return certgen.RunHelper(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	}
	signals := certgen.WatchSignals()
	defer signals.Close()
	app := certgen.App{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr, Signals: signals, Diagnostics: diagnostics}
	return app.Run(os.Args[1:])
}
func main() { os.Exit(run()) }
