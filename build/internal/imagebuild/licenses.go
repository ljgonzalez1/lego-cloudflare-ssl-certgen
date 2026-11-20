package imagebuild

import (
	"github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/internal/licenseaudit"
	"os"
	"strings"
)

func CollectLicenses(root, inputs string) error {
	toolchain, err := licenseaudit.ToolchainRoot()
	if err != nil {
		return err
	}
	sources, err := licenseaudit.NewNativeSources(root, toolchain)
	if err != nil {
		return err
	}
	defer sources.Close()
	return licenseaudit.Collect(licenseaudit.Options{Root: root, Inputs: inputs, ProjectVersion: os.Getenv("APP_VERSION"), LegoVersion: "v" + strings.TrimPrefix(os.Getenv("LEGO_VERSION"), "v"), Toolchain: toolchain, ResolveModule: sources.Module, ResolveGo: sources.GoSource, ResolveData: sources.Data})
}
func VerifyLicenses(root string) error { return licenseaudit.Verify(root) }
