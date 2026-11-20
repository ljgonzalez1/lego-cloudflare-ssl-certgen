package licenseaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBundledAPKIdentityMustMatchInstalledMetadata(t *testing.T) {
	work := t.TempDir()
	root, installed := filepath.Join(work, "root"), filepath.Join(work, "installed")
	commit := strings.Repeat("a", 40)
	text := []byte(fixtureLicense)
	hash := digest(text)
	put(t, filepath.Join(work, "texts", hash+".txt"), fixtureLicense)
	put(t, filepath.Join(root, "etc/ssl/certs/ca-certificates.crt"), "CA fixture")
	c := Component{ID: "data:ca-certificates", Kind: "runtime-data", Name: "ca-certificates", Version: "20261020-r0", DeclaredLicense: "MPL-2.0 AND MIT", Sum: "aports-commit:" + commit,
		Origin: "https://raw.githubusercontent.com/alpinelinux/aports/" + commit + "/main/ca-certificates/APKBUILD", Documents: []Document{{Source: "LICENSE", SHA256: hash}}}
	b := Bundle{directory: work, byID: map[string]Component{c.ID: c}, Files: []File{{Path: "etc/ssl/certs/ca-certificates.crt", Component: c.ID, SHA256: digest([]byte("CA fixture"))}}}
	for _, mutation := range []string{"", "version", "license", "commit", "origin"} {
		version, license, revision, origin := c.Version, c.DeclaredLicense, commit, "ca-certificates"
		switch mutation {
		case "version":
			version = "20261020-r1"
		case "license":
			license = "MIT"
		case "commit":
			revision = strings.Repeat("b", 40)
		case "origin":
			origin = "different-package"
		}
		put(t, filepath.Join(installed, "lib/apk/db/installed"), fmt.Sprintf("P:ca-certificates-bundle\nV:%s\nL:%s\nc:%s\no:%s\n\n", version, license, revision, origin))
		_, err := b.Data(root, installed, "ca-certificates")
		if (err == nil) != (mutation == "") {
			t.Fatalf("metadata mutation %q: %v", mutation, err)
		}
	}
}

func TestFrozenBundleRoundTripAndCorruption(t *testing.T) {
	options, work := fixtureOptions(t)
	resolve := options.ResolveModule
	options.ResolveModule = func(module, version, sum string) (Source, error) {
		source, err := resolve(module, version, sum)
		if source.Sum == "" {
			source.Sum = "h1:" + strings.Repeat("A", 43) + "=" // Offline fixture only.
		}
		return source, err
	}
	if err := Collect(options); err != nil {
		t.Fatal(err)
	}
	before := manifestAt(t, options.Root)
	destination := filepath.Join(work, "bundle")
	if err := ExportBundle(options.Root, destination); err != nil {
		t.Fatal(err)
	}
	bundle, err := OpenBundle(destination)
	if err != nil {
		t.Fatal(err)
	}
	options.ResolveModule, options.ResolveGo = bundle.Module, bundle.GoSource
	options.ResolveData = func(kind string) (Source, error) { return bundle.Data(options.Root, work, kind) }
	if err := Collect(options); err != nil {
		t.Fatal(err)
	}
	after := manifestAt(t, options.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("frozen notice reuse changed the runtime inventory")
	}
	if _, err := bundle.Module("example.test/dependency", "v1.0.0", "h1:wrong"); err == nil {
		t.Fatal("wrong executable module checksum accepted")
	}
	if _, err := bundle.Module("example.test/new", "v1.0.0", ""); err == nil {
		t.Fatal("unknown module accepted without new source evidence")
	}
	put(t, filepath.Join(options.Root, "etc/ssl/certs/ca-certificates.crt"), "changed CA payload")
	if _, err := options.ResolveData("ca-certificates"); err == nil {
		t.Fatal("changed runtime data accepted with stale notices")
	}
	first := bundle.Components[0].Documents[0]
	put(t, filepath.Join(destination, "texts", first.SHA256+".txt"), "mutated notice")
	if _, err := OpenBundle(destination); err == nil {
		t.Fatal("changed original notice accepted")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "bundle.json" && entry.Name() != "texts" {
			t.Fatalf("non-notice content was exported: %s", entry.Name())
		}
	}
}
