package imagebuild

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// A trimmed helper has no embedded GOROOT. Ask the native compiler rather than
// resolving a relative bin/go path inside the source tree.
func compilerRoot() (string, error) {
	if root := runtime.GOROOT(); root != "" {
		return root, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := exec.CommandContext(ctx, value("GO", "go"), "env", "GOROOT").Output()
	if err != nil {
		return "", fmt.Errorf("cannot locate native Go toolchain: %w", err)
	}
	root := strings.TrimSpace(string(result))
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("native Go toolchain returned an invalid GOROOT")
	}
	return root, nil
}

func TargetEnvironment() ([]string, error) {
	architecture := value("TARGETARCH", runtime.GOARCH)
	variant := os.Getenv("TARGETVARIANT")
	if _, err := ReleaseArchitecture(architecture, variant); err != nil {
		return nil, err
	}
	settings := map[string]string{"GOOS": "linux", "GOARCH": architecture, "CGO_ENABLED": "0", "GOAMD64": "v1", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": ""}
	if architecture == "arm" {
		settings["GOARM"] = strings.TrimPrefix(variant, "v")
	} else {
		settings["GOARM"] = ""
	}
	environment := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, replaced := settings[key]; !replaced {
			environment = append(environment, entry)
		}
	}
	for key, val := range settings {
		environment = append(environment, key+"="+val)
	}
	return environment, nil
}
func Compile(output string) error {
	root, err := compilerRoot()
	if err != nil {
		return err
	}
	environment, err := TargetEnvironment()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	linkerFlags := "-s -w -buildid="
	if version := os.Getenv("APP_VERSION"); version != "" {
		if !versionPattern.MatchString("v" + version) {
			return fmt.Errorf("invalid APP_VERSION")
		}
		linkerFlags += " -X=main.applicationVersion=" + version
	}
	command := exec.Command(filepath.Join(root, "bin/go"), "build", "-buildvcs=false", "-mod=vendor", "-trimpath", "-tags=netgo,osusergo", "-ldflags="+linkerFlags, "-o", output, "./cmd/certgen")
	command.Env = environment
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}
func copyFile(source, target string, mode os.FileMode) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	return os.WriteFile(target, contents, mode)
}
func PrepareRuntime(root, licenses string) error {
	return prepareRuntimeFrom(root, licenses, "/")
}
func prepareRuntimeFrom(root, licenses, sourceRoot string) error {
	toolchain, err := compilerRoot()
	if err != nil {
		return err
	}
	for _, dir := range []string{"usr/local/bin", "etc/ssl/certs", "usr/share/licenses", "app", "tmp", "ssl-certs", "root"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(sourceRoot, "etc/ssl/certs/ca-certificates.crt"), filepath.Join(root, "etc/ssl/certs/ca-certificates.crt"), 0644); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(toolchain, "lib/time/zoneinfo.zip"), filepath.Join(root, "usr/share/zoneinfo.zip"), 0644); err != nil {
		return err
	}
	files := map[string]string{"etc/passwd": "root:x:0:0:root:/root:/sbin/nologin\ncertgen:x:1000:1000:certgen:/nonexistent:/sbin/nologin\n", "etc/group": "root:x:0:\ncertgen:x:1000:\n"}
	for path, contents := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(contents), 0644); err != nil {
			return err
		}
	}
	if err := os.Chmod(filepath.Join(root, "tmp"), os.ModeSticky|0777); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(filepath.Join(root, "ssl-certs"), 1000, 1000); err != nil {
			return err
		}
	}
	target := filepath.Join(root, "usr/share/licenses")
	sources := map[string]string{filepath.Join(toolchain, "LICENSE"): "Go/LICENSE", "vendor/gopkg.in/yaml.v3/LICENSE": "go-yaml/LICENSE", "vendor/gopkg.in/yaml.v3/NOTICE": "go-yaml/NOTICE", filepath.Join(licenses, "MPL-2.0.txt"): "ca-certificates/MPL-2.0", filepath.Join(licenses, "project.txt"): "certgen/LICENSE"}
	for source, destination := range sources {
		if err := copyFile(source, filepath.Join(target, destination), 0644); err != nil {
			return err
		}
	}
	notice := "lego-cloudflare-certgen is MIT licensed.\n\nlego " + os.Getenv("LEGO_VERSION") + ": MIT, see lego/LICENSE\n  https://github.com/go-acme/lego\n\nGo " + runtime.Version() + ": BSD-3-Clause, see Go/LICENSE\n  https://go.dev/\n\ngo-yaml v3.0.1: MIT and Apache-2.0, see go-yaml/LICENSE and go-yaml/NOTICE\n  https://github.com/go-yaml/yaml/tree/v3.0.1\n\nCA certificate bundle: Mozilla trust store under MPL-2.0, see ca-certificates/MPL-2.0\n  https://curl.se/docs/caextract.html\n\nTimezone database: public domain, https://www.iana.org/time-zones\n"
	return os.WriteFile(filepath.Join(target, "NOTICE"), []byte(notice), 0644)
}
