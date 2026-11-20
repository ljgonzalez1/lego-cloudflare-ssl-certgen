package imagebuild

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveEntry struct {
	name, contents string
	kind           byte
}

func makeArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zip := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zip)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		size := int64(len(entry.contents))
		if kind != tar.TypeReg {
			size = 0
		}
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Typeflag: kind, Mode: 0755, Size: size, Linkname: "../../escape"}); err != nil {
			t.Fatal(err)
		}
		if size > 0 {
			if _, err := archive.Write([]byte(entry.contents)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zip.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func goodArchive(t *testing.T) []byte {
	return makeArchive(t, archiveEntry{name: "lego", contents: "fixture executable"}, archiveEntry{name: "LICENSE", contents: "fixture license"})
}

func TestMapEveryReleaseArchitecture(t *testing.T) {
	for _, item := range []struct{ arch, variant, want string }{
		{"amd64", "", "amd64"}, {"amd64", "v1", "amd64"}, {"386", "", "386"},
		{"arm", "v6", "armv6"}, {"arm", "v7", "armv7"}, {"arm64", "", "arm64"}, {"arm64", "v8", "arm64"},
	} {
		got, err := ReleaseArchitecture(item.arch, item.variant)
		if err != nil || got != item.want {
			t.Fatalf("%+v: %s %v", item, got, err)
		}
	}
	for _, arch := range []string{"", "x86_64", "mips", "arm"} {
		if _, err := ReleaseArchitecture(arch, ""); err == nil {
			t.Fatalf("accepted unsupported architecture %q", arch)
		}
	}
}

func TestDownloadChecksChecksumBeforeExtracting(t *testing.T) {
	archive := goodArchive(t)
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/checksums":
			fmt.Fprintf(writer, "%s  fixture.tar.gz\n", digest)
		case "/archive":
			_, _ = writer.Write(archive)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	downloader := Downloader{Client: server.Client(), Attempts: 1}
	output := filepath.Join(t.TempDir(), "out")
	if err := downloader.Download(context.Background(), output, server.URL+"/checksums", "fixture.tar.gz", server.URL+"/archive", ""); err != nil {
		t.Fatal(err)
	}
	for name, mode := range map[string]os.FileMode{"lego": 0755, "LICENSE": 0644} {
		info, err := os.Stat(filepath.Join(output, name))
		if err != nil || info.Mode().Perm() != mode || info.Size() == 0 {
			t.Fatalf("%s: %v", name, err)
		}
	}
	for _, item := range []struct{ archive, digest string }{
		{"fixture.tar.gz", strings.Repeat("0", 64)}, {"../escape", digest}, {"fixture.tar.gz", "invalid"},
	} {
		output = filepath.Join(t.TempDir(), "out")
		if err := downloader.Download(context.Background(), output, server.URL+"/missing", item.archive, server.URL+"/archive", item.digest); err == nil {
			t.Fatalf("accepted %+v", item)
		}
		if _, err := os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("failed download created output")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := downloader.fetch(ctx, server.URL+"/archive", 1024); err == nil {
		t.Fatal("canceled download succeeded")
	}
	if _, err := downloader.fetch(context.Background(), server.URL+"/archive", 8); err == nil {
		t.Fatal("oversized download succeeded")
	}
}

func TestRejectMalformedOrLinkedArchiveFiles(t *testing.T) {
	good := goodArchive(t)
	badCRC := append([]byte(nil), good...)
	badCRC[len(badCRC)-8] ^= 0xff
	for name, contents := range map[string][]byte{
		"not gzip": []byte("invalid"), "truncated": good[:len(good)-9], "bad CRC": badCRC,
		"missing executable":   makeArchive(t, archiveEntry{name: "LICENSE", contents: "license"}),
		"empty executable":     makeArchive(t, archiveEntry{name: "lego"}, archiveEntry{name: "LICENSE", contents: "license"}),
		"duplicate executable": makeArchive(t, archiveEntry{name: "lego", contents: "one"}, archiveEntry{name: "lego", contents: "two"}, archiveEntry{name: "LICENSE", contents: "license"}),
		"symlink executable":   makeArchive(t, archiveEntry{name: "lego", kind: tar.TypeSymlink}, archiveEntry{name: "LICENSE", contents: "license"}),
		"hardlink license":     makeArchive(t, archiveEntry{name: "lego", contents: "exe"}, archiveEntry{name: "LICENSE", kind: tar.TypeLink}),
	} {
		t.Run(name, func(t *testing.T) {
			output := filepath.Join(t.TempDir(), "out")
			if err := ExtractArchive(contents, output); err == nil {
				t.Fatal("malformed archive succeeded")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("malformed archive left output")
			}
		})
	}
}

func TestDownloaderRejectsHTTPDowngrades(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "http://127.0.0.1/unsafe", http.StatusFound)
	}))
	defer server.Close()
	downloader := NewDownloader()
	downloader.Attempts = 1
	downloader.Client.Transport = server.Client().Transport
	if _, err := downloader.fetch(context.Background(), server.URL, 1024); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("HTTP downgrade was accepted: %v", err)
	}
}

func TestArchiveCannotOverwriteOutsideItsOutput(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	contents := makeArchive(t, archiveEntry{name: "../outside", contents: "replaced"}, archiveEntry{name: "lego", contents: "executable"}, archiveEntry{name: "LICENSE", contents: "license"})
	output := filepath.Join(root, "out")
	if err := ExtractArchive(contents, output); err != nil {
		t.Fatal(err)
	}
	actual, _ := os.ReadFile(outside)
	if string(actual) != "original" {
		t.Fatal("archive escaped the output directory")
	}
	if err := os.Remove(filepath.Join(output, "lego")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(output, "lego")); err != nil {
		t.Fatal(err)
	}
	if err := ExtractArchive(contents, output); err == nil {
		t.Fatal("followed an output file symlink")
	}
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(output, linked); err != nil {
		t.Fatal(err)
	}
	if err := ExtractArchive(contents, linked); err == nil {
		t.Fatal("followed an output directory symlink")
	}
}

func TestTargetEnvironmentSetsIndependentCrossCompilation(t *testing.T) {
	t.Setenv("GOARCH", "wrong")
	t.Setenv("CGO_ENABLED", "1")
	for _, item := range []struct{ arch, variant string }{{"amd64", ""}, {"386", ""}, {"arm", "v6"}, {"arm", "v7"}, {"arm64", ""}} {
		t.Setenv("TARGETARCH", item.arch)
		t.Setenv("TARGETVARIANT", item.variant)
		environment, err := TargetEnvironment()
		if err != nil {
			t.Fatal(err)
		}
		settings := map[string]string{}
		for _, entry := range environment {
			key, value, _ := strings.Cut(entry, "=")
			settings[key] = value
		}
		if settings["GOARCH"] != item.arch || settings["CGO_ENABLED"] != "0" || settings["GOOS"] != "linux" || settings["GOTOOLCHAIN"] != "local" {
			t.Fatalf("incorrect environment: %v", settings)
		}
		if item.arch == "arm" && settings["GOARM"] != strings.TrimPrefix(item.variant, "v") {
			t.Fatal("incorrect ARM level")
		}
	}
}

func TestPrepareRuntimeContainsOnlyRequiredDataAndLicenses(t *testing.T) {
	// Production uses the native Alpine stage. A fixture tree makes this test
	// independent of the host distribution and needs no network or root account.
	source := t.TempDir()
	for path, data := range map[string]string{
		"etc/ssl/certs/ca-certificates.crt": "fixture CA", "etc/alpine-release": "3.24.4", "etc/os-release": "ID=alpine\n", "usr/share/zoneinfo/UTC": "fixture zone",
	} {
		target := filepath.Join(source, path)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(t.TempDir(), "runtime")
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Error(err)
		}
	}()
	if err := prepareRuntimeFrom(root, "licenses", source); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"Go/LICENSE", "go-yaml/LICENSE", "go-yaml/NOTICE", "certgen/LICENSE", "ca-certificates/MPL-2.0", "NOTICE"} {
		info, err := os.Stat(filepath.Join(root, "usr/share/licenses", path))
		if err != nil || info.Size() == 0 {
			t.Fatalf("missing license %s: %v", path, err)
		}
	}
	if info, err := os.Stat(filepath.Join(root, "tmp")); err != nil || info.Mode()&os.ModeSticky == 0 || info.Mode().Perm() != 0777 {
		t.Fatal("incorrect /tmp permissions")
	}
	if _, err := os.Stat(filepath.Join(root, "bin/sh")); !os.IsNotExist(err) {
		t.Fatal("shell copied into runtime")
	}
}
