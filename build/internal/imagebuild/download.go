package imagebuild

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const maximumArchiveSize = 128 << 20

var filenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var checksumPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9_.-]+)?$`)

func value(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func ReleaseArchitecture(architecture, variant string) (string, error) {
	switch architecture + "/" + variant {
	case "amd64/", "amd64/v1":
		return "amd64", nil
	case "386/":
		return "386", nil
	case "arm/v6":
		return "armv6", nil
	case "arm/v7":
		return "armv7", nil
	case "arm64/", "arm64/v8":
		return "arm64", nil
	}
	return "", errors.New("Unsupported target platform. Use linux/amd64, linux/386, linux/arm/v6, linux/arm/v7 or linux/arm64.")
}

type Downloader struct {
	Client   *http.Client
	Attempts int

	Progress func(string, ...any)
}

func NewDownloader() Downloader {
	return Downloader{Client: &http.Client{Timeout: 180 * time.Second, CheckRedirect: func(request *http.Request, previous []*http.Request) error {
		if request.URL.Scheme != "https" || len(previous) >= 10 {
			return errors.New("release download requires HTTPS and at most ten redirects")
		}
		return nil
	}}, Attempts: 4}
}
func (d Downloader) fetch(ctx context.Context, url string, maximum int64) ([]byte, error) {
	attempts := d.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		if d.Progress != nil {
			d.Progress("HTTP release attempt %d/%d GET %s", attempt+1, attempts, url)
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		response, err := d.Client.Do(request)
		if err == nil {
			if d.Progress != nil {
				d.Progress("HTTP release status=%d bytes_expected=%d", response.StatusCode, response.ContentLength)
			}
			contents, readErr := io.ReadAll(io.LimitReader(response.Body, maximum+1))
			response.Body.Close()
			switch {
			case readErr != nil:
				last = readErr
			case response.StatusCode != 200:
				last = fmt.Errorf("HTTP %d fetching release asset", response.StatusCode)
				if response.StatusCode < 500 && response.StatusCode != 429 {
					return nil, last
				}
			case int64(len(contents)) > maximum:
				return nil, errors.New("release asset exceeds its size limit")
			default:
				if d.Progress != nil {
					d.Progress("HTTP release body complete: %d bytes", len(contents))
				}
				return contents, nil
			}
		} else {
			last = err
		}
		if attempt+1 < attempts {
			if d.Progress != nil {
				d.Progress("HTTP release retry after %ds: %v", attempt+1, last)
			}
			timer := time.NewTimer(time.Duration(attempt+1) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, last
}
func (d Downloader) Download(ctx context.Context, output, releaseBase, archive, url, expected string) error {
	if !filenamePattern.MatchString(archive) {
		return errors.New("LEGO_TARBALL must be a release archive filename.")
	}
	if expected == "" {
		checksums, err := d.fetch(ctx, releaseBase, 1<<20)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(checksums), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && fields[1] == archive {
				expected = fields[0]
				break
			}
		}
	}
	if !checksumPattern.MatchString(expected) {
		return errors.New("Missing or invalid SHA-256 for the lego release.")
	}
	contents, err := d.fetch(ctx, url, maximumArchiveSize)
	if err != nil {
		return err
	}
	actual := sha256.Sum256(contents)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), expected) {
		return errors.New("SHA-256 verification failed for the lego archive.")
	}
	if d.Progress != nil {
		d.Progress("lego archive SHA-256 verified: %x; extracting %s", actual, archive)
	}
	return ExtractArchive(contents, output)
}
func ExtractArchive(contents []byte, output string) error {
	if info, err := os.Lstat(output); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return errors.New("output must be a directory, not a link or file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	work, err := os.MkdirTemp(filepath.Dir(output), ".lego-extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	compressed, err := gzip.NewReader(bytes.NewReader(contents))
	if err != nil {
		return err
	}
	defer compressed.Close()
	// Bound the expanded stream as well as the download. Even ignored entries
	// must not turn a tiny compressed archive into unbounded decompression work.
	expanded := &io.LimitedReader{R: compressed, N: 2 * maximumArchiveSize}
	archive := tar.NewReader(expanded)
	found := map[string]bool{}
	entries := 0
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if entries > 1024 || header.Size < 0 || header.Size > maximumArchiveSize {
			return errors.New("release archive exceeds the extraction limits")
		}
		if header.Name != "lego" && header.Name != "LICENSE" {
			continue
		}
		if found[header.Name] || header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA || header.Size <= 0 || header.Size > maximumArchiveSize {
			return fmt.Errorf("The lego archive must contain a nonempty regular %s file.", header.Name)
		}
		file, err := os.OpenFile(filepath.Join(work, header.Name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, archive, header.Size)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		found[header.Name] = true
	}
	// tar EOF can precede the gzip trailer. Read it to validate its CRC before
	// committing either file, and reject truncated or over-expanded streams.
	if _, err = io.Copy(io.Discard, expanded); err != nil {
		return err
	}
	if expanded.N == 0 {
		return errors.New("release archive exceeds the expanded size limit")
	}
	for _, name := range []string{"lego", "LICENSE"} {
		if !found[name] {
			return fmt.Errorf("The lego archive must contain a nonempty regular %s file.", name)
		}
		info, err := os.Lstat(filepath.Join(output, name))
		if err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			return errors.New("Refusing to replace a link or directory in the output.")
		}
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err = os.MkdirAll(output, 0755); err != nil {
		return err
	}
	for _, name := range []string{"lego", "LICENSE"} {
		mode := os.FileMode(0644)
		if name == "lego" {
			mode = 0755
		}
		if err = os.Chmod(filepath.Join(work, name), mode); err != nil {
			return err
		}
		if err = os.Rename(filepath.Join(work, name), filepath.Join(output, name)); err != nil {
			return err
		}
	}
	return nil
}
func DownloadFromEnvironment(output string) error {
	architecture, err := ReleaseArchitecture(os.Getenv("TARGETARCH"), os.Getenv("TARGETVARIANT"))
	if err != nil {
		return err
	}
	version := os.Getenv("LEGO_VERSION")
	if !versionPattern.MatchString(version) {
		return errors.New("The lego release version must be a complete vX.Y.Z version.")
	}
	release := "https://github.com/go-acme/lego/releases/download/" + version
	archive := value("LEGO_TARBALL", "lego_"+version+"_linux_"+architecture+".tar.gz")
	url := value("LEGO_URL", release+"/"+archive)
	if !strings.HasPrefix(url, "https://") {
		return errors.New("LEGO_URL must use HTTPS.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	downloader := NewDownloader()
	downloader.Progress = Progress
	return downloader.Download(ctx, output, release+"/lego_"+strings.TrimPrefix(version, "v")+"_checksums.txt", archive, url, os.Getenv("LEGO_SHA256"))
}
