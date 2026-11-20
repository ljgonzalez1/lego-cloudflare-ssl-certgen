// Package debugrun supervises the application without imposing debugger support
// on architectures where Delve cannot execute Go programs.
package debugrun

import (
	"runtime"
	"runtime/debug"
	"strings"
)

func SupportsDelve(goos, architecture string) bool {
	return goos == "linux" && (architecture == "amd64" || architecture == "arm64" || architecture == "386")
}

func Architecture() string {
	if runtime.GOARCH == "386" {
		return "i386"
	}
	if runtime.GOARCH != "arm" {
		return runtime.GOARCH
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "GOARM" {
				level, _, _ := strings.Cut(setting.Value, ",")
				if level == "6" || level == "7" {
					return "armv" + level
				}
			}
		}
	}
	return "arm"
}
