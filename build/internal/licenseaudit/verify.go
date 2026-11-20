package licenseaudit

import (
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
)

func boundFile(root, relative string) (string, error) {
	if !safeRelative(relative) {
		return "", fmt.Errorf("unsafe inventory path %q", relative)
	}
	p := filepath.Join(root, filepath.FromSlash(relative))
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, real)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("inventory path escapes root: %s", relative)
	}
	return p, nil
}
func sameModule(record Module, actual Module, components map[string]Component, main bool, binary string) error {
	component, ok := components[record.Component]
	if !ok {
		return fmt.Errorf("missing module component: %s", record.Component)
	}
	id := record.Component
	record.Component = ""
	if !reflect.DeepEqual(record, actual) {
		return fmt.Errorf("binary module metadata changed: %s", actual.Path)
	}
	if component.Kind != "go-module" || component.Name != record.ResolvedPath {
		return fmt.Errorf("incorrect module attribution: %s", id)
	}
	override := main && (actual.ResolvedVersion == "" || actual.ResolvedVersion == "(devel)") && ((actual.ResolvedPath == Project && binary == "usr/local/bin/certgen") || (actual.ResolvedPath == Lego && binary == "usr/local/bin/lego"))
	if record.ResolvedSum != "" && record.ResolvedSum != component.Sum {
		return fmt.Errorf("module source checksum binding changed")
	}
	if !override && component.Version != record.ResolvedVersion {
		return fmt.Errorf("wrong source version for %s", id)
	}
	return nil
}
func Verify(root string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	p, err := boundFile(root, path.Join(Location, "manifest.json"))
	if err != nil {
		return err
	}
	file, err := os.Open(p)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 64<<20))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err = decoder.Decode(&manifest); err != nil {
		return err
	}
	var extra interface{}
	if err = decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("invalid trailing manifest data")
	}
	if manifest.Schema != 1 || len(manifest.Components) == 0 || len(manifest.Binaries) == 0 || len(manifest.Files) == 0 {
		return fmt.Errorf("incomplete license manifest")
	}
	components := map[string]Component{}
	usedComponents := map[string]bool{}
	expectedNotice := map[string]bool{"manifest.json": true, "NOTICE": true}
	for _, component := range manifest.Components {
		if component.ID == "" || component.Origin == "" || len(component.Documents) == 0 {
			return fmt.Errorf("incomplete component %q", component.ID)
		}
		if _, ok := components[component.ID]; ok {
			return fmt.Errorf("duplicate component %s", component.ID)
		}
		components[component.ID] = component
		seen := map[string]bool{}
		for _, doc := range component.Documents {
			if seen[doc.Source] || !safeRelative(doc.Source) || doc.Path != path.Join(Location, "components", digest([]byte(component.ID)), doc.Source) {
				return fmt.Errorf("invalid component notice binding")
			}
			seen[doc.Source] = true
			alias := strings.TrimPrefix(doc.Path, Location+"/")
			target := path.Join("texts", doc.SHA256+".txt")
			if manifest.Aliases[alias] != target {
				return fmt.Errorf("notice belongs to a different component: %s", doc.Path)
			}
		}
	}
	noticePath, err := boundFile(root, path.Join(Location, "NOTICE"))
	if err != nil {
		return err
	}
	notice, err := os.ReadFile(noticePath)
	if err != nil {
		return err
	}
	if string(notice) != renderNotice(manifest.Components) {
		return fmt.Errorf("notice index differs from component inventory")
	}
	for alias, target := range manifest.Aliases {
		if !safeRelative(alias) || !safeRelative(target) || !strings.HasPrefix(target, "texts/") || !strings.HasSuffix(target, ".txt") {
			return fmt.Errorf("unsafe notice link")
		}
		full, err := boundFile(root, path.Join(Location, alias))
		if err != nil {
			return err
		}
		link, err := os.Readlink(full)
		if err != nil {
			return err
		}
		expected, err := filepath.Rel(filepath.Dir(full), filepath.Join(root, Location, filepath.FromSlash(target)))
		if err != nil || link != expected {
			return fmt.Errorf("changed notice symlink: %s", alias)
		}
		blob, err := boundFile(root, path.Join(Location, target))
		if err != nil {
			return err
		}
		info, err := os.Lstat(blob)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > maxDocument {
			return fmt.Errorf("invalid notice body: %s", target)
		}
		hash, err := hashFile(blob)
		if err != nil {
			return err
		}
		if target != path.Join("texts", hash+".txt") {
			return fmt.Errorf("notice checksum mismatch: %s", target)
		}
		expectedNotice[alias] = true
		expectedNotice[target] = true
	}
	// New/removed binaries must cause inventory regeneration, including optional Delve.
	actualBinaries, err := discover(root)
	if err != nil {
		return err
	}
	expectedBinaries := map[string]bool{}
	for _, binary := range manifest.Binaries {
		usedComponents[binary.Main.Component] = true
		usedComponents[binary.GoComponent] = true
		for _, dep := range binary.Deps {
			usedComponents[dep.Component] = true
		}
		if expectedBinaries[binary.Path] {
			return fmt.Errorf("duplicate executable")
		}
		expectedBinaries[binary.Path] = true
		full, err := boundFile(root, binary.Path)
		if err != nil {
			return err
		}
		hash, err := hashFile(full)
		if err != nil {
			return err
		}
		if hash != binary.SHA256 {
			return fmt.Errorf("executable checksum changed: %s", binary.Path)
		}
		info, err := buildinfo.ReadFile(full)
		if err != nil {
			return err
		}
		if info.GoVersion != binary.GoVersion || len(info.Deps) != len(binary.Deps) {
			return fmt.Errorf("binary build metadata changed")
		}
		toolchain, ok := components[binary.GoComponent]
		if !ok || toolchain.Kind != "go-toolchain" || toolchain.Version != info.GoVersion {
			return fmt.Errorf("incorrect Go toolchain license")
		}
		if err = sameModule(binary.Main, rawModule(info.Main), components, true, binary.Path); err != nil {
			return err
		}
		for i, dep := range info.Deps {
			if err = sameModule(binary.Deps[i], rawModule(*dep), components, false, binary.Path); err != nil {
				return err
			}
		}
	}
	if len(actualBinaries) != len(expectedBinaries) {
		return fmt.Errorf("runtime executable set changed")
	}
	for _, p := range actualBinaries {
		if !expectedBinaries[p] {
			return fmt.Errorf("unlicensed executable %s", p)
		}
	}
	expectedFiles := map[string]File{}
	for _, entry := range manifest.Files {
		usedComponents[entry.Component] = true
		if _, ok := expectedFiles[entry.Path]; ok {
			return fmt.Errorf("duplicate runtime file")
		}
		expectedFiles[entry.Path] = entry
		if _, ok := components[entry.Component]; !ok {
			return fmt.Errorf("missing owner for %s", entry.Path)
		}
		full, err := boundFile(root, entry.Path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(full)
		if err != nil {
			return err
		}
		if entry.Link != "" {
			link, err := os.Readlink(full)
			if err != nil || link != entry.Link {
				return fmt.Errorf("runtime symlink changed: %s", entry.Path)
			}
		} else {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("runtime file type changed")
			}
			hash, err := hashFile(full)
			if err != nil {
				return err
			}
			if hash != entry.SHA256 {
				return fmt.Errorf("runtime file checksum changed: %s", entry.Path)
			}
		}
	}
	for id := range components {
		if !usedComponents[id] {
			return fmt.Errorf("orphaned license component: %s", id)
		}
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, Location+"/") {
			if !expectedNotice[strings.TrimPrefix(rel, Location+"/")] {
				return fmt.Errorf("stale/unmapped notice: %s", rel)
			}
			return nil
		}
		if _, ok := expectedFiles[rel]; ok {
			return nil
		}
		// Container engines inject these files outside the immutable image payload.
		if d.Type().IsRegular() && (rel == "etc/hosts" || rel == "etc/hostname" || rel == "etc/resolv.conf" || rel == ".dockerenv") {
			return nil
		}
		return fmt.Errorf("unmapped runtime file: %s", rel)
	})
}
