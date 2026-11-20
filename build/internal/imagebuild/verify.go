package imagebuild

import (
	"debug/buildinfo"
	"debug/elf"
	"fmt"
	"os"
	"strings"
)

// CheckProductionBinary runs on the native builder, never in the final image.
// ELF inspection does not execute a foreign binary or depend on binutils.
func CheckProductionBinary(path string) error {
	binary, err := elf.Open(path)
	if err != nil {
		return err
	}
	defer binary.Close()
	for _, section := range binary.Sections {
		if strings.HasPrefix(section.Name, ".debug_") || strings.HasPrefix(section.Name, ".zdebug_") || section.Name == ".symtab" {
			return fmt.Errorf("production binary retains debug section %s", section.Name)
		}
	}
	for _, segment := range binary.Progs {
		if segment.Type == elf.PT_INTERP || segment.Type == elf.PT_DYNAMIC {
			return fmt.Errorf("production binary requires a dynamic runtime")
		}
	}
	if arch := os.Getenv("TARGETARCH"); arch != "" {
		expected := map[string]elf.Machine{"amd64": elf.EM_X86_64, "386": elf.EM_386, "arm": elf.EM_ARM, "arm64": elf.EM_AARCH64}
		machine, ok := expected[arch]
		if !ok || binary.Machine != machine {
			return fmt.Errorf("ELF architecture does not match %s", arch)
		}
	}
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		return err
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["CGO_ENABLED"] != "0" || settings["-trimpath"] != "true" || settings["-gcflags"] != "" {
		return fmt.Errorf("binary must use static, trimmed, optimized production settings")
	}
	return nil
}
