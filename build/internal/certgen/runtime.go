package certgen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func timestamp(epoch time.Time) string {
	_, offset := epoch.Zone()
	sign := "+"
	if offset < 0 {
		sign = "-"
		offset = -offset
	}
	return epoch.Format("2006-01-02_15.04.05") + fmt.Sprintf("_GMT%s%d", sign, offset/3600)
}
func MakeOutputPath(issue bool, root, group string, location *time.Location) (string, error) {
	if !validGroup(group) {
		return "", fmt.Errorf("invalid output path request")
	}
	if location == nil {
		location = time.UTC
	}
	epoch := time.Now().In(location)
	parent := filepath.Join(root, group)
	stamp := timestamp(epoch)
	if issue {
		if info, err := os.Lstat(parent); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Cannot use certificate folder: %s", parent)
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		if err := os.MkdirAll(parent, 0700); err != nil {
			return "", err
		}
		for {
			err := os.Mkdir(filepath.Join(parent, stamp), 0700)
			if err == nil {
				break
			}
			if !os.IsExist(err) {
				return "", fmt.Errorf("Cannot create timestamp folder: %w", err)
			}
			epoch = epoch.Add(time.Second)
			stamp = timestamp(epoch)
		}
	}
	target := filepath.Join(parent, stamp, "requested_"+timestamp(epoch.Add(55*time.Second))+"_expires_"+timestamp(epoch.Add(90*24*time.Hour-12*time.Hour)))
	if issue {
		if err := os.Mkdir(target, 0700); err != nil {
			return "", fmt.Errorf("Cannot create certificate folder: %w", err)
		}
	}
	return target, nil
}
func outputMounted(path string) bool {
	bytes, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(bytes), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 4 && fields[4] == path {
			return true
		}
	}
	return false
}
