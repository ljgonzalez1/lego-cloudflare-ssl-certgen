package licenseaudit

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const fixtureLicense = "Copyright fixture contributors.\nPermission is hereby granted, free of charge, to use this test fixture.\n"

func put(t *testing.T, p, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
}
func goCompiler(t *testing.T) string {
	t.Helper()
	root, err := ToolchainRoot()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "bin/go")
}
func buildFixture(t *testing.T, directory, module, output string, dependency bool) {
	t.Helper()
	body := "package main\nfunc main(){}\n"
	mod := "module " + module + "\n\ngo 1.23.0\n"
	if dependency {
		mod += "require example.test/dependency v1.0.0\n"
		body = "package main\nimport (\"fmt\";\"example.test/dependency\")\nfunc main(){fmt.Println(dependency.Value())}\n"
		put(t, filepath.Join(directory, "vendor/modules.txt"), "# example.test/dependency v1.0.0\n## explicit; go 1.23.0\nexample.test/dependency\n")
		put(t, filepath.Join(directory, "vendor/example.test/dependency/a.go"), "package dependency\nfunc Value()int{return 42}\n")
	}
	put(t, filepath.Join(directory, "go.mod"), mod)
	put(t, filepath.Join(directory, "main.go"), body)
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(goCompiler(t), "build", "-buildvcs=false", "-mod=vendor", "-trimpath", "-ldflags=-s -w", "-o", output, ".")
	command.Dir = directory
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v %s", err, data)
	}
}
func fixtureOptions(t *testing.T) (Options, string) {
	t.Helper()
	work := t.TempDir()
	root := filepath.Join(work, "root")
	inputs := filepath.Join(work, "inputs")
	source := filepath.Join(work, "notices")
	put(t, filepath.Join(inputs, "project/LICENSE"), fixtureLicense)
	put(t, filepath.Join(inputs, "Apache-2.0.txt"), "Apache License\nVersion 2.0\nfixture full terms\n")
	put(t, filepath.Join(inputs, "MPL-2.0.txt"), "Mozilla Public License Version 2.0\nfixture full terms\n")
	put(t, filepath.Join(source, "LICENSE"), fixtureLicense)
	put(t, filepath.Join(source, "NOTICE"), "Copyright fixture authors\n")
	put(t, filepath.Join(source, "nested/COPYING"), fixtureLicense)
	put(t, filepath.Join(source, "some.go"), "// Copyright fixture authors\n// Licensed under the MIT license.\npackage sample\n")
	buildFixture(t, filepath.Join(work, "project"), Project, filepath.Join(root, "usr/local/bin/certgen"), true)
	buildFixture(t, filepath.Join(work, "lego"), Lego, filepath.Join(root, "usr/local/bin/lego"), false)
	put(t, filepath.Join(root, "etc/ssl/certs/ca-certificates.crt"), "CA fixture")
	put(t, filepath.Join(root, "usr/share/zoneinfo.zip"), "timezone fixture")
	options := Options{Root: root, Inputs: inputs, ProjectVersion: "6.0.1", LegoVersion: "v4.34.0",
		ResolveModule: func(name, version, sum string) (Source, error) {
			return Source{Directory: source, Origin: "offline fixture: " + name + "@" + version, Sum: sum}, nil
		},
		ResolveGo: func(version string) (Source, error) {
			return Source{Directory: source, Origin: "offline toolchain notice fixture: " + version}, nil
		},
		ResolveData: func(kind string) (Source, error) {
			return Source{Directory: source, Origin: "offline data notice fixture: " + kind, License: "MPL-2.0 AND MIT", Version: "fixture-1"}, nil
		}}
	return options, work
}
func manifestAt(t *testing.T, root string) Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, Location, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m Manifest
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestInventoryUsesActualGoMetadataAndDeduplicatesNotices(t *testing.T) {
	options, _ := fixtureOptions(t)
	if err := Collect(options); err != nil {
		t.Fatal(err)
	}
	m := manifestAt(t, options.Root)
	found := false
	documents := 0
	for _, c := range m.Components {
		documents += len(c.Documents)
		if c.Name == "example.test/dependency" && c.Version == "v1.0.0" {
			found = true
		}
	}
	if !found || len(m.Binaries) != 2 {
		t.Fatal("actual dependency metadata not inventoried")
	}
	blobs, err := os.ReadDir(filepath.Join(options.Root, Location, "texts"))
	if err != nil {
		t.Fatal(err)
	}
	if len(blobs) >= documents {
		t.Fatal("identical notice bodies were not deduplicated")
	}
	for _, p := range []string{"Go/LICENSE", "go-yaml/LICENSE"} {
		if p == "go-yaml/LICENSE" {
			continue
		}
		if _, err := os.ReadFile(filepath.Join(options.Root, Location, p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := Verify(options.Root); err != nil {
		t.Fatal(err)
	}
}
func TestMutationsAndWrongComponentMappingsFailVerification(t *testing.T) {
	options, _ := fixtureOptions(t)
	if err := Collect(options); err != nil {
		t.Fatal(err)
	}
	restore := func(p string, mutated []byte) {
		t.Helper()
		original, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(p, mutated, 0644); err != nil {
			t.Fatal(err)
		}
		if err = Verify(options.Root); err == nil {
			t.Fatal("corruption was accepted")
		}
		if err = os.WriteFile(p, original, 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := manifestAt(t, options.Root)
	doc := m.Components[0].Documents[0]
	target, _ := filepath.EvalSymlinks(filepath.Join(options.Root, doc.Path))
	restore(target, []byte("wrong notice"))
	restore(filepath.Join(options.Root, Location, "NOTICE"), []byte("misattributed index"))
	restore(filepath.Join(options.Root, "usr/local/bin/lego"), []byte("not the licensed executable"))
	for _, mutation := range []string{"component", "version", "go-version", "duplicate-file", "source-hash"} {
		m := manifestAt(t, options.Root)
		switch mutation {
		case "component":
			m.Components[0].Documents[0].Path = m.Components[1].Documents[0].Path
		case "version":
			for i := range m.Components {
				if m.Components[i].Name == "example.test/dependency" {
					m.Components[i].Version = "v99.0.0"
				}
			}
		case "go-version":
			m.Binaries[0].GoVersion = "go0.0.0"
		case "duplicate-file":
			m.Files = append(m.Files, m.Files[0])
		case "source-hash":
			m.Components[0].Documents[0].SHA256 = strings.Repeat("0", 64)
		}
		data, _ := json.Marshal(m)
		restore(filepath.Join(options.Root, Location, "manifest.json"), data)
	}
	put(t, filepath.Join(options.Root, "usr/local/bin/unmapped"), "extra runtime program")
	if err := Verify(options.Root); err == nil {
		t.Fatal("unmapped runtime program accepted")
	}
}
func TestMissingLicensesAndSymlinkEscapeFailClosed(t *testing.T) {
	options, work := fixtureOptions(t)
	source := filepath.Join(work, "empty-notices")
	put(t, filepath.Join(source, "AUTHORS"), "Copyright only, no grant or license terms\n")
	options.ResolveModule = func(string, string, string) (Source, error) {
		return Source{Directory: source, Origin: "unlicensed fixture"}, nil
	}
	if err := Collect(options); err == nil {
		t.Fatal("notice without license terms was accepted")
	}
	options, _ = fixtureOptions(t)
	if err := Collect(options); err != nil {
		t.Fatal(err)
	}
	m := manifestAt(t, options.Root)
	p := filepath.Join(options.Root, m.Components[0].Documents[0].Path)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(work, "outside"), p); err != nil {
		t.Fatal(err)
	}
	if err := Verify(options.Root); err == nil {
		t.Fatal("license symlink escape accepted")
	}
}

func TestOptionalProgramNoticesFollowRetainedBinaries(t *testing.T) {
	options, work := fixtureOptions(t)
	proxy := filepath.Join(work, "proxy", "example.test/optional/@v")
	put(t, filepath.Join(proxy, "v1.0.0.mod"), "module example.test/optional\n\ngo 1.23.0\n")
	put(t, filepath.Join(proxy, "v1.0.0.info"), `{"Version":"v1.0.0","Time":"2026-01-01T00:00:00Z"}`)
	put(t, filepath.Join(proxy, "list"), "v1.0.0\n")
	archiveFile, err := os.Create(filepath.Join(proxy, "v1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(archiveFile)
	for name, body := range map[string]string{"go.mod": "module example.test/optional\n\ngo 1.23.0\n", "main.go": "package main\nfunc main(){}\n", "LICENSE": fixtureLicense} {
		entry, err := archive.Create("example.test/optional@v1.0.0/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err = archiveFile.Close(); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(work, "optional-bin")
	command := exec.Command(goCompiler(t), "install", "-buildvcs=false", "-trimpath", "example.test/optional@v1.0.0")
	command.Dir = work
	// This fixture uses only a private file:// proxy, never upstream network/source authentication.
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=-modcacherw", "GOWORK=off", "GOTOOLCHAIN=local", "GOSUMDB=off", "GOPROXY=file://"+filepath.Join(work, "proxy"), "GOBIN="+bin, "GOPATH="+filepath.Join(work, "gopath"))
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("offline optional binary: %v %s", err, data)
	}
	target := filepath.Join(options.Root, "usr/local/bin/dlv")
	if err = os.Rename(filepath.Join(bin, "optional"), target); err != nil {
		t.Fatal(err)
	}
	if err = Collect(options); err != nil {
		t.Fatal(err)
	}
	first := manifestAt(t, options.Root)
	if len(first.Binaries) != 3 {
		t.Fatal("optional program was omitted")
	}
	if err = os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err = Verify(options.Root); err == nil {
		t.Fatal("removed executable was accepted without regenerating licenses")
	}
	if err = Collect(options); err != nil {
		t.Fatal(err)
	}
	second := manifestAt(t, options.Root)
	if len(second.Binaries) != 2 {
		t.Fatal("removed program retained")
	}
	for _, component := range second.Components {
		if component.Name == "example.test/optional" {
			t.Fatal("removed program kept stale notices")
		}
	}
}

type sourceTransport func(*http.Request) (*http.Response, error)

func (f sourceTransport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestDataSourcesBindPayloadPackageRecipeAndArchive(t *testing.T) {
	work := t.TempDir()
	root, installed := filepath.Join(work, "root"), filepath.Join(work, "installed")
	for _, directory := range []string{root, installed} {
		for name, body := range map[string]string{"etc/ssl/certs/ca-certificates.crt": "CA fixture", "usr/share/zoneinfo/Etc/UTC": "TZif fixture", "etc/alpine-release": "3.24.4", "etc/os-release": "ID=alpine\n"} {
			put(t, filepath.Join(directory, name), body)
		}
	}
	commit := strings.Repeat("a", 40)
	var database strings.Builder
	recipes := map[string]string{}
	for _, entry := range []struct{ name, version, origin, license string }{
		{"ca-certificates-bundle", "20261020-r0", "ca-certificates", "MPL-2.0 AND MIT"},
		{"tzdata", "2026e-r0", "tzdata", "Public-Domain"},
		{"alpine-release", "3.24.4-r0", "alpine-base", "MIT"},
	} {
		fmt.Fprintf(&database, "P:%s\nV:%s\no:%s\nL:%s\nc:%s\n\n", entry.name, entry.version, entry.origin, entry.license, commit)
		version, revision, _ := strings.Cut(entry.version, "-r")
		recipes[entry.origin] = fmt.Sprintf("# Maintainer: Alpine fixture contributor\npkgname=%s\npkgver=%s\npkgrel=%s\nlicense=\"%s\"\n", entry.origin, version, revision, entry.license)
	}
	put(t, filepath.Join(installed, "lib/apk/db/installed"), database.String())
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	archive := tar.NewWriter(gz)
	for name, body := range map[string]string{"upstream/LICENSE": fixtureLicense, "upstream/certdata.txt": "#\n# Copyright fixture Mozilla contributors\n# This Source Code Form is subject to the Mozilla Public License, v. 2.0.\n#\nBEGINDATA\n"} {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha512.Sum512(compressed.Bytes())
	recipe := fmt.Sprintf("source=\"https://source.example.test/source.tar.gz\"\nsha512sums=\"%x  source.tar.gz\"\n", sum)
	tampered, challenge := false, false
	githubChallenge := false
	invalidRecipe := ""
	client := &http.Client{Transport: sourceTransport(func(request *http.Request) (*http.Response, error) {
		var data []byte
		switch {
		case request.URL.Host == "raw.githubusercontent.com" && githubChallenge:
			data = []byte("<html>challenge, not source evidence</html>")
		case request.URL.Host == "gitlab.alpinelinux.org":
			// A bot challenge can be HTTP 200: reject it and try the pinned mirror.
			data = []byte("<html>challenge, not source evidence</html>")
		case strings.HasSuffix(request.URL.Path, "/APKBUILD"):
			origin := filepath.Base(filepath.Dir(request.URL.Path))
			data = []byte(recipes[origin])
			if origin != "alpine-base" {
				data = append(data, recipe...)
			}
			if invalidRecipe != "" {
				data = bytes.ReplaceAll(data, []byte(invalidRecipe), []byte("wrong"))
			}
		case strings.HasSuffix(request.URL.Path, "/COPYING"):
			// Real aports trees have no repository-root COPYING. A fictitious
			// fixture here hid the failure in every target image build.
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader("not found")), Header: http.Header{}}, nil
		case request.URL.String() == "https://source.example.test/source.tar.gz":
			data = compressed.Bytes()
			if challenge {
				data = []byte("<html>HTTP 200 source challenge</html>")
			}
			if tampered {
				data = append(append([]byte{}, data...), 'x')
			}
		case request.URL.String() == "https://distfiles.alpinelinux.org/distfiles/v3.24/source.tar.gz" && challenge && !tampered:
			data = compressed.Bytes()
		default:
			return nil, fmt.Errorf("unexpected source request %s", request.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Header: http.Header{}}, nil
	})}
	for _, kind := range []string{"ca-certificates", "tzdata", "alpine-release"} {
		sources := &NativeSources{Root: root, DataRoot: installed, Work: filepath.Join(work, kind), Client: client}
		source, err := sources.Data(kind)
		if err != nil {
			t.Fatal(kind, err)
		}
		if source.Sum != "aports-commit:"+commit || source.Version == "" {
			t.Fatal("package provenance omitted")
		}
		if !strings.HasPrefix(source.Origin, "https://raw.githubusercontent.com/") {
			t.Fatal("pinned raw mirror was not preferred")
		}
		collector := collector{options: Options{Inputs: "../../licenses"}, blobs: map[string][]byte{}, aliases: map[string]string{}}
		component := Component{ID: "data:" + kind, DeclaredLicense: source.License}
		if err := collector.collectTree(&component, source.Directory, false); err != nil {
			t.Fatal(err)
		}
		if kind == "ca-certificates" {
			found := false
			for _, doc := range component.Documents {
				found = found || bytes.Contains(collector.blobs[doc.SHA256], []byte("Copyright fixture Mozilla contributors"))
			}
			if !found {
				t.Fatal("Mozilla data header attribution omitted")
			}
		}
		if kind == "alpine-release" {
			found := false
			for _, doc := range component.Documents {
				found = found || doc.Source == "supplemental/MIT.txt"
			}
			if !found {
				t.Fatal("MIT-declared release metadata lacks supplemental terms")
			}
		}
	}
	githubChallenge = true
	fallback := &NativeSources{Root: root, DataRoot: installed, Work: filepath.Join(work, "recipe-fallback"), Client: client}
	fallbackSource, err := fallback.Data("ca-certificates")
	if err != nil || !strings.HasPrefix(fallbackSource.Origin, "https://git.alpinelinux.org/") {
		t.Fatal("invalid HTTP 200 recipes did not trigger pinned fallback", err)
	}
	githubChallenge = false
	challenge = true
	sources := &NativeSources{Root: root, DataRoot: installed, Work: filepath.Join(work, "mirror"), Client: client}
	source, err := sources.Data("ca-certificates")
	if err != nil {
		t.Fatal("checksum-identical distfiles fallback failed", err)
	}
	notice, err := os.ReadFile(filepath.Join(source.Directory, "NOTICE-source-provenance.txt"))
	if err != nil || !bytes.Contains(notice, []byte("Retrieved from: https://distfiles.alpinelinux.org/distfiles/v3.24/source.tar.gz")) {
		t.Fatal("actual source mirror omitted from provenance", err)
	}
	if err = os.Remove(filepath.Join(source.Directory, "0/upstream/LICENSE")); err != nil {
		t.Fatal(err)
	}
	collector := collector{options: Options{Inputs: "../../licenses"}, blobs: map[string][]byte{}, aliases: map[string]string{}}
	component := Component{ID: "data:ca-certificates", DeclaredLicense: source.License}
	if err = collector.collectTree(&component, source.Directory, false); err != nil {
		t.Fatal("Mozilla license header requires no fictitious root license", err)
	}
	put(t, filepath.Join(source.Directory, "0/upstream/certdata.txt"), "# Copyright fixture Mozilla contributors\nBEGINDATA\n")
	component.Documents = nil
	if err = collector.collectTree(&component, source.Directory, false); err == nil {
		t.Fatal("upstream source without license terms was accepted")
	}
	challenge = false
	for _, value := range []string{"pkgname=ca-certificates", "pkgver=20261020", "pkgrel=0", "MPL-2.0 AND MIT"} {
		invalidRecipe = value
		sources := &NativeSources{Root: root, DataRoot: installed, Work: filepath.Join(work, "invalid-recipe"), Client: client}
		if _, err := sources.Data("ca-certificates"); err == nil || !strings.Contains(err.Error(), "installed package ca-certificates-bundle 20261020-r0") {
			t.Fatal("mismatched recipe accepted or package diagnostic omitted", value, err)
		}
	}
	invalidRecipe = ""
	tampered = true
	sources = &NativeSources{Root: root, DataRoot: installed, Work: filepath.Join(work, "tampered-source"), Client: client}
	if _, err := sources.Data("ca-certificates"); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatal("changed upstream source archive accepted", err)
	}
	tampered = false
	put(t, filepath.Join(root, "etc/ssl/certs/ca-certificates.crt"), "changed CA fixture")
	sources = &NativeSources{Root: root, DataRoot: installed, Work: filepath.Join(work, "changed"), Client: client}
	if _, err := sources.Data("ca-certificates"); err == nil {
		t.Fatal("changed CA payload attributed to unmodified package")
	}
	put(t, filepath.Join(root, "usr/share/zoneinfo/Etc/UTC"), "different timezone")
	if _, err := sources.Data("tzdata"); err == nil {
		t.Fatal("changed timezone payload attributed to unmodified package")
	}
}

func TestAlpineReleaseSupplementDoesNotLicenseUnrelatedSources(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "NOTICE-APKBUILD.txt"), "# Maintainer: Alpine fixture contributor\nlicense=\"MIT\"\n")
	for _, entry := range []struct{ id, license string }{
		{"data:ca-certificates", "MIT"},
		{"data:tzdata", "MIT"},
		{"data:alpine-release", "unknown"},
	} {
		collector := collector{options: Options{Inputs: "../../licenses"}, blobs: map[string][]byte{}, aliases: map[string]string{}}
		component := Component{ID: entry.id, DeclaredLicense: entry.license}
		if err := collector.collectTree(&component, root, false); err == nil {
			t.Fatal("supplemental MIT terms incorrectly licensed unrelated source", entry)
		}
	}
}

func TestCAHeaderNoticeRetainsFullMPLTerms(t *testing.T) {
	options, work := fixtureOptions(t)
	resolve := options.ResolveData
	source := filepath.Join(work, "ca-source")
	put(t, filepath.Join(source, "NOTICE-APKBUILD.txt"), "# Maintainer: Alpine fixture contributor\nlicense=\"MPL-2.0 AND MIT\"\n")
	put(t, filepath.Join(source, "certdata.txt"), "# This Source Code Form is subject to the terms of the Mozilla Public\n# License, v. 2.0. If a copy of the MPL was not distributed with this\n# file, You can obtain one at http://mozilla.org/MPL/2.0/.\nBEGINDATA\n")
	options.ResolveData = func(kind string) (Source, error) {
		if kind == "ca-certificates" {
			return Source{Directory: source, Origin: "checksum-verified CA source fixture", License: "MPL-2.0 AND MIT", Version: "20261020-r0"}, nil
		}
		return resolve(kind)
	}
	if err := Collect(options); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, component := range manifestAt(t, options.Root).Components {
		if component.ID == "data:ca-certificates" {
			for _, doc := range component.Documents {
				found = found || doc.Source == "supplemental/MPL-2.0.txt"
			}
		}
	}
	if !found {
		t.Fatal("Mozilla header was retained without full MPL terms")
	}
	if err := Verify(options.Root); err != nil {
		t.Fatal(err)
	}
}

func TestRealApplicationIncludesVendoredYAMLAndFullApacheTerms(t *testing.T) {
	options, _ := fixtureOptions(t)
	compiler := goCompiler(t)
	command := exec.Command(compiler, "build", "-mod=vendor", "-buildvcs=false", "-trimpath", "-ldflags=-s -w -X main.applicationVersion=6.0.1", "-o", filepath.Join(options.Root, "usr/local/bin/certgen"), "./cmd/certgen")
	command.Dir = "../.."
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("real application build: %v %s", err, data)
	}
	var err error
	options.Inputs, err = filepath.Abs("../../licenses")
	if err != nil {
		t.Fatal(err)
	}
	options.ResolveGo = func(version string) (Source, error) {
		root, err := ToolchainRoot()
		return Source{Directory: root, Origin: "actual local Go SDK " + version}, err
	}
	// Use the actual vendored source without changing the test process cwd.
	options.VendorRoot = "../../vendor"
	if err = Collect(options); err != nil {
		t.Fatal(err)
	}
	m := manifestAt(t, options.Root)
	found := false
	for _, component := range m.Components {
		if component.Name != "gopkg.in/yaml.v3" {
			continue
		}
		if component.Version != "v3.0.1" {
			t.Fatal("wrong vendored YAML source version")
		}
		for _, doc := range component.Documents {
			if doc.Source == "supplemental/Apache-2.0.txt" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("full Apache terms for actual YAML dependency omitted")
	}
}
func TestModuleZIPAuthenticationRejectsTamperedCache(t *testing.T) {
	work := t.TempDir()
	archivePath := filepath.Join(work, "source.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	name := "example.test/dependency@v1.0.0/LICENSE"
	writer, _ := archive.Create(name)
	_, _ = writer.Write([]byte(fixtureLicense))
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	file.Close()
	content := sha256.Sum256([]byte(fixtureLicense))
	outer := sha256.Sum256([]byte(fmt.Sprintf("%x  %s\n", content, name)))
	expected := "h1:" + base64.StdEncoding.EncodeToString(outer[:])
	if err = authenticatedModuleSource(archivePath, expected, filepath.Join(work, "valid")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(work, "valid/LICENSE")); err != nil || string(data) != fixtureLicense {
		t.Fatal("module root was not stripped correctly")
	}
	if err = authenticatedModuleSource(archivePath, "h1:wrong", filepath.Join(work, "bad")); err == nil {
		t.Fatal("changed archive checksum was accepted")
	}
}
func TestAPKProvenanceAndChecksumRecipeParsing(t *testing.T) {
	data := []byte("P:ca-certificates-bundle\nV:20261020-r0\nL:MPL-2.0 AND MIT\no:ca-certificates\nc:" + strings.Repeat("a", 40) + "\n")
	fields, err := installedPackage(data, "ca-certificates-bundle")
	if err != nil {
		t.Fatal(err)
	}
	recipe := "pkgname=ca-certificates\npkgver=20261020\nsource=\"https://example.test/$pkgname-$pkgver.tar.gz\"\nsha512sums=\"" + strings.Repeat("a", 128) + " ca-certificates-20261020.tar.gz\"\n"
	archives, err := sourceArchives(recipe, fields)
	if err != nil || len(archives) != 1 || archives[0][0] != "ca-certificates-20261020.tar.gz" {
		t.Fatalf("archives=%v err=%v", archives, err)
	}
	if _, err = sourceArchives(strings.ReplaceAll(recipe, strings.Repeat("a", 128), "SKIP"), fields); err == nil {
		t.Fatal("unverified source archive accepted")
	}
	t.Setenv("GOSUMDB", "off")
	if _, err = publicGoEnvironment(); err == nil {
		t.Fatal("checksum authentication bypass accepted")
	}
}
func TestSourceExtractionRejectsTraversalAndAllowsDirectories(t *testing.T) {
	for _, name := range []string{"../outside", "valid/LICENSE"} {
		var buffer bytes.Buffer
		gzipWriter := gzip.NewWriter(&buffer)
		archive := tar.NewWriter(gzipWriter)
		_ = archive.WriteHeader(&tar.Header{Name: "valid/", Typeflag: tar.TypeDir, Mode: 0755})
		_ = archive.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(fixtureLicense))})
		_, _ = archive.Write([]byte(fixtureLicense))
		archive.Close()
		gzipWriter.Close()
		err := extractSource(buffer.Bytes(), "source.tar.gz", t.TempDir())
		if (err != nil) != (name == "../outside") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}
func TestNativeGoNoticeCollectionIncludesPatentsAndNestedLicenses(t *testing.T) {
	root, err := ToolchainRoot()
	if err != nil {
		t.Fatal(err)
	}
	c := &collector{options: Options{Inputs: "../../licenses"}, blobs: map[string][]byte{}, aliases: map[string]string{}}
	component := Component{ID: "fixture-native-go", Name: "Go", Version: runtime.Version()}
	if err = c.collectTree(&component, root, true); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, doc := range component.Documents {
		seen[doc.Source] = true
	}
	if !seen["LICENSE"] || !seen["PATENTS"] || !seen["src/vendor/golang.org/x/crypto/LICENSE"] {
		t.Fatal("Go or bundled third-party notices omitted")
	}
}

func TestGoTimezoneNoticesMatchActualSDKData(t *testing.T) {
	toolchain, err := ToolchainRoot()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	root := filepath.Join(work, "root")
	data, err := os.ReadFile(filepath.Join(toolchain, "lib/time/zoneinfo.zip"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "usr/share/zoneinfo.zip"), string(data))
	sources := NativeSources{Root: root, Toolchain: toolchain, Work: work}
	source, err := sources.Data("tzdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"LICENSE", "COPYING-Go-SDK", "NOTICE-update-source.txt"} {
		if _, err := os.Stat(filepath.Join(source.Directory, name)); err != nil {
			t.Fatal("missing timezone notice", name, err)
		}
	}
	original, _ := os.ReadFile(filepath.Join(toolchain, "LICENSE"))
	retained, _ := os.ReadFile(filepath.Join(source.Directory, "COPYING-Go-SDK"))
	if !bytes.Equal(original, retained) {
		t.Fatal("Go update-script terms changed")
	}
	put(t, filepath.Join(root, "usr/share/zoneinfo.zip"), "different zip")
	if _, err := sources.Data("tzdata"); err == nil {
		t.Fatal("different timezone ZIP attributed to native SDK")
	}
}

// A split OCI SDK module inherits its repository-root dual license; unknown
// modules must still fail closed when their source archive lacks license terms.
func TestSplitOCIModuleUsesApprovedApacheTerms(t *testing.T) {
	root := t.TempDir()
	inputs := filepath.Join(root, "inputs")
	put(t, filepath.Join(inputs, "Apache-2.0.txt"), "Apache License\nVersion 2.0\nPermission to use, copy\n")
	source := filepath.Join(root, "source")
	put(t, filepath.Join(source, "common.go"), "// Copyright (c) Oracle.\npackage common\n")
	c := &collector{
		options: Options{Inputs: inputs},
		blobs:   map[string][]byte{}, aliases: map[string]string{},
	}
	allowed := Component{ID: "module:github.com/nrdcg/oci-go-sdk/common/v1065@v1065.111.0"}
	if err := c.collectTree(&allowed, source, false); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range allowed.Documents {
		if d.Source == "inherited/Apache-2.0.txt" {
			found = true
		}
	}
	if !found || allowed.DeclaredLicense != "Apache-2.0 OR UPL-1.0" {
		t.Fatal("missing inherited OCI license evidence")
	}
	denied := Component{ID: "module:example.com/unknown@v1.0.0"}
	if err := c.collectTree(&denied, source, false); err == nil {
		t.Fatal("license-free unknown module was accepted")
	}
	unreviewed := Component{ID: "module:github.com/nrdcg/oci-go-sdk/common/v1065@v1065.112.0"}
	if err := c.collectTree(&unreviewed, source, false); err == nil {
		t.Fatal("unreviewed OCI version was accepted")
	}
}
