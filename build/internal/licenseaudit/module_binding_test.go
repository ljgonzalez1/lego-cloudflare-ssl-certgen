package licenseaudit

import (
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

func TestVendoredAndAuthenticatedYAMLKeepSeparateBindings(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "vendored-first", true: "authenticated-first"}[reverse], func(t *testing.T) {
			work := t.TempDir()
			vendor := filepath.Join(work, "vendor")
			upstream := filepath.Join(work, "upstream")
			put(t, filepath.Join(vendor, "gopkg.in/yaml.v3/LICENSE"), fixtureLicense)
			put(t, filepath.Join(vendor, "gopkg.in/yaml.v3/NOTICE"), "Copyright vendored YAML authors\n")
			put(t, filepath.Join(upstream, "LICENSE"), fixtureLicense)
			sum := "h1:" + strings.Repeat("A", 43) + "=" // Fixture, not an upstream checksum.
			options := Options{VendorRoot: vendor, ResolveModule: func(name, version, expected string) (Source, error) {
				return Source{Directory: upstream, Origin: "authenticated fixture", Sum: sum}, nil
			}}
			c := collector{options: options, components: map[string]Component{}, blobs: map[string][]byte{}, aliases: map[string]string{}}
			vendored := debug.Module{Path: "gopkg.in/yaml.v3", Version: "v3.0.1"}
			authenticated := vendored
			authenticated.Sum = sum
			order := []struct {
				binary string
				module debug.Module
			}{{"usr/local/bin/certgen", vendored}, {"usr/local/bin/lego", authenticated}}
			if reverse {
				order[0], order[1] = order[1], order[0]
			}
			for _, input := range order {
				record, err := c.module(input.module, input.binary, false)
				if err != nil {
					t.Fatal(err)
				}
				if err := sameModule(record, rawModule(input.module), c.components, false, input.binary); err != nil {
					t.Fatal(err)
				}
			}
			if len(c.components) != 2 {
				t.Fatal("vendored source was conflated with the complete authenticated module")
			}
			id := "module:gopkg.in/yaml.v3@v3.0.1"
			if c.components[id+":vendored"].Sum != "" || c.components[id].Sum != sum {
				t.Fatal("a checksum was lost or incorrectly assigned to the vendored subtree")
			}
			record := rawModule(authenticated)
			record.Component = id
			wrong := c.components[id]
			wrong.Sum = "h1:wrong"
			c.components[id] = wrong
			err := sameModule(record, rawModule(authenticated), c.components, false, "usr/local/bin/lego")
			if err == nil || !strings.Contains(err.Error(), "binary=usr/local/bin/lego") || !strings.Contains(err.Error(), "gopkg.in/yaml.v3@v3.0.1") {
				t.Fatalf("checksum mismatch must fail with actionable metadata: %v", err)
			}
		})
	}
}

func TestConflictingComponentSourceCannotReuseAnEarlierIdentity(t *testing.T) {
	directory := t.TempDir()
	put(t, filepath.Join(directory, "LICENSE"), fixtureLicense)
	c := collector{components: map[string]Component{}, blobs: map[string][]byte{}, aliases: map[string]string{}}
	source := Source{Directory: directory, Origin: "fixture", Sum: "h1:one"}
	id := "module:example.test/module@v1.0.0"
	if _, err := c.component(id, "go-module", "example.test/module", "v1.0.0", source, false); err != nil {
		t.Fatal(err)
	}
	source.Sum = "h1:two"
	if _, err := c.component(id, "go-module", "example.test/module", "v1.0.0", source, false); err == nil {
		t.Fatal("a conflicting source checksum was silently deduplicated")
	}
}
