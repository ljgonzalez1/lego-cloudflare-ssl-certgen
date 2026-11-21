package licenseaudit

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

// NativeSources authenticates pinned Go modules through Go's checksum database
// and Alpine source archives through the exact installed package's aports commit.
// Source downloads/cache never enter the final runtime.
type NativeSources struct {
	Go, Toolchain, Root, DataRoot, Work string
	Client                              *http.Client
	modules                             map[string]Source
	goRoots                             map[string]Source

	Progress func(string, ...any)
}

func NewNativeSources(root, toolchain string) (*NativeSources, error) {
	work, err := os.MkdirTemp("", "certgen-license-sources-")
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 180 * time.Second, CheckRedirect: func(r *http.Request, previous []*http.Request) error {
		if r.URL.Scheme != "https" || len(previous) > 5 {
			return fmt.Errorf("source download requires HTTPS")
		}
		return nil
	}}
	return &NativeSources{Go: filepath.Join(toolchain, "bin/go"), Toolchain: toolchain, Root: root, DataRoot: "/", Work: work, Client: client, modules: map[string]Source{}, goRoots: map[string]Source{}}, nil
}
func (s *NativeSources) Close() { _ = os.RemoveAll(s.Work) }
func publicGoEnvironment() ([]string, error) {
	if os.Getenv("GOSUMDB") == "off" {
		return nil, fmt.Errorf("license sources require Go checksum authentication; GOSUMDB=off is refused")
	}
	replacements := map[string]string{"GOOS": "", "GOARCH": "", "GOARM": "", "GOAMD64": "", "GOFLAGS": "", "GOWORK": "off", "GOTOOLCHAIN": "local", "GOPRIVATE": "", "GONOSUMDB": ""}
	env := []string{}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, ok := replacements[key]; !ok {
			env = append(env, entry)
		}
	}
	for key, value := range replacements {
		env = append(env, key+"="+value)
	}
	return env, nil
}
func (s *NativeSources) Module(module, version, expected string) (Source, error) {
	key := module + "@" + version
	if cached, ok := s.modules[key]; ok {
		if expected != "" && cached.Sum != expected {
			return Source{}, fmt.Errorf("module checksum mismatch for %s", key)
		}
		return cached, nil
	}
	if strings.ContainsAny(key, "\r\n\x00") || !strings.HasPrefix(version, "v") || strings.ContainsAny(version, "/\\ ") {
		return Source{}, fmt.Errorf("unsupported module version %q", key)
	}
	environment, err := publicGoEnvironment()
	if err != nil {
		return Source{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	started := time.Now()
	logProgress(s.Progress, "Go module download: %s (limit 4m; authenticated shared cache may be in use)", key)
	command := exec.CommandContext(ctx, s.Go, "mod", "download", "-json", key)
	command.Dir = s.Work
	command.Env = environment
	// Bound pipe draining too: a surviving VCS child can otherwise keep Output
	// waiting after the Go command exits or its context has been cancelled.
	command.WaitDelay = 5 * time.Second
	logProgress(s.Progress, "EXEC %q; working directory=%s; context limit=4m; pipe drain limit=5s", command.Args, s.Work)
	data, err := command.Output()
	if err != nil {
		var failure struct{ Error string }
		_ = json.Unmarshal(data, &failure)
		if failure.Error == "" {
			if exited, ok := err.(*exec.ExitError); ok {
				failure.Error = string(exited.Stderr)
			}
		}
		if ctx.Err() != nil {
			failure.Error = ctx.Err().Error() + ": " + failure.Error
		}
		return Source{}, fmt.Errorf("cannot obtain authenticated source for %s after %s: %w: %s", key, time.Since(started).Round(time.Millisecond), err, safeDiagnostic(failure.Error))
	}
	logProgress(s.Progress, "Go module download completed: %s in %s", key, time.Since(started).Round(time.Millisecond))
	var result struct{ Path, Version, Dir, Zip, Sum, Error string }
	if err = json.Unmarshal(data, &result); err != nil {
		return Source{}, err
	}
	if result.Error != "" || result.Path != module || result.Version != version || !strings.HasPrefix(result.Sum, "h1:") || result.Zip == "" {
		return Source{}, fmt.Errorf("incomplete authenticated module source for %s", key)
	}
	if expected != "" && expected != result.Sum {
		return Source{}, fmt.Errorf("module checksum mismatch for %s", key)
	}
	directory := filepath.Join(s.Work, "module-"+digest([]byte(key)))
	logProgress(s.Progress, "authenticate cached ZIP and extract source notices: %s", key)
	if err = authenticatedModuleSourceWithProgress(result.Zip, result.Sum, directory, s.Progress); err != nil {
		return Source{}, err
	}
	logProgress(s.Progress, "module source ready: %s", key)
	source := Source{Directory: directory, Origin: "Go module proxy source: " + key, Sum: result.Sum}
	s.modules[key] = source
	return source, nil
}
func readGoVersion(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("missing Go VERSION")
	}
	return fields[0], nil
}
func (s *NativeSources) GoSource(version string) (Source, error) {
	logProgress(s.Progress, "resolve Go SDK source: %s", version)
	if cached, ok := s.goRoots[version]; ok {
		return cached, nil
	}
	native, err := readGoVersion(s.Toolchain)
	if err != nil {
		return Source{}, err
	}
	root := s.Toolchain
	sum := ""
	origin := "https://go.dev/dl/#" + version
	if native != version {
		if !regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
			return Source{}, fmt.Errorf("unrecognized executable Go toolchain: %q", version)
		}
		source, err := s.Module("golang.org/toolchain", "v0.0.1-"+version+".linux-amd64", "")
		if err != nil {
			return Source{}, err
		}
		root = source.Directory
		sum = source.Sum
		origin = source.Origin
	}
	actual, err := readGoVersion(root)
	if err != nil || actual != version {
		return Source{}, fmt.Errorf("Go source version does not match executable %s", version)
	}
	source := Source{Directory: root, Origin: origin, Sum: sum, License: "BSD-3-Clause with bundled third-party notices"}
	s.goRoots[version] = source
	return source, nil
}
func (s *NativeSources) fetch(url string, limit int64) (data []byte, err error) {
	started := time.Now()
	timeout := time.Duration(0)
	if s.Client != nil {
		timeout = s.Client.Timeout
	}
	logProgress(s.Progress, "HTTP GET %s (client timeout %s)", url, timeout)
	defer func() {
		if err != nil {
			logProgress(s.Progress, "HTTP failed after %s: %s: %v", time.Since(started).Round(time.Millisecond), url, err)
		} else {
			logProgress(s.Progress, "HTTP body complete: %s (%d bytes, %s)", url, len(data), time.Since(started).Round(time.Millisecond))
		}
	}()
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("untrusted non-HTTPS source URL")
	}
	response, err := s.Client.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	logProgress(s.Progress, "HTTP status %d: %s", response.StatusCode, url)
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("source server returned HTTP %d", response.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("source size bound exceeded")
	}
	return data, nil
}
func installedPackage(data []byte, name string) (map[string]string, error) {
	for _, block := range strings.Split(string(data), "\n\n") {
		fields := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			key, value, ok := strings.Cut(line, ":")
			if ok {
				fields[key] = value
			}
		}
		if fields["P"] == name {
			if fields["V"] == "" || fields["L"] == "" || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(fields["c"]) {
				return nil, fmt.Errorf("incomplete installed package provenance for %s", name)
			}
			return fields, nil
		}
	}
	return nil, fmt.Errorf("installed package metadata not found: %s", name)
}

// Parse quoted/unquoted scalar APKBUILD assignments as data; never execute shell.
func assignment(text, key string) (string, error) {
	expression := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `=`)
	at := expression.FindStringIndex(text)
	if at == nil {
		return "", fmt.Errorf("APKBUILD has no %s", key)
	}
	value := text[at[1]:]
	if value == "" {
		return "", fmt.Errorf("empty APKBUILD field")
	}
	if value[0] == '"' || value[0] == '\'' {
		quote := value[0]
		value = value[1:]
		end := strings.IndexByte(value, quote)
		if end < 0 {
			return "", fmt.Errorf("unterminated APKBUILD field")
		}
		return value[:end], nil
	}
	return strings.SplitN(value, "\n", 2)[0], nil
}
func sourceArchives(apkbuild string, fields map[string]string) ([][3]string, error) {
	sources, err := assignment(apkbuild, "source")
	if err != nil {
		return nil, err
	}
	sums, err := assignment(apkbuild, "sha512sums")
	if err != nil {
		return nil, err
	}
	version := strings.Split(fields["V"], "-r")[0]
	replacer := strings.NewReplacer("${pkgver}", version, "$pkgver", version, "${pkgname}", fields["o"], "$pkgname", fields["o"])
	constants := map[string]string{"pkgver": version, "pkgname": fields["o"]}
	for _, match := range regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_]*)=(.*)$`).FindAllStringSubmatch(apkbuild, -1) {
		key := match[1]
		if key == "source" || key == "sha512sums" || key == "pkgver" || key == "pkgname" {
			continue
		}
		v := strings.Trim(match[2], "\"'")
		if regexp.MustCompile(`^[A-Za-z0-9_./+${}-]+$`).MatchString(v) {
			constants[key] = v
		}
	}
	for pass := 0; pass < 10; pass++ {
		for k, v := range constants {
			sources = strings.ReplaceAll(sources, "${"+k+"}", v)
			sources = strings.ReplaceAll(sources, "$"+k, v)
		}
	}
	sources = replacer.Replace(sources)
	sums = replacer.Replace(sums)
	checksums := map[string]string{}
	for _, line := range strings.Split(sums, "\n") {
		items := strings.Fields(line)
		if len(items) == 2 && regexp.MustCompile(`^[a-fA-F0-9]{128}$`).MatchString(items[0]) {
			checksums[items[1]] = strings.ToLower(items[0])
		}
	}
	var result [][3]string
	for _, source := range strings.Fields(sources) {
		filename, url, alias := strings.Cut(source, "::")
		if !alias {
			url = source
			filename = path.Base(strings.SplitN(url, "?", 2)[0])
		}
		if !strings.HasPrefix(url, "https://") {
			continue
		}
		if !strings.HasSuffix(filename, ".tar.gz") && !strings.HasSuffix(filename, ".tar.bz2") {
			continue
		}
		if strings.ContainsAny(url, "$`\\") || !safeRelative(filename) || strings.Contains(filename, "/") {
			return nil, fmt.Errorf("unsupported APKBUILD source expansion")
		}
		sum := checksums[filename]
		if sum == "" {
			return nil, fmt.Errorf("source archive lacks a SHA-512 binding: %s", filename)
		}
		result = append(result, [3]string{filename, url, sum})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no supported checksum-bound source archives in APKBUILD")
	}
	return result, nil
}
func extractSource(data []byte, name, directory string) error {
	var reader io.Reader
	if strings.HasSuffix(name, ".gz") {
		zip, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer zip.Close()
		reader = zip
	} else {
		reader = bzip2.NewReader(bytes.NewReader(data))
	}
	archive := tar.NewReader(io.LimitReader(reader, 512<<20))
	total := int64(0)
	count := 0
	for {
		entry, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		count++
		if count > 100000 {
			return fmt.Errorf("source archive has too many entries")
		}
		relative := strings.TrimPrefix(entry.Name, "./")
		if entry.Typeflag == tar.TypeDir {
			relative = strings.TrimSuffix(relative, "/")
			if relative == "" || relative == "." {
				continue
			}
		}
		if !safeRelative(relative) {
			return fmt.Errorf("unsafe source archive path")
		}
		if entry.Typeflag == tar.TypeDir {
			continue
		}
		if entry.Typeflag != tar.TypeReg && entry.Typeflag != tar.TypeRegA {
			if licenseName(path.Base(relative)) || strings.Contains(strings.ToLower("/"+relative), "/licenses/") {
				return fmt.Errorf("source archive contains an unsupported irregular notice: %s", relative)
			}
			continue
		}
		if entry.Size < 0 || entry.Size > maxDocument {
			return fmt.Errorf("source file size exceeds bound")
		}
		total += entry.Size
		if total > 512<<20 {
			return fmt.Errorf("expanded source exceeds bound")
		}
		destination := filepath.Join(directory, filepath.FromSlash(relative))
		if err = os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, archive, entry.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

func (s *NativeSources) aports(commit, relative string, valid func([]byte) bool) ([]byte, string, error) {
	urls := []string{
		"https://raw.githubusercontent.com/alpinelinux/aports/" + commit + "/" + relative,
		"https://gitlab.alpinelinux.org/alpine/aports/-/raw/" + commit + "/" + relative,
		"https://git.alpinelinux.org/aports/plain/" + relative + "?id=" + commit,
	}
	var failures []string
	for _, url := range urls {
		data, err := s.fetch(url, maxDocument)
		if err == nil && valid(data) {
			logProgress(s.Progress, "installed package recipe verified: %s at %s", relative, commit)
			return data, url, nil
		}
		if err == nil {
			err = fmt.Errorf("response did not match the installed package recipe")
		}
		failures = append(failures, url+": "+err.Error())
		logProgress(s.Progress, "aports candidate rejected; trying next pinned mirror: %s: %v", url, err)
	}
	return nil, "", fmt.Errorf("cannot obtain valid pinned aports evidence: %s at %s: %s", relative, commit, strings.Join(failures, "; "))
}

func packageRecipe(data []byte, fields map[string]string) bool {
	values := map[string]string{}
	for _, key := range []string{"pkgname", "pkgver", "pkgrel", "license"} {
		value, err := assignment(string(data), key)
		if err != nil {
			return false
		}
		values[key] = value
	}
	return values["pkgname"] == fields["o"] && values["pkgver"]+"-r"+values["pkgrel"] == fields["V"] &&
		strings.Join(strings.Fields(values["license"]), " ") == strings.Join(strings.Fields(fields["L"]), " ")
}

// Mirrors may supply the same archive, never a different revision or checksum.
func (s *NativeSources) sourceArchive(entry [3]string) ([]byte, string, error) {
	urls := []string{entry[1]}
	release, err := os.ReadFile(filepath.Join(s.DataRoot, "etc/alpine-release"))
	if err == nil {
		match := regexp.MustCompile(`^([0-9]+\.[0-9]+)\.[0-9]+$`).FindStringSubmatch(strings.TrimSpace(string(release)))
		if match != nil {
			// Prefer Alpine's distribution mirror over source hosts with bot challenges.
			urls = append([]string{"https://distfiles.alpinelinux.org/distfiles/v" + match[1] + "/" + entry[0]}, urls...)
		}
	}
	var failures []string
	for _, url := range urls {
		data, err := s.fetch(url, 128<<20)
		if err == nil {
			sum := sha512.Sum512(data)
			if hex.EncodeToString(sum[:]) == entry[2] {
				logProgress(s.Progress, "source archive SHA-512 verified: %s", entry[0])
				return data, url, nil
			}
			err = fmt.Errorf("source archive checksum mismatch")
		}
		failures = append(failures, url+": "+err.Error())
		logProgress(s.Progress, "source archive candidate rejected: %s: %v", url, err)
	}
	return nil, "", fmt.Errorf("cannot obtain checksum-verified source archive %s: %s", entry[0], strings.Join(failures, "; "))
}

func (s *NativeSources) Data(kind string) (Source, error) {
	logProgress(s.Progress, "check runtime data payload: %s", kind)
	if kind == "tzdata" {
		if _, err := os.Stat(filepath.Join(s.Root, "usr/share/zoneinfo.zip")); err == nil {
			hash, err := hashFile(filepath.Join(s.Root, "usr/share/zoneinfo.zip"))
			if err != nil {
				return Source{}, err
			}
			original, err := hashFile(filepath.Join(s.Toolchain, "lib/time/zoneinfo.zip"))
			if err != nil || hash != original {
				return Source{}, fmt.Errorf("timezone ZIP does not match native Go source")
			}
			directory := filepath.Join(s.Work, "timezone-notices")
			if err = os.MkdirAll(directory, 0755); err != nil {
				return Source{}, err
			}
			data, err := os.ReadFile(filepath.Join(s.Toolchain, "lib/time/README"))
			if err != nil {
				return Source{}, err
			}
			if err = os.WriteFile(filepath.Join(directory, "LICENSE"), data, 0644); err != nil {
				return Source{}, err
			}
			update, err := os.ReadFile(filepath.Join(s.Toolchain, "lib/time/update.bash"))
			if err != nil {
				return Source{}, err
			}
			if err = os.WriteFile(filepath.Join(directory, "NOTICE-update-source.txt"), update, 0644); err != nil {
				return Source{}, err
			}
			goLicense, err := os.ReadFile(filepath.Join(s.Toolchain, "LICENSE"))
			if err != nil {
				return Source{}, err
			}
			if err = os.WriteFile(filepath.Join(directory, "COPYING-Go-SDK"), goLicense, 0644); err != nil {
				return Source{}, err
			}
			native, _ := readGoVersion(s.Toolchain)
			return Source{Directory: directory, Origin: "https://go.dev/lib/time/ at " + native + "; zoneinfo.zip SHA-256 " + hash, License: "Public domain (IANA data), as declared in Go lib/time/README; BSD-3-Clause for retained Go SDK update-script notice", Version: native}, nil
		}
	}
	name := kind
	if kind == "ca-certificates" {
		name = "ca-certificates-bundle"
		hash, err := hashFile(filepath.Join(s.Root, "etc/ssl/certs/ca-certificates.crt"))
		if err != nil {
			return Source{}, err
		}
		original, err := hashFile(filepath.Join(s.DataRoot, "etc/ssl/certs/ca-certificates.crt"))
		if err != nil || hash != original {
			return Source{}, fmt.Errorf("CA bundle differs from the installed package payload")
		}
	}
	if kind == "alpine-release" {
		for _, relative := range []string{"etc/alpine-release", "etc/os-release"} {
			actual, err := hashFile(filepath.Join(s.Root, relative))
			if err != nil {
				return Source{}, err
			}
			original, err := hashFile(filepath.Join(s.DataRoot, relative))
			if err != nil || actual != original {
				return Source{}, fmt.Errorf("release data differs from installed package: %s", relative)
			}
		}
	}
	if kind == "tzdata" {
		err := filepath.WalkDir(filepath.Join(s.Root, "usr/share/zoneinfo"), func(p string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(s.Root, p)
			if err != nil {
				return err
			}
			original := filepath.Join(s.DataRoot, relative)
			if entry.Type()&os.ModeSymlink != 0 {
				a, err := os.Readlink(p)
				if err != nil {
					return err
				}
				b, err := os.Readlink(original)
				if err != nil || a != b {
					return fmt.Errorf("timezone link differs from installed package: %s", relative)
				}
				return nil
			}
			a, err := hashFile(p)
			if err != nil {
				return err
			}
			b, err := hashFile(original)
			if err != nil || a != b {
				return fmt.Errorf("timezone data differs from installed package: %s", relative)
			}
			return nil
		})
		if err != nil {
			return Source{}, err
		}
	}
	database, err := os.ReadFile(filepath.Join(s.DataRoot, "lib/apk/db/installed"))
	if err != nil {
		return Source{}, err
	}
	fields, err := installedPackage(database, name)
	if err != nil {
		return Source{}, err
	}
	logProgress(s.Progress, "installed APK: %s %s; declared license %s; aports commit %s", name, fields["V"], fields["L"], fields["c"])
	origin := fields["o"]
	if origin == "" {
		origin = name
	}
	fields["o"] = origin
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(origin) {
		return Source{}, fmt.Errorf("invalid package source origin")
	}
	recipe, base, err := s.aports(fields["c"], "main/"+origin+"/APKBUILD", func(data []byte) bool {
		return packageRecipe(data, fields)
	})
	if err != nil {
		return Source{}, fmt.Errorf("installed package %s %s: %w", name, fields["V"], err)
	}
	directory := filepath.Join(s.Work, "data-"+name)
	if err = os.MkdirAll(directory, 0755); err != nil {
		return Source{}, err
	}
	var notice strings.Builder
	fmt.Fprintf(&notice, "Installed Alpine package: %s %s\nDeclared license: %s\nSource recipe: %s\n\n", name, fields["V"], fields["L"], base)
	// Aports has no repository-root COPYING. Retain the exact recipe (including
	// contributor notices), then collect the checksum-bound upstream notices.
	if err = os.WriteFile(filepath.Join(directory, "NOTICE-APKBUILD.txt"), recipe, 0644); err != nil {
		return Source{}, err
	}
	var archives [][3]string
	if kind != "alpine-release" {
		archives, err = sourceArchives(string(recipe), fields)
		if err != nil {
			return Source{}, err
		}
	} else if fields["L"] != "MIT" || origin != "alpine-base" {
		return Source{}, fmt.Errorf("release metadata source/license changed; explicit review required")
	}
	for index, entry := range archives {
		data, sourceURL, err := s.sourceArchive(entry)
		if err != nil {
			return Source{}, err
		}
		if err = extractSource(data, entry[0], filepath.Join(directory, fmt.Sprint(index))); err != nil {
			return Source{}, err
		}
		logProgress(s.Progress, "source archive extracted: %s", entry[0])
		fmt.Fprintf(&notice, "Corresponding source: %s\nRetrieved from: %s\nSHA-512: %s\n", entry[1], sourceURL, entry[2])
	}
	if err = os.WriteFile(filepath.Join(directory, "NOTICE-source-provenance.txt"), []byte(notice.String()), 0644); err != nil {
		return Source{}, err
	}
	return Source{Directory: directory, Origin: base, Sum: "aports-commit:" + fields["c"], License: fields["L"], Version: fields["V"]}, nil
}

// Authenticate the cached archive itself using Go's documented h1 directory-hash
// format. Do not trust a mutable extracted module-cache directory as evidence.
func authenticatedModuleSource(archivePath, expected, destination string) error {
	return authenticatedModuleSourceWithProgress(archivePath, expected, destination, nil)
}

func authenticatedModuleSourceWithProgress(archivePath, expected, destination string, progress func(string, ...any)) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	files := append([]*zip.File(nil), archive.File...)
	logProgress(progress, "authenticate Go ZIP: %d entries", len(files))
	sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
	outer := sha256.New()
	total := uint64(0)
	seen := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file.Name, "/") {
			continue
		}
		if !safeRelative(file.Name) || strings.ContainsAny(file.Name, "\r\n") || seen[file.Name] {
			return fmt.Errorf("invalid module ZIP path")
		}
		seen[file.Name] = true
		total += file.UncompressedSize64
		if total > 500<<20 || file.UncompressedSize64 > 500<<20 {
			return fmt.Errorf("module source exceeds bound")
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, io.LimitReader(reader, 500<<20+1))
		closeErr := reader.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		fmt.Fprintf(outer, "%x  %s\n", h.Sum(nil), file.Name)
	}
	actual := "h1:" + base64.StdEncoding.EncodeToString(outer.Sum(nil))
	if actual != expected {
		return fmt.Errorf("cached module ZIP checksum mismatch")
	}
	logProgress(progress, "Go ZIP h1 verified (%d expanded bytes); extracting notice sources", total)
	if len(files) > 100000 {
		return fmt.Errorf("module ZIP has too many files")
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name, "/") {
			continue
		}
		at := strings.IndexByte(file.Name, '@')
		if at < 0 {
			return fmt.Errorf("module ZIP has no versioned root")
		}
		slash := strings.IndexByte(file.Name[at:], '/')
		if slash < 0 {
			return fmt.Errorf("module ZIP has no source path")
		}
		relative := file.Name[at+slash+1:]
		if !safeRelative(relative) {
			return fmt.Errorf("invalid module root")
		}
		keep := licenseName(path.Base(relative)) || strings.Contains(strings.ToLower("/"+relative), "/licenses/") || relative == "VERSION" || relative == "lib/time/README"
		limit := int64(maxDocument)
		if !keep {
			switch strings.ToLower(path.Ext(relative)) {
			case ".go", ".s", ".c", ".h", ".py", ".sh", ".rs", ".txt":
				keep = true
				limit = 64 << 10
			}
		}
		if !keep {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(reader, limit+1))
		reader.Close()
		if err != nil {
			return err
		}
		if len(data) > maxDocument {
			return fmt.Errorf("module notice exceeds bound")
		}
		if int64(len(data)) > limit {
			data = data[:limit]
		}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err = os.WriteFile(target, data, 0644); err != nil {
			return err
		}
	}
	return nil
}

func ToolchainRoot() (string, error) {
	if root := runtime.GOROOT(); root != "" {
		return root, nil
	}
	compiler := os.Getenv("GO")
	if compiler == "" {
		compiler = "go"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, compiler, "env", "GOROOT").Output()
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(string(output))
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("invalid native GOROOT")
	}
	return root, nil
}
