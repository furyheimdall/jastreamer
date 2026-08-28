package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRootRegistryAcceptsAtMost32CanonicalRoots_belowAllowedBases(t *testing.T) {
	// Given
	base := t.TempDir()
	registry, err := NewRootRegistry([]string{base})
	if err != nil {
		t.Fatal(err)
	}
	for index := range MaximumRootCount {
		path := filepath.Join(base, string(rune('a'+index)))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Add(path, "Music"); err != nil {
			t.Fatalf("add root %d: %v", index, err)
		}
	}
	extra := filepath.Join(base, "extra")
	if err := os.Mkdir(extra, 0o700); err != nil {
		t.Fatal(err)
	}

	// When
	_, err = registry.Add(extra, "Extra")

	// Then
	if !errors.Is(err, ErrTooManyRoots) || len(registry.Roots()) != MaximumRootCount {
		t.Fatalf("root limit = %d, %v", len(registry.Roots()), err)
	}
}

func TestRootRegistryRejectsDuplicateEscapeSymlinkAndUnreadableRoot(t *testing.T) {
	// Given
	base := t.TempDir()
	inside := filepath.Join(base, "inside")
	outside := t.TempDir()
	unreadable := filepath.Join(base, "unreadable")
	for _, path := range []string{inside, unreadable} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(unreadable, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o700) })
	escape := filepath.Join(base, "escape")
	if err := os.Symlink(outside, escape); err != nil {
		t.Fatal(err)
	}
	registry, err := NewRootRegistry([]string{base})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(inside, "Inside"); err != nil {
		t.Fatal(err)
	}

	// When
	_, duplicateErr := registry.Add(filepath.Join(inside, "."), "Duplicate")
	_, escapeErr := registry.Add(escape, "Escape")
	_, unreadableErr := registry.Add(unreadable, "Unreadable")

	// Then
	if !errors.Is(duplicateErr, ErrDuplicateRoot) || !errors.Is(escapeErr, ErrRootOutsideAllowedBase) || !errors.Is(unreadableErr, ErrUnreadableRoot) {
		t.Fatalf("root errors = duplicate %v, escape %v, unreadable %v", duplicateErr, escapeErr, unreadableErr)
	}
}
