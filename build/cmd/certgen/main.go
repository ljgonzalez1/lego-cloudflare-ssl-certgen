package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/certgen"
)

var applicationVersion = "6.0.1"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Printf("certgen version %s %s/%s\n", applicationVersion, runtime.GOOS, runtime.GOARCH)
		return
	}
	os.Exit(certgen.RunCLI(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
