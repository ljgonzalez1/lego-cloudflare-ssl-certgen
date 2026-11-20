package imagebuild

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/licenseaudit"
)

func CollectLicenses(root, inputs string) error {
	Tracef("load local license bundle; no network source resolver is enabled")
	bundle, err := licenseaudit.OpenBundle(filepath.Join(inputs, "third-party"))
	if err != nil {
		return fmt.Errorf("local license bundle: %w", err)
	}
	Tracef("local license bundle ready: %d components; verify actual target payload", len(bundle.Components))
	return licenseaudit.Collect(licenseaudit.Options{
		Root: root, Inputs: inputs, ProjectVersion: os.Getenv("APP_VERSION"),
		LegoVersion: "v" + strings.TrimPrefix(os.Getenv("LEGO_VERSION"), "v"),
		ResolveModule: bundle.Module, ResolveGo: bundle.GoSource,
		ResolveData: func(kind string) (licenseaudit.Source, error) { return bundle.Data(root, "/", kind) },
		Progress: Progress,
	})
}

// The shared native preparation stage is the only place license sources can be
// downloaded. Each target audit consumes the resulting text-only bundle offline.
func collectDownloadedLicenses(root, inputs string) error {
	platform := value("TARGETARCH", runtime.GOARCH)
	if variant := os.Getenv("TARGETVARIANT"); variant != "" {
		platform += "/" + variant
	}
	progress := Progress
	stop := func() {}
	if progress == nil {
		progress, stop = licenseaudit.NewProgressLog(os.Stderr, "linux/"+platform)
	}
	defer stop()
	progress("diagnostic revision=beta5; authenticate upstream license sources once")
	toolchain, err := licenseaudit.ToolchainRoot()
	if err != nil {
		return err
	}
	sources, err := licenseaudit.NewNativeSources(root, toolchain)
	if err != nil {
		return err
	}
	defer func() {
		progress("clean up temporary license source trees")
		sources.Close()
		progress("temporary license source trees removed")
	}()
	sources.Progress = progress
	err = licenseaudit.Collect(licenseaudit.Options{Root: root, Inputs: inputs, ProjectVersion: os.Getenv("APP_VERSION"), LegoVersion: "v" + strings.TrimPrefix(os.Getenv("LEGO_VERSION"), "v"), Toolchain: toolchain, ResolveModule: sources.Module, ResolveGo: sources.GoSource, ResolveData: sources.Data, Progress: progress})
	if err != nil {
		progress("FAILED: %v", err)
	} else {
		progress("license collection complete")
	}
	return err
}

func PrepareLicenseBundle(root, inputs, output string) error {
	refresh := os.Getenv("REFRESH_LICENSE_BUNDLE")
	if refresh != "" && refresh != "0" && refresh != "1" {
		return fmt.Errorf("REFRESH_LICENSE_BUNDLE must be 0 or 1")
	}
	if refresh != "1" {
		_, err := os.Stat(filepath.Join(inputs, "third-party/bundle.json"))
		if err == nil {
			Tracef("validate and reuse committed local license texts")
			bundle, err := licenseaudit.OpenBundle(filepath.Join(inputs, "third-party"))
			if err != nil {
				return err
			}
			Tracef("reuse %d bundled third-party components without source downloads", len(bundle.Components))
			return bundle.CopyTo(output)
		}
		if !os.IsNotExist(err) {
			return err
		}
	}
	Tracef("prepare shared license bundle; export it with bash ci/update-license-bundle.sh to retain it in the project")
	if err := PrepareRuntime(root, inputs); err != nil {
		return err
	}
	if err := Compile(filepath.Join(root, "usr/local/bin/certgen")); err != nil {
		return err
	}
	// Keep Delve for evidence discovery even if a target's ptrace probe later
	// removes it. The final target collector still inventories only retained code.
	if err := BuildDelve(root); err != nil {
		return err
	}
	download, err := os.MkdirTemp("", "certgen-license-lego-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(download)
	if err := DownloadFromEnvironment(download); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(download, "lego"), filepath.Join(root, "usr/local/bin/lego"), 0755); err != nil {
		return err
	}
	if err := copyTree("/src", filepath.Join(root, "src")); err != nil {
		return err
	}
	if err := collectDownloadedLicenses(root, inputs); err != nil {
		return err
	}
	Tracef("export original license texts, module sums and data fingerprints; exclude all executables and archives")
	return licenseaudit.ExportBundle(root, output)
}

func VerifyLicenses(root string) error { return licenseaudit.Verify(root) }
