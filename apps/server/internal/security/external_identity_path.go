package security

import (
	"errors"
	"path/filepath"
)

func canonicalConfiguredPath(absolute string) (string, error) {
	cleaned := filepath.Clean(absolute)
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil || !canonicalFilePathsEqual(cleaned, resolved) {
		return "", errors.New("configured path must be canonical and contain no symlinks")
	}
	return resolved, nil
}
