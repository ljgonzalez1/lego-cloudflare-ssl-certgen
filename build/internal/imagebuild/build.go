package imagebuild

import (
	"debug/buildinfo"
	"fmt"
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/debugrun"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

func TargetEnvironment() ([]string, error) {
	architecture := value("TARGETARCH", runtime.GOARCH)
	variant := os.Getenv("TARGETVARIANT")
	if _, err := ReleaseArchitecture(architecture, variant); err != nil {
		return nil, err
	}
	settings := map[string]string{"GOOS": "linux", "GOARCH": architecture, "CGO_ENABLED": "0", "GOAMD64": "v1", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": ""}
	if !debugrun.SupportsDelve("linux", architecture) {
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
	environment, err := TargetEnvironment()
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return err
	}
	linkerFlags := "-compressdwarf=false"
	if version := os.Getenv("APP_VERSION"); version != "" {
		if !versionPattern.MatchString("v" + version) {
			return fmt.Errorf("invalid APP_VERSION")
		}
		linkerFlags += " -X=main.applicationVersion=" + version
	}
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin/go"), "build", "-v", "-x", "-buildvcs=false", "-mod=vendor", "-gcflags=all=-N -l -d=checkptr=2", "-ldflags="+linkerFlags, "-o", output, "./cmd/certgen")
	command.Env = environment
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	Tracef("EXEC %q; target GOOS=linux GOARCH=%s GOARM=%s CGO_ENABLED=0", command.Args, value("TARGETARCH", runtime.GOARCH), os.Getenv("TARGETVARIANT"))
	return command.Run()
}
func copyFile(source, target string, mode os.FileMode) error {
	Tracef("COPY file %s -> %s mode=%04o", source, target, mode)
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return err
	}
	if err = os.WriteFile(target, contents, mode); err != nil {
		return err
	}
	Tracef("COPY complete %s bytes=%d", target, len(contents))
	return nil
}
func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			Tracef("MKDIR %s mode=0755", destination)
			return os.MkdirAll(destination, 0755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			Tracef("SYMLINK %s -> %s", destination, link)
			return os.Symlink(link, destination)
		}
		return copyFile(path, destination, 0644)
	})
}
func PrepareRuntime(root, licenses string) error {
	return prepareRuntimeFrom(root, licenses, "/")
}
func prepareRuntimeFrom(root, licenses, sourceRoot string) error {
	for _, dir := range []string{"usr/local/bin", "etc/ssl/certs", "usr/share/zoneinfo", "usr/share/licenses", "app", "tmp", "ssl-certs", "root"} {
		Tracef("MKDIR runtime %s mode=0755", filepath.Join(root, dir))
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(sourceRoot, "etc/ssl/certs/ca-certificates.crt"), filepath.Join(root, "etc/ssl/certs/ca-certificates.crt"), 0644); err != nil {
		return err
	}
	if err := copyTree(filepath.Join(sourceRoot, "usr/share/zoneinfo"), filepath.Join(root, "usr/share/zoneinfo")); err != nil {
		return err
	}
	for _, file := range []string{"/etc/alpine-release", "/etc/os-release"} {
		if err := copyFile(filepath.Join(sourceRoot, file), filepath.Join(root, file), 0644); err != nil {
			return err
		}
	}
	files := map[string]string{"etc/passwd": "root:x:0:0:root:/root:/sbin/nologin\ncertgen:x:1000:1000:certgen:/nonexistent:/sbin/nologin\n", "etc/group": "root:x:0:\ncertgen:x:1000:\n"}
	for path, contents := range files {
		Tracef("WRITE generated runtime file %s bytes=%d", path, len(contents))
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
	sources := map[string]string{filepath.Join(runtime.GOROOT(), "LICENSE"): "Go/LICENSE", "vendor/gopkg.in/yaml.v3/LICENSE": "go-yaml/LICENSE", "vendor/gopkg.in/yaml.v3/NOTICE": "go-yaml/NOTICE", filepath.Join(licenses, "MPL-2.0.txt"): "ca-certificates/MPL-2.0", filepath.Join(licenses, "project.txt"): "certgen/LICENSE"}
	for source, destination := range sources {
		if err := copyFile(source, filepath.Join(target, destination), 0644); err != nil {
			return err
		}
	}
	notice := "lego-cloudflare-certgen is MIT licensed.\n\nlego " + os.Getenv("LEGO_VERSION") + ": MIT, see lego/LICENSE\n  https://github.com/go-acme/lego\n\nGo " + runtime.Version() + ": BSD-3-Clause, see Go/LICENSE\n  https://go.dev/\n\ngo-yaml v3.0.1: MIT and Apache-2.0, see go-yaml/LICENSE and go-yaml/NOTICE\n  https://github.com/go-yaml/yaml/tree/v3.0.1\n\nCA certificate bundle: Mozilla trust store under MPL-2.0, see ca-certificates/MPL-2.0\n  https://curl.se/docs/caextract.html\n\nTimezone database: public domain, https://www.iana.org/time-zones\n"
	return os.WriteFile(filepath.Join(target, "NOTICE"), []byte(notice), 0644)
}
func escapedModule(text string) string {
	var result strings.Builder
	for _, ch := range text {
		if unicode.IsUpper(ch) {
			result.WriteByte('!')
			result.WriteRune(unicode.ToLower(ch))
		} else {
			result.WriteRune(ch)
		}
	}
	return result.String()
}
func BuildDelve(root string) error {
	architecture := value("TARGETARCH", runtime.GOARCH)
	if architecture == "arm" {
		fmt.Println("Delve does not support 32-bit ARM. The application retains DWARF, symbols and beta traces.")
		return nil
	}
	environment, err := TargetEnvironment()
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp("", "certgen-delve-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	for i := len(environment) - 1; i >= 0; i-- {
		key, _, _ := strings.Cut(environment[i], "=")
		if key == "GOPATH" || key == "GOBIN" || key == "GOMODCACHE" {
			environment = append(environment[:i], environment[i+1:]...)
		}
	}
	environment = append(environment, "GOPATH="+work, "GOMODCACHE="+filepath.Join(work, "pkg/mod"), "GOBIN=")
	version := value("DELVE_VERSION", "v1.27.2")
	if !versionPattern.MatchString(version) {
		return fmt.Errorf("invalid Delve version")
	}
	command := exec.Command(filepath.Join(runtime.GOROOT(), "bin/go"), "install", "-v", "-x", "-buildvcs=false", "-gcflags=all=-N -l", "-ldflags=-compressdwarf=false", "github.com/go-delve/delve/cmd/dlv@"+version)
	command.Env = environment
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	Tracef("EXEC %q; target GOARCH=%s CGO_ENABLED=0", command.Args, architecture)
	if err = command.Run(); err != nil {
		return err
	}
	source := filepath.Join(work, "bin")
	if architecture != runtime.GOARCH {
		source = filepath.Join(source, "linux_"+architecture)
	}
	binary := filepath.Join(root, "usr/local/bin/dlv")
	if err = copyFile(filepath.Join(source, "dlv"), binary, 0755); err != nil {
		return err
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return err
	}
	modules := append([]*structModule{}, moduleDescription(info.Main.Path, info.Main.Version))
	for _, dependency := range info.Deps {
		modules = append(modules, moduleDescription(dependency.Path, dependency.Version))
	}
	cache := filepath.Join(work, "pkg/mod")
	notice := "\nDelve and its Go modules. Each copied license is under delve-modules/.\n"
	for _, module := range modules {
		if module.Version == "" {
			continue
		}
		Tracef("Delve dependency notices: %s@%s", module.Path, module.Version)
		directory := filepath.Join(cache, escapedModule(module.Path)+"@"+escapedModule(module.Version))
		entries, err := os.ReadDir(directory)
		if err != nil {
			return err
		}
		copied := 0
		for _, entry := range entries {
			name := strings.ToLower(entry.Name())
			if entry.IsDir() || !(strings.HasPrefix(name, "license") || strings.HasPrefix(name, "notice") || strings.HasPrefix(name, "copying") || strings.HasPrefix(name, "copyright")) {
				continue
			}
			destination := filepath.Join(root, "usr/share/licenses/delve-modules", strings.ReplaceAll(module.Path, "/", "_"), entry.Name())
			if err = copyFile(filepath.Join(directory, entry.Name()), destination, 0644); err != nil {
				return err
			}
			copied++
		}
		if copied == 0 {
			return fmt.Errorf("cannot find the license of Delve dependency %s", module.Path)
		}
		notice += module.Path + " " + module.Version + "\n"
	}
	file, err := os.OpenFile(filepath.Join(root, "usr/share/licenses/NOTICE"), os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.WriteString(notice)
	return err
}

type structModule struct{ Path, Version string }

func moduleDescription(path, version string) *structModule { return &structModule{path, version} }
