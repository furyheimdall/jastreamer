//go:build windows

package security

import (
	"os"
	"path/filepath"
	"strings"
)

func canonicalFilePathsEqual(configured, resolved string) bool {
	if configured == resolved {
		return true
	}
	if pathHasSymlinkComponent(configured) {
		return false
	}
	left, leftErr := os.Lstat(configured)
	right, rightErr := os.Lstat(resolved)
	if leftErr != nil || rightErr != nil || left.Mode()&os.ModeSymlink != 0 || right.Mode()&os.ModeSymlink != 0 {
		return false
	}
	return os.SameFile(left, right)
}

func pathHasSymlinkComponent(path string) bool {
	volume := filepath.VolumeName(path)
	current := volume
	rest := strings.TrimPrefix(path, volume)
	rest = strings.TrimPrefix(rest, `\`)
	if rest == "" {
		return false
	}
	for rest != "" {
		part, remaining, found := strings.Cut(rest, `\`)
		if part != "" {
			if current == volume {
				current = volume + `\` + part
			} else {
				current = current + `\` + part
			}
			info, err := os.Lstat(current)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return true
			}
		}
		if !found {
			break
		}
		rest = remaining
	}
	return false
}
