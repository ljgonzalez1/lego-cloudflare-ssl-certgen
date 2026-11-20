package imagebuild

import (
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/licenseaudit"
	"os"
	"runtime"
	"strings"
)

func CollectLicenses(root, inputs string) error {
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
	progress("diagnostic revision=beta4-build-trace-r3; start license collection")
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
func VerifyLicenses(root string) error { return licenseaudit.Verify(root) }
