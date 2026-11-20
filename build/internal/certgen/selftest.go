package certgen

import (
	"bytes"
	"debug/buildinfo"
	"debug/elf"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func CheckDebugBinary(path string) error {
	binary, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer binary.Close()
	for _, section := range []string{".debug_info", ".debug_line", ".debug_frame", ".symtab"} {
		if binary.Section(section) == nil {
			return fmt.Errorf("missing ELF section %s", section)
		}
		if binary.Section(section).Flags&elf.SHF_COMPRESSED != 0 {
			return fmt.Errorf("ELF section %s must remain uncompressed", section)
		}
	}
	if binary.Section(".zdebug_info") != nil {
		return fmt.Errorf("DWARF information must remain uncompressed")
	}
	if _, err = binary.DWARF(); err != nil {
		return fmt.Errorf("cannot read DWARF: %w", err)
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return err
	}
	flags := map[string]string{}
	for _, setting := range info.Settings {
		flags[setting.Key] = setting.Value
	}
	compilerFlags := strings.Join(strings.Fields(flags["-gcflags"]), " ")
	if compilerFlags != "all=-N -l -d=checkptr=2" || !strings.Contains(flags["-ldflags"], "-compressdwarf=false") {
		return fmt.Errorf("binary does not retain the requested debug compiler settings")
	}
	return nil
}
func ImageSelfTest(output, errorOutput *os.File) int {
	fail := func(err error) int { fmt.Fprintln(errorOutput, "Image self-test:", err); return 1 }
	for _, path := range []string{"/bin/sh", "/bin/bash", "/bin/busybox", "/bin/ash", "/sbin/su-exec", "/usr/bin/python3", "/usr/bin/g++", "/usr/bin/curl"} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			return fail(fmt.Errorf("unexpected runtime program: %s", path))
		}
	}
	for _, license := range []string{"NOTICE", "lego/LICENSE", "Go/LICENSE", "go-yaml/LICENSE", "go-yaml/NOTICE", "ca-certificates/MPL-2.0"} {
		if !nonemptyFile("/usr/share/licenses/" + license) {
			return fail(fmt.Errorf("missing license: %s", license))
		}
	}
	executable, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	if err = CheckDebugBinary(executable); err != nil {
		return fail(err)
	}
	delve := "/usr/local/bin/dlv"
	if runtime.GOARCH == "arm" {
		if _, err = os.Lstat(delve); !os.IsNotExist(err) {
			return fail(fmt.Errorf("Delve is unsupported on 32-bit ARM"))
		}
	} else {
		version, err := exec.Command(delve, "version").CombinedOutput()
		if err != nil || !bytes.Contains(version, []byte("Delve Debugger")) {
			return fail(fmt.Errorf("Delve does not run on this architecture: %s (%v)", version, err))
		}
	}
	dir, err := os.MkdirTemp("", "certgen-image-test-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(dir)
	if err = os.Chmod(dir, 0755); err != nil {
		return fail(err)
	}
	config := filepath.Join(dir, "domains.yaml")
	if err = os.WriteFile(config, []byte("smoke: [example.com, www.example.com, '*.example.com']\n"), 0644); err != nil {
		return fail(err)
	}
	target := filepath.Join(dir, "no-orders")
	command := exec.Command(executable, "--config", config, "--output", target, "--test")
	command.Stdin = nil
	command.Stderr = errorOutput
	command.Env = append(os.Environ(), "TZ=America/Santiago", "UID=1000", "GID=1000")
	plan, err := command.Output()
	if err != nil {
		return fail(err)
	}
	fmt.Fprint(output, string(plan))
	expected := "lego version " + strings.TrimPrefix(os.Getenv("LEGO_VERSION"), "v") + " linux/" + runtime.GOARCH
	if !bytes.Contains(plan, []byte(expected)) || !bytes.Contains(plan, []byte("Offline test passed.")) {
		return fail(fmt.Errorf("offline preview or lego architecture verification failed"))
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		return fail(fmt.Errorf("offline preview created output"))
	}
	for _, args := range [][]string{{"--config", config}, {"--config", config, "--test", "--production"}} {
		command = exec.Command(executable, args...)
		command.Stdin = nil
		command.Stdout = output
		command.Stderr = errorOutput
		err = command.Run()
		failure, ok := err.(*exec.ExitError)
		if !ok || failure.ExitCode() != 2 {
			return fail(fmt.Errorf("invalid unattended invocation was accepted"))
		}
	}
	return 0
}
