package media

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
)

func TestOpenValidated_rejects_FIFO_replacement_without_blocking_or_leaking(t *testing.T) {
	// Given
	root := t.TempDir()
	path := filepath.Join(root, "track.flac")
	if err := os.WriteFile(path, []byte("catalog"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	identity := fileIdentity{size: info.Size(), modifiedNS: info.ModTime().UnixNano()}
	validatedPath, err := safeRegularPath(root, "track.flac", identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	beforeDescriptors, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	beforeGoroutines := runtime.NumGoroutine()

	// When
	for range 128 {
		file, openErr := openValidated(validatedPath, Claims{FileSize: identity.size, ModifiedNS: identity.modifiedNS})

		// Then
		if file != nil {
			_ = file.Close()
			t.Fatal("FIFO replacement returned a readable file")
		}
		if !errors.Is(openErr, ErrStaleFile) {
			t.Fatalf("open error = %v; want %v", openErr, ErrStaleFile)
		}
	}
	if got := runtime.NumGoroutine(); got != beforeGoroutines {
		t.Fatalf("goroutines after FIFO failures = %d; want %d", got, beforeGoroutines)
	}
	afterDescriptors, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	if len(afterDescriptors) != len(beforeDescriptors) {
		t.Fatalf("descriptors after FIFO failures = %d; want %d", len(afterDescriptors), len(beforeDescriptors))
	}
}
