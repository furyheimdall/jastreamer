//go:build windows

package httpapi

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func filesystemRoots() ([]filesystemRoot, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, err
	}
	roots := make([]filesystemRoot, 0, 26)
	for index := range 26 {
		if mask&(uint32(1)<<index) == 0 {
			continue
		}
		name := string(rune('A'+index)) + ":"
		path := name + `\`
		directory, err := os.Open(path)
		if err != nil {
			continue
		}
		_ = directory.Close()
		roots = append(roots, filesystemRoot{Name: name, Path: path})
	}
	return roots, nil
}

func validPlatformFilesystemPath(path string) bool {
	normalized := strings.ReplaceAll(path, "/", `\`)
	lower := strings.ToLower(normalized)
	for _, prefix := range []string{`\\?\`, `\\.\`, `\??\`, `\\??\`} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}

	volume := filepath.VolumeName(normalized)
	switch {
	case len(volume) == 2 && volume[1] == ':':
		letter := volume[0]
		if !((letter >= 'A' && letter <= 'Z') || (letter >= 'a' && letter <= 'z')) {
			return false
		}
		if strings.Contains(normalized[2:], ":") {
			return false
		}
	case strings.HasPrefix(volume, `\\`):
		parts := strings.Split(strings.TrimPrefix(volume, `\\`), `\`)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.Contains(normalized, ":") {
			return false
		}
	default:
		return false
	}

	remainder := strings.TrimLeft(normalized[len(volume):], `\`)
	for _, component := range strings.Split(remainder, `\`) {
		if component != "" && component != ".." && !filepath.IsLocal(component) {
			return false
		}
	}
	return true
}

func platformFilesystemLink(info fs.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && data.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
