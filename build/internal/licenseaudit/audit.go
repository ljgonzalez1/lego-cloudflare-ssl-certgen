// Package licenseaudit is a native build-time inventory and notice verifier.
// It is not imported by the production application or shipped as a runtime tool.
package licenseaudit

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
)

const Location = "usr/share/licenses"
const Project = "github.com/ljgonzalez1/lego-cloudflare-ssl-certgen"
const Lego = "github.com/go-acme/lego/v4"
const maxDocument = 16 << 20

type Document struct {
	Source string `json:"source"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type Component struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind"`
	Name            string     `json:"name"`
	Version         string     `json:"version"`
	Origin          string     `json:"origin"`
	Sum             string     `json:"source_sum,omitempty"`
	DeclaredLicense string     `json:"declared_license,omitempty"`
	Documents       []Document `json:"documents"`
}
type Module struct {
	Path            string `json:"path"`
	Version         string `json:"version"`
	Sum             string `json:"sum,omitempty"`
	ResolvedPath    string `json:"resolved_path"`
	ResolvedVersion string `json:"resolved_version"`
	ResolvedSum     string `json:"resolved_sum,omitempty"`
	Component       string `json:"component"`
}
type Binary struct {
	Path        string   `json:"path"`
	SHA256      string   `json:"sha256"`
	GoVersion   string   `json:"go_version"`
	Main        Module   `json:"main"`
	Deps        []Module `json:"dependencies"`
	GoComponent string   `json:"go_component"`
}
type File struct {
	Path      string `json:"path"`
	SHA256    string `json:"sha256,omitempty"`
	Link      string `json:"link,omitempty"`
	Component string `json:"component"`
}
type Manifest struct {
	Schema     int               `json:"schema"`
	Components []Component       `json:"components"`
	Binaries   []Binary          `json:"binaries"`
	Files      []File            `json:"files"`
	Aliases    map[string]string `json:"notice_aliases"`
}
type Source struct{ Directory, Origin, Sum, License, Version string }
type Options struct {
	Root, Inputs, ProjectVersion, LegoVersion, Toolchain, VendorRoot string

	Progress func(string, ...any)

	// Source resolution is replaceable in offline tests, not through an environment bypass.
	ResolveModule func(string, string, string) (Source, error)
	ResolveGo     func(string) (Source, error)
	ResolveData   func(string) (Source, error)
}
type collector struct {
	options    Options
	components map[string]Component
	blobs      map[string][]byte
	aliases    map[string]string
}

func digest(data []byte) string { value := sha256.Sum256(data); return hex.EncodeToString(value[:]) }
func safeRelative(p string) bool {
	return p != "" && !strings.Contains(p, "\\") && !strings.HasPrefix(p, "/") && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}
func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (c *collector) addDocument(component *Component, source string, data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || len(data) > maxDocument {
		return fmt.Errorf("empty or oversized notice: %s", source)
	}
	if !safeRelative(source) {
		return fmt.Errorf("unsafe notice name: %q", source)
	}
	hash := digest(data)
	c.blobs[hash] = data
	alias := path.Join("components", digest([]byte(component.ID)), source)
	c.aliases[alias] = path.Join("texts", hash+".txt")
	component.Documents = append(component.Documents, Document{source, path.Join(Location, alias), hash})
	return nil
}

var noticeFilename = regexp.MustCompile(`(?i)^(?:(?:un)?licen[cs]e(?:[._-].*)?|copying(?:[._-].*)?|copyright(?:[._-].*)?|notice(?:[._-].*)?|patents(?:[._-].*)?|authors|third[._-]?party[._-]?notices?(?:[._-].*)?|.*[._-]license(?:[._-].*)?)$`)

func licenseName(name string) bool { return noticeFilename.MatchString(name) }
func noticeText(data []byte) string {
	lines := strings.Split(strings.ToLower(string(data)), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimLeft(line, " \t#/*")
	}
	return strings.Join(strings.Fields(strings.Join(lines, " ")), " ")
}
func terms(data []byte) bool {
	n := noticeText(data)
	return strings.Contains(n, "permission is hereby granted") || strings.Contains(n, "permission to use, copy") || strings.Contains(n, "redistribution and use") || strings.Contains(n, "apache license") || strings.Contains(n, "mozilla public license") || strings.Contains(n, "general public license") || strings.Contains(n, "public domain") || strings.Contains(n, "open font license") || strings.Contains(n, "creative commons") || strings.Contains(n, "boost software license") || strings.Contains(n, "do what the fuck you want")
}
func evidence(data []byte) bool {
	n := strings.ToLower(string(data))
	return strings.Contains(n, "copyright") || strings.Contains(n, "licensed under") || strings.Contains(n, "permission is hereby") || strings.Contains(n, "redistribution and use") || strings.Contains(n, "public domain") || strings.Contains(n, "spdx-license-identifier") || terms(data)
}

// Preserve leading source attributions as well as explicitly named notice files.
// All module notices are collected conservatively; no guessed SPDX expression is assigned.
func header(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	inBlock := false
	for _, line := range strings.SplitAfter(string(data), "\n") {
		t := strings.TrimSpace(line)
		if inBlock {
			out.WriteString(line)
			if strings.Contains(t, "*/") {
				inBlock = false
			}
			continue
		}
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
			out.WriteString(line)
			continue
		}
		if strings.HasPrefix(t, "/*") {
			out.WriteString(line)
			inBlock = !strings.Contains(t, "*/")
			continue
		}
		break
	}
	if inBlock {
		return nil, fmt.Errorf("notice header exceeds scan bound: %s", p)
	}
	result := []byte(out.String())
	if !evidence(result) {
		return nil, nil
	}
	return result, nil
}
func (c *collector) collectTree(component *Component, root string, goSDK bool) error {
	named := 0
	termsFound := false
	headers := map[string]struct {
		name string
		data []byte
	}{}
	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "testdata" || (goSDK && (rel == "src/cmd" || rel == "test" || rel == "pkg" || rel == "bin" || rel == "doc" || rel == "api")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			if licenseName(entry.Name()) {
				return fmt.Errorf("irregular notice: %s", p)
			}
			return nil
		}
		if licenseName(entry.Name()) || strings.Contains(strings.ToLower("/"+rel), "/licenses/") {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if err = c.addDocument(component, rel, data); err != nil {
				return err
			}
			named++
			termsFound = termsFound || terms(data)
			return nil
		}
		switch strings.ToLower(filepath.Ext(p)) {
		case ".go", ".s", ".c", ".h", ".py", ".sh", ".rs", ".txt":
			data, err := header(p)
			if err != nil {
				return err
			}
			if len(data) > 0 {
				hash := digest(data)
				if _, ok := headers[hash]; !ok {
					headers[hash] = struct {
						name string
						data []byte
					}{rel, data}
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(headers) > 0 {
		keys := make([]string, 0, len(headers))
		for hash := range headers {
			keys = append(keys, hash)
		}
		sort.Strings(keys)
		var notices bytes.Buffer
		for _, hash := range keys {
			h := headers[hash]
			fmt.Fprintf(&notices, "--- %s ---\n", h.name)
			notices.Write(h.data)
			notices.WriteByte('\n')
		}
		if err = c.addDocument(component, "SOURCE-ATTRIBUTIONS.txt", notices.Bytes()); err != nil {
			return err
		}
		// Mozilla certdata carries its MPL notice in the data header. Collect
		// adds the full MPL terms for this source-identified CA component.
		if component.ID == "data:ca-certificates" && strings.Contains(component.DeclaredLicense, "MPL-2.0") && strings.Contains(noticeText(notices.Bytes()), "mozilla public license") {
			termsFound = true
		}
	}
	// Oracle OCI SDK split modules omit the repository-root LICENSE.txt.
	// The upstream dual license permits choosing Apache-2.0; keep its complete
	// terms and the module's source-header attributions in the final inventory.
	if named == 0 && strings.HasPrefix(component.ID, "module:github.com/nrdcg/oci-go-sdk/") && strings.HasSuffix(component.ID, "@v1065.111.0") {
		data, err := os.ReadFile(filepath.Join(c.options.Inputs, "Apache-2.0.txt"))
		if err != nil {
			return err
		}
		if err := c.addDocument(component, "inherited/Apache-2.0.txt", data); err != nil {
			return err
		}
		component.DeclaredLicense = "Apache-2.0 OR UPL-1.0"
		named++
		termsFound = terms(data)
	}
	// Alpine release files are generated by alpine-base's MIT-declared recipe,
	// which is retained with its original attributions and has no source archive.
	if component.ID == "data:alpine-release" && component.DeclaredLicense == "MIT" && !termsFound {
		data, err := os.ReadFile(filepath.Join(c.options.Inputs, "MIT.txt"))
		if err != nil {
			return err
		}
		if err = c.addDocument(component, "supplemental/MIT.txt", data); err != nil {
			return err
		}
		termsFound = terms(data)
	}
	if named == 0 || !termsFound {
		return fmt.Errorf("no license/notice files for %s", component.ID)
	}
	// Upstream notices sometimes refer to Apache 2.0 without providing the full terms.
	apache := false
	for _, d := range component.Documents {
		if bytes.Contains(c.blobs[d.SHA256], []byte("Apache")) {
			apache = true
		}
	}
	if apache {
		data, err := os.ReadFile(filepath.Join(c.options.Inputs, "Apache-2.0.txt"))
		if err != nil {
			return err
		}
		if err = c.addDocument(component, "supplemental/Apache-2.0.txt", data); err != nil {
			return err
		}
	}
	sort.Slice(component.Documents, func(i, j int) bool { return component.Documents[i].Source < component.Documents[j].Source })
	return nil
}
func (c *collector) component(id, kind, name, version string, source Source, sdk bool) (string, error) {
	if _, ok := c.components[id]; ok {
		return id, nil
	}
	if kind == "runtime-data" && source.Version != "" {
		version = source.Version
	}
	component := Component{ID: id, Kind: kind, Name: name, Version: version, Origin: source.Origin, Sum: source.Sum, DeclaredLicense: source.License}
	logProgress(c.options.Progress, "scan notices: %s", id)
	if err := c.collectTree(&component, source.Directory, sdk); err != nil {
		return "", err
	}
	logProgress(c.options.Progress, "notices ready: %s (%d documents)", id, len(component.Documents))
	c.components[id] = component
	return id, nil
}
func rawModule(m debug.Module) Module {
	actual := m
	if m.Replace != nil {
		actual = *m.Replace
	}
	return Module{Path: m.Path, Version: m.Version, Sum: m.Sum, ResolvedPath: actual.Path, ResolvedVersion: actual.Version, ResolvedSum: actual.Sum}
}
func (c *collector) module(m debug.Module, binary string, main bool) (Module, error) {
	record := rawModule(m)
	var source Source
	var err error
	version := record.ResolvedVersion
	switch {
	case main && binary == "usr/local/bin/certgen" && record.ResolvedPath == Project:
		version = c.options.ProjectVersion
		source = Source{Directory: filepath.Join(c.options.Inputs, "project"), Origin: "https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen/releases/tag/v" + version, License: "MIT"}
	case main && binary == "usr/local/bin/lego" && record.ResolvedPath == Lego && (version == "" || version == "(devel)"):
		version = c.options.LegoVersion
		source, err = c.options.ResolveModule(record.ResolvedPath, version, record.ResolvedSum)
	case record.ResolvedPath == "gopkg.in/yaml.v3" && version == "v3.0.1" && binary == "usr/local/bin/certgen":
		vendor := c.options.VendorRoot
		if vendor == "" {
			vendor = "vendor"
		}
		source = Source{Directory: filepath.Join(vendor, "gopkg.in/yaml.v3"), Origin: "vendored gopkg.in/yaml.v3@v3.0.1", License: "MIT AND Apache-2.0"}
	default:
		if version == "" || version == "(devel)" {
			return record, fmt.Errorf("unversioned/local module needs explicit source mapping: %s", record.ResolvedPath)
		}
		source, err = c.options.ResolveModule(record.ResolvedPath, version, record.ResolvedSum)
	}
	if err != nil {
		return record, err
	}
	id := "module:" + record.ResolvedPath + "@" + version
	if _, err = c.component(id, "go-module", record.ResolvedPath, version, source, false); err != nil {
		return record, err
	}
	record.Component = id
	return record, nil
}
func discover(root string) ([]string, error) {
	var binaries []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == Location || strings.HasPrefix(rel, Location+"/") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		var magic [4]byte
		_, readErr := io.ReadFull(f, magic[:])
		f.Close()
		if readErr == nil && string(magic[:]) == "\x7fELF" {
			binaries = append(binaries, rel)
		}
		return nil
	})
	sort.Strings(binaries)
	return binaries, err
}
func Collect(options Options) error {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return err
	}
	options.Root = root
	if options.ProjectVersion == "" || options.LegoVersion == "" {
		return fmt.Errorf("APP_VERSION and LEGO_VERSION are required for source attribution")
	}
	if options.ResolveModule == nil || options.ResolveGo == nil || options.ResolveData == nil {
		return fmt.Errorf("source resolvers are required")
	}
	c := &collector{options: options, components: map[string]Component{}, blobs: map[string][]byte{}, aliases: map[string]string{}}
	manifest := Manifest{Schema: 1}
	logProgress(options.Progress, "discover retained ELF binaries")
	binaries, err := discover(root)
	if err != nil {
		return err
	}
	for index, p := range binaries {
		info, err := buildinfo.ReadFile(filepath.Join(root, p))
		if err != nil {
			return fmt.Errorf("unmapped non-Go executable %s: %w", p, err)
		}
		logProgress(options.Progress, "binary %d/%d: %s; Go %s; %d dependencies", index+1, len(binaries), p, info.GoVersion, len(info.Deps))
		hash, err := hashFile(filepath.Join(root, p))
		if err != nil {
			return err
		}
		binary := Binary{Path: p, SHA256: hash, GoVersion: info.GoVersion}
		if binary.Main, err = c.module(info.Main, p, true); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		for dependency, dep := range info.Deps {
			logProgress(options.Progress, "%s dependency %d/%d: %s@%s", p, dependency+1, len(info.Deps), dep.Path, dep.Version)
			m, err := c.module(*dep, p, false)
			if err != nil {
				return err
			}
			binary.Deps = append(binary.Deps, m)
		}
		goSource, err := options.ResolveGo(info.GoVersion)
		if err != nil {
			return err
		}
		binary.GoComponent, err = c.component("toolchain:"+info.GoVersion, "go-toolchain", "Go", info.GoVersion, goSource, true)
		if err != nil {
			return err
		}
		manifest.Binaries = append(manifest.Binaries, binary)
	}
	if len(manifest.Binaries) == 0 {
		return fmt.Errorf("runtime has no Go executables")
	}
	dataIDs := map[string]string{}
	kinds := []string{"ca-certificates", "tzdata"}
	if _, err := os.Stat(filepath.Join(root, "etc/alpine-release")); err == nil {
		kinds = append(kinds, "alpine-release")
	}
	for _, kind := range kinds {
		logProgress(options.Progress, "resolve runtime data: %s", kind)
		target := "etc/ssl/certs/ca-certificates.crt"
		if kind == "alpine-release" {
			target = "etc/alpine-release"
		}
		if kind == "tzdata" {
			target = "usr/share/zoneinfo"
			if _, err := os.Stat(filepath.Join(root, "usr/share/zoneinfo.zip")); err == nil {
				target = "usr/share/zoneinfo.zip"
			}
		}
		if _, err := os.Stat(filepath.Join(root, target)); err != nil {
			return fmt.Errorf("missing runtime data %s", target)
		}
		source, err := options.ResolveData(kind)
		if err != nil {
			return err
		}
		id, err := c.component("data:"+kind, "runtime-data", kind, "source-identified", source, false)
		if err != nil {
			return err
		}
		if kind == "ca-certificates" && strings.Contains(source.License, "MPL") {
			component := c.components[id]
			data, err := os.ReadFile(filepath.Join(options.Inputs, "MPL-2.0.txt"))
			if err != nil {
				return err
			}
			if err = c.addDocument(&component, "supplemental/MPL-2.0.txt", data); err != nil {
				return err
			}
			c.components[id] = component
		}
		dataIDs[kind] = id
	}
	// Full inventory binds notices to the exact assembled payload, including copied debug sources.
	firstParty := ""
	for _, b := range manifest.Binaries {
		if b.Path == "usr/local/bin/certgen" {
			firstParty = b.Main.Component
		}
	}
	if firstParty == "" {
		return fmt.Errorf("missing certgen runtime executable")
	}
	logProgress(options.Progress, "hash and attribute retained runtime files")
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if rel == Location || strings.HasPrefix(rel, Location+"/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		component := ""
		switch {
		case rel == "etc/passwd" || rel == "etc/group" || rel == "src/go.mod" || rel == "src/vendor/modules.txt" || rel == "usr/share/certgen/debugger.json" || strings.HasPrefix(rel, "src/cmd/") || strings.HasPrefix(rel, "src/internal/") || strings.HasPrefix(rel, "src/spinner/"):
			component = firstParty
		case strings.HasPrefix(rel, "usr/share/zoneinfo"):
			component = dataIDs["tzdata"]
		case rel == "etc/ssl/certs/ca-certificates.crt":
			component = dataIDs["ca-certificates"]
		case rel == "etc/alpine-release" || rel == "etc/os-release":
			component = dataIDs["alpine-release"]
		}
		best := 0
		for _, b := range manifest.Binaries {
			if b.Path == "usr/local/bin/certgen" {
				for _, m := range b.Deps {
					prefix := "src/vendor/" + m.Path + "/"
					if strings.HasPrefix(rel, prefix) && len(prefix) > best {
						component = m.Component
						best = len(prefix)
					}
				}
			}
		}
		for _, b := range manifest.Binaries {
			if b.Path == rel {
				component = b.Main.Component
			}
		}
		if component == "" {
			return fmt.Errorf("runtime file needs an explicit license mapping: %s", rel)
		}
		file := File{Path: rel, Component: component}
		if d.Type()&os.ModeSymlink != 0 {
			file.Link, err = os.Readlink(p)
		} else if d.Type().IsRegular() {
			file.SHA256, err = hashFile(p)
		} else {
			return fmt.Errorf("unsupported runtime file: %s", rel)
		}
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, file)
		return nil
	})
	if err != nil {
		return err
	}
	for _, v := range c.components {
		manifest.Components = append(manifest.Components, v)
	}
	sort.Slice(manifest.Components, func(i, j int) bool { return manifest.Components[i].ID < manifest.Components[j].ID })
	// Generate a fresh notice tree. Stale debugger/module notices cannot survive a removed executable.
	licenseRoot := filepath.Join(root, Location)
	logProgress(options.Progress, "write inventory: %d files, %d components, %d unique notice texts", len(manifest.Files), len(manifest.Components), len(c.blobs))
	if st, err := os.Lstat(licenseRoot); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("license destination is a symlink")
	}
	if err = os.RemoveAll(licenseRoot); err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Join(licenseRoot, "texts"), 0755); err != nil {
		return err
	}
	for hash, data := range c.blobs {
		if err = os.WriteFile(filepath.Join(licenseRoot, "texts", hash+".txt"), data, 0644); err != nil {
			return err
		}
	}
	for _, b := range manifest.Binaries {
		aliases := map[string]string{}
		if b.Path == "usr/local/bin/certgen" {
			aliases["certgen/LICENSE"] = b.Main.Component
			aliases["Go/LICENSE"] = b.GoComponent
			for _, m := range b.Deps {
				if m.Path == "gopkg.in/yaml.v3" {
					aliases["go-yaml/LICENSE"] = m.Component
					aliases["go-yaml/NOTICE"] = m.Component
				}
			}
		}
		if b.Path == "usr/local/bin/lego" {
			aliases["lego/LICENSE"] = b.Main.Component
		}
		for alias, id := range aliases {
			want := "LICENSE"
			if strings.HasSuffix(alias, "/NOTICE") {
				want = "NOTICE"
			}
			found := false
			for _, d := range c.components[id].Documents {
				if d.Source == want {
					c.aliases[alias] = path.Join("texts", d.SHA256+".txt")
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("missing compatibility notice %s", alias)
			}
		}
	}
	// The full MPL text is required even when the upstream package only references it.
	if id := dataIDs["ca-certificates"]; id != "" && strings.Contains(c.components[id].DeclaredLicense, "MPL") {
		data, err := os.ReadFile(filepath.Join(options.Inputs, "MPL-2.0.txt"))
		if err != nil {
			return err
		}
		hash := digest(data)
		c.blobs[hash] = data
		c.aliases["ca-certificates/MPL-2.0"] = path.Join("texts", hash+".txt")
		if err = os.WriteFile(filepath.Join(licenseRoot, "texts", hash+".txt"), data, 0644); err != nil {
			return err
		}
	}
	for alias, target := range c.aliases {
		destination := filepath.Join(licenseRoot, filepath.FromSlash(alias))
		if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		relative, err := filepath.Rel(filepath.Dir(destination), filepath.Join(licenseRoot, filepath.FromSlash(target)))
		if err != nil {
			return err
		}
		if err = os.Symlink(relative, destination); err != nil {
			return err
		}
	}
	// Readable component mapping; original documents remain verbatim in the shared text store.
	notice := renderNotice(manifest.Components)
	if err = os.WriteFile(filepath.Join(licenseRoot, "NOTICE"), []byte(notice), 0644); err != nil {
		return err
	}
	manifest.Aliases = c.aliases
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if err = os.WriteFile(filepath.Join(licenseRoot, "manifest.json"), encoded, 0644); err != nil {
		return err
	}
	logProgress(options.Progress, "verify generated inventory against retained payload")
	if err = Verify(root); err != nil {
		return err
	}
	logProgress(options.Progress, "inventory verified successfully")
	return nil
}

func renderNotice(components []Component) string {
	var notice strings.Builder
	notice.WriteString("Runtime license evidence inventory. See manifest.json for exact paths, versions and SHA-256.\nLicense evidence is not an automated legal compatibility opinion.\n\n")
	for _, component := range components {
		fmt.Fprintf(&notice, "%s\n  source: %s\n", component.ID, component.Origin)
		for _, d := range component.Documents {
			fmt.Fprintf(&notice, "  %s -> /%s\n", d.Source, d.Path)
		}
	}
	return notice.String()
}
