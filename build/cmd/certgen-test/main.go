//go:build certgen_test

// This executable exposes internal helpers to the offline regression suite.
// It is never built or copied into the production image.
package main

import (
	"os"
	"path/filepath"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/certgen"
)

func main() {
	if filepath.Base(os.Args[0]) == "certgen-core" {
		os.Exit(certgen.RunHelper(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(certgen.RunCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
