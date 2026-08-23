package catalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

var ErrOutsideRoot = errors.New("catalog: path outside root")

func Resolve(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("resolve %q: %w", relative, ErrOutsideRoot)
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("absolute root: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return "", fmt.Errorf("canonical root: %w", err)
	}
	candidate := filepath.Join(canonicalRoot, filepath.Clean(relative))
	if !isWithin(canonicalRoot, candidate) {
		return "", fmt.Errorf("resolve %q: %w", relative, ErrOutsideRoot)
	}
	canonical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		return "", fmt.Errorf("canonical path: %w", err)
	}
	if !isWithin(canonicalRoot, canonical) {
		return "", fmt.Errorf("resolve %q: %w", relative, ErrOutsideRoot)
	}
	return canonical, nil
}

func canonicalRoot(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("absolute root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonical root: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("stat root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root %q is not a directory", canonical)
	}
	return canonical, nil
}

func isWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
