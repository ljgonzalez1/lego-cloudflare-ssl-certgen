package certgen

import (
	"bytes"
	"debug/buildinfo"
	"debug/elf"
	"encoding/json"
	"fmt"
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/debugrun"
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
	dir, err := os.MkdirTemp("", "certgen-image-test-")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(dir)
	if err = os.Chmod(dir, 0755); err != nil {
		return fail(err)
	}
	if err = os.Setenv("CERTGEN_LOG_DIR", filepath.Join(dir, "debug")); err != nil {
		return fail(err)
	}
	delve := "/usr/local/bin/dlv"
	debuggerEnabled := false
	reason := "Delve is not supported on this architecture"
	if debugrun.SupportsDelve(runtime.GOOS, runtime.GOARCH) {
		fmt.Fprintln(output, "[image-self-test] START debugger execution probe")
		probe := exec.Command(executable, "debugger-probe")
		probe.Stdout = output
		probe.Stderr = errorOutput
		if err = probe.Run(); err == nil {
			debuggerEnabled = true
			reason = "Delve executed the real Go probe"
		} else {
			reason = "Delve could not execute the Go probe; using portable Go diagnostics"
		}
		fmt.Fprintln(output, "[image-self-test] debugger probe result:", reason)
	}
	if !debuggerEnabled {
		if err = os.Remove(delve); err != nil && !os.IsNotExist(err) {
			return fail(err)
		}
		fmt.Fprintln(output, "Image self-test:", reason)
	}
	if err = os.MkdirAll("/usr/share/certgen", 0755); err != nil {
		return fail(err)
	}
	manifest, _ := json.Marshal(map[string]any{"architecture": debugrun.Architecture(), "delve": debuggerEnabled, "reason": reason, "go_traces": true})
	if err = os.WriteFile("/usr/share/certgen/debugger.json", manifest, 0644); err != nil {
		return fail(err)
	}
	config := filepath.Join(dir, "domains.yaml")
	if err = os.WriteFile(config, []byte("smoke: [example.com, www.example.com, '*.example.com']\n"), 0644); err != nil {
		return fail(err)
	}
	target := filepath.Join(dir, "no-orders")
	fmt.Fprintln(output, "[image-self-test] START offline CLI preview and lego architecture check; expected status=0")
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
	fmt.Fprintln(output, "[image-self-test] PASS offline CLI preview; no issuance files created")
	for _, args := range [][]string{{"--config", config}, {"--config", config, "--test", "--production"}} {
		fmt.Fprintf(output, "[image-self-test] START negative CLI check args=%q; expected status=2\n", args)
		command = exec.Command(executable, args...)
		command.Stdin = nil
		command.Stdout = output
		command.Stderr = errorOutput
		err = command.Run()
		failure, ok := err.(*exec.ExitError)
		if !ok || failure.ExitCode() != 2 {
			return fail(fmt.Errorf("invalid unattended invocation was accepted"))
		}
		fmt.Fprintln(output, "[image-self-test] PASS negative CLI check; observed status=2")
	}
	fmt.Fprintln(output, "[image-self-test] PASS all image checks")
	return 0
}
