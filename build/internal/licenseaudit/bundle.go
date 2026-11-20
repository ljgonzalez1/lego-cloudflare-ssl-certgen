package licenseaudit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Bundle contains notices and provenance, never the executables or downloaded
// source archives. Data fingerprints bind those notices to the retained payload.
type Bundle struct {
	Schema     int         `json:"schema"`
	Components []Component `json:"components"`
	Files      []File      `json:"data_files"`

	directory string
	byID      map[string]Component
}

var sha256Text = regexp.MustCompile(`^[a-f0-9]{64}$`)

func OpenBundle(directory string) (*Bundle, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	p, err := boundFile(root, "bundle.json")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(io.LimitReader(f, 64<<20))
	decoder.DisallowUnknownFields()
	b := &Bundle{directory: root, byID: map[string]Component{}}
	if err = decoder.Decode(b); err != nil {
		return nil, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("invalid trailing bundle data")
	}
	if b.Schema != 1 || len(b.Components) == 0 || len(b.Files) == 0 {
		return nil, fmt.Errorf("incomplete local license bundle; run bash ci/update-license-bundle.sh")
	}
	for _, component := range b.Components {
		if component.ID == "" || component.Origin == "" || component.Version == "" || len(component.Documents) == 0 {
			return nil, fmt.Errorf("incomplete bundled component %q", component.ID)
		}
		if _, exists := b.byID[component.ID]; exists {
			return nil, fmt.Errorf("duplicate bundled component %s", component.ID)
		}
		switch component.Kind {
		case "go-module":
			if component.ID != "module:"+component.Name+"@"+component.Version || !strings.HasPrefix(component.Sum, "h1:") {
				return nil, fmt.Errorf("missing authenticated module identity: %s", component.ID)
			}
		case "go-toolchain":
			if component.ID != "toolchain:"+component.Version || component.Name != "Go" {
				return nil, fmt.Errorf("invalid bundled Go toolchain: %s", component.ID)
			}
		case "runtime-data":
			if component.ID != "data:"+component.Name || !dataPath(component.Name, dataTarget(component.Name)) {
				return nil, fmt.Errorf("invalid bundled runtime data: %s", component.ID)
			}
		default:
			return nil, fmt.Errorf("unsupported bundled component kind: %s", component.Kind)
		}
		seen := map[string]bool{}
		for _, doc := range component.Documents {
			if !safeRelative(doc.Source) || seen[doc.Source] || !sha256Text.MatchString(doc.SHA256) || doc.Path != filepath.ToSlash(filepath.Join(Location, "components", digest([]byte(component.ID)), doc.Source)) {
				return nil, fmt.Errorf("invalid bundled notice for %s: %s", component.ID, doc.Source)
			}
			seen[doc.Source] = true
			if _, err := b.document(doc); err != nil {
				return nil, fmt.Errorf("%s: %w", component.ID, err)
			}
		}
		b.byID[component.ID] = component
	}
	seen := map[string]bool{}
	for _, file := range b.Files {
		component, ok := b.byID[file.Component]
		if !ok || component.Kind != "runtime-data" || !safeRelative(file.Path) || !dataPath(component.Name, file.Path) || seen[file.Path] || ((file.Link == "") == (file.SHA256 == "")) || (file.SHA256 != "" && !sha256Text.MatchString(file.SHA256)) {
			return nil, fmt.Errorf("invalid bundled data fingerprint: %s", file.Path)
		}
		seen[file.Path] = true
	}
	return b, nil
}

func dataTarget(kind string) string {
	switch kind {
	case "ca-certificates":
		return "etc/ssl/certs/ca-certificates.crt"
	case "tzdata":
		return "usr/share/zoneinfo"
	case "alpine-release":
		return "etc/alpine-release"
	}
	return ""
}

func dataPath(kind, p string) bool {
	switch kind {
	case "ca-certificates":
		return p == dataTarget(kind)
	case "tzdata":
		return p == "usr/share/zoneinfo" || p == "usr/share/zoneinfo.zip" || strings.HasPrefix(p, "usr/share/zoneinfo/")
	case "alpine-release":
		return p == "etc/alpine-release" || p == "etc/os-release"
	}
	return false
}

func (b *Bundle) document(doc Document) ([]byte, error) {
	p, err := boundFile(b.directory, "texts/"+doc.SHA256+".txt")
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > maxDocument {
		return nil, fmt.Errorf("missing or oversized bundled notice: %s", doc.Source)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	if digest(data) != doc.SHA256 {
		return nil, fmt.Errorf("bundled notice checksum changed: %s", doc.Source)
	}
	return data, nil
}

func (b *Bundle) source(id string) (Source, error) {
	c, ok := b.byID[id]
	if !ok {
		return Source{}, fmt.Errorf("local license bundle has no %s; refresh with bash ci/update-license-bundle.sh", id)
	}
	source := Source{Origin: c.Origin, Sum: c.Sum, License: c.DeclaredLicense, Version: c.Version, Notices: map[string][]byte{}}
	for _, doc := range c.Documents {
		data, err := b.document(doc)
		if err != nil {
			return Source{}, err
		}
		source.Notices[doc.Source] = data
	}
	return source, nil
}

func (b *Bundle) Module(module, version, sum string) (Source, error) {
	source, err := b.source("module:" + module + "@" + version)
	if err == nil && sum != "" && sum != source.Sum {
		return Source{}, fmt.Errorf("bundled module checksum mismatch: %s@%s binary=%q bundle=%q", module, version, sum, source.Sum)
	}
	return source, err
}

func (b *Bundle) GoSource(version string) (Source, error) {
	return b.source("toolchain:" + version)
}

func (b *Bundle) CopyTo(destination string) error {
	if err := os.MkdirAll(filepath.Join(destination, "texts"), 0755); err != nil {
		return err
	}
	for _, component := range b.Components {
		for _, doc := range component.Documents {
			data, err := b.document(doc)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(destination, "texts", doc.SHA256+".txt"), data, 0644); err != nil {
				return err
			}
		}
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(destination, "bundle.json"), append(data, '\n'), 0644)
}

func (b *Bundle) Data(root, dataRoot, kind string) (Source, error) {
	source, err := b.source("data:" + kind)
	if err != nil {
		return Source{}, err
	}
	if strings.HasPrefix(source.Sum, "aports-commit:") {
		name := kind
		if kind == "ca-certificates" {
			name = "ca-certificates-bundle"
		}
		db, err := os.ReadFile(filepath.Join(dataRoot, "lib/apk/db/installed"))
		if err != nil {
			return Source{}, err
		}
		fields, err := installedPackage(db, name)
		if err != nil {
			return Source{}, err
		}
		origin := fields["o"]
		if origin == "" {
			origin = name
		}
		recipeURL, _, _ := strings.Cut(source.Origin, "?")
		if fields["V"] != source.Version || fields["L"] != source.License || "aports-commit:"+fields["c"] != source.Sum || !strings.HasSuffix(recipeURL, "/main/"+origin+"/APKBUILD") {
			return Source{}, fmt.Errorf("installed %s changed since license bundle preparation; refresh the bundle", name)
		}
	}
	expected := map[string]File{}
	for _, file := range b.Files {
		if file.Component == "data:"+kind {
			expected[file.Path] = file
		}
	}
	if len(expected) == 0 {
		return Source{}, fmt.Errorf("no bundled payload fingerprints for %s", kind)
	}
	for p, file := range expected {
		actual := filepath.Join(root, filepath.FromSlash(p))
		st, err := os.Lstat(actual)
		if err != nil {
			return Source{}, fmt.Errorf("bundled data missing: %s: %w", p, err)
		}
		if file.Link != "" {
			link, err := os.Readlink(actual)
			if err != nil || link != file.Link {
				return Source{}, fmt.Errorf("bundled data link changed: %s", p)
			}
		} else {
			hash, err := hashFile(actual)
			if !st.Mode().IsRegular() || err != nil || hash != file.SHA256 {
				return Source{}, fmt.Errorf("bundled data checksum changed: %s", p)
			}
		}
	}
	// Check both timezone representations, including unexpected newly added files.
	if kind == "tzdata" {
		err := filepath.WalkDir(root, func(p string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if dataPath(kind, relative) {
				if _, ok := expected[relative]; !ok {
					return fmt.Errorf("unmapped timezone payload: %s", relative)
				}
			}
			return nil
		})
		if err != nil {
			return Source{}, err
		}
	}
	return source, nil
}

// ExportBundle copies only original notice texts and their metadata. Project and
// vendored YAML notices are already in the build context and remain separate.
func ExportBundle(root, destination string) error {
	if err := Verify(root); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, Location, "manifest.json"))
	if err != nil {
		return err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return err
	}
	b := Bundle{Schema: 1}
	for _, c := range manifest.Components {
		if c.Name == Project || strings.HasSuffix(c.ID, ":vendored") {
			continue
		}
		b.Components = append(b.Components, c)
		for _, doc := range c.Documents {
			data, err := os.ReadFile(filepath.Join(root, Location, "texts", doc.SHA256+".txt"))
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Join(destination, "texts"), 0755); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(destination, "texts", doc.SHA256+".txt"), data, 0644); err != nil {
				return err
			}
		}
	}
	for _, file := range manifest.Files {
		if strings.HasPrefix(file.Component, "data:") {
			b.Files = append(b.Files, file)
		}
	}
	encoded, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(destination, "bundle.json"), append(encoded, '\n'), 0644); err != nil {
		return err
	}
	_, err = OpenBundle(destination)
	return err
}
