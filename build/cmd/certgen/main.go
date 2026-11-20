package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/certgen"
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/debugrun"
)

var applicationVersion = "6.0.1-beta-2"

func runWorker(bridge *debugrun.Bridge) (status int) {
	diagnostics := certgen.NewDiagnostics(os.Stderr)
	if bridge != nil {
		diagnostics.Recorder = bridge
		done := make(chan struct{})
		go bridge.RecordPeriodic(done)
		defer func() { close(done); bridge.Finish(status) }()
	}
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
	if bridge == nil && filepath.Base(os.Args[0]) == "certgen-core" {
		return certgen.RunHelper(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
	}
	signals := certgen.WatchSignals()
	defer signals.Close()
	app := certgen.App{Input: os.Stdin, Output: os.Stdout, Error: os.Stderr, Signals: signals, Diagnostics: diagnostics}
	if bridge != nil {
		app.ParentOwnsOutput = bridge.RootOwner
		app.PrepareOutput = bridge.PrepareOutput
	}
	return app.Run(os.Args[1:])
}

func run() int {
	if debugrun.IsWorker() {
		bridge, err := debugrun.ConnectWorker()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Diagnostic supervisor:", err)
			return 1
		}
		if bridge.Probe {
			bridge.Finish(0)
			return 0
		}
		return runWorker(bridge)
	}
	if filepath.Base(os.Args[0]) == "certgen-core" || len(os.Args) > 1 && os.Args[1] == "image-self-test" {
		return runWorker(nil)
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	identity, identityErr := certgen.ReadIdentity()
	options, optionsErr := certgen.ParseOptions(os.Args[1:])
	probeOnly := len(os.Args) == 2 && os.Args[1] == "debugger-probe"
	return debugrun.Run(debugrun.Options{Executable: executable, Arguments: os.Args[1:], Version: applicationVersion, UID: identity.UID, GID: identity.GID, IdentityValid: identityErr == nil, PrepareOutput: !probeOnly && optionsErr == nil && options.Action == "issue" && options.Output == "/ssl-certs", ProbeOnly: probeOnly})
}
func main() { os.Exit(run()) }
