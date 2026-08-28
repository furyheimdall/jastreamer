//go:build windows

package transcode

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func TestProbe_blocks_rename_replacement_between_version_and_capability_launches(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg.exe")
	replacement := filepath.Join(directory, "replacement.exe")
	attemptMarker := filepath.Join(directory, "attempted")
	replacementMarker := filepath.Join(directory, "replacement-executed")
	copyWindowsTestExecutable(t, path)
	if err := os.WriteFile(replacement, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(windowsHelperEnabled, "1")
	t.Setenv(windowsHelperMode, "probe_replace")
	t.Setenv("FFMPEG_SELF", path)
	t.Setenv("FFMPEG_REPLACEMENT", replacement)
	t.Setenv("FFMPEG_ATTEMPT_MARKER", attemptMarker)
	t.Setenv("FFMPEG_REPLACEMENT_MARKER", replacementMarker)

	// When
	approval := Probe(t.Context(), path, 5*time.Second)
	t.Cleanup(func() {
		if err := approval.Close(); err != nil {
			t.Error(err)
		}
	})

	// Then
	assertWindowsProbeMutationBlocked(t, approval, windowsProbeMarkers{attempted: attemptMarker, replacement: replacementMarker})
}

func TestProbe_blocks_in_place_write_between_version_and_capability_launches(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg.exe")
	replacement := filepath.Join(directory, "replacement.exe")
	attemptMarker := filepath.Join(directory, "attempted")
	replacementMarker := filepath.Join(directory, "replacement-executed")
	copyWindowsTestExecutable(t, path)
	if err := os.WriteFile(replacement, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(windowsHelperEnabled, "1")
	t.Setenv(windowsHelperMode, "probe_rewrite")
	t.Setenv("FFMPEG_SELF", path)
	t.Setenv("FFMPEG_REPLACEMENT", replacement)
	t.Setenv("FFMPEG_ATTEMPT_MARKER", attemptMarker)
	t.Setenv("FFMPEG_REPLACEMENT_MARKER", replacementMarker)

	// When
	approval := Probe(t.Context(), path, 5*time.Second)
	t.Cleanup(func() {
		if err := approval.Close(); err != nil {
			t.Error(err)
		}
	})

	// Then
	assertWindowsProbeMutationBlocked(t, approval, windowsProbeMarkers{attempted: attemptMarker, replacement: replacementMarker})
}

func TestProbe_blocks_parent_swap_between_version_and_capability_launches(t *testing.T) {
	// Given
	root := t.TempDir()
	parent := filepath.Join(root, "configured")
	replacementParent := filepath.Join(root, "replacement")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(replacementParent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "ffmpeg.exe")
	attemptMarker := filepath.Join(root, "attempted")
	replacementMarker := filepath.Join(root, "replacement-executed")
	copyWindowsTestExecutable(t, path)
	if err := os.WriteFile(filepath.Join(replacementParent, "ffmpeg.exe"), []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(windowsHelperEnabled, "1")
	t.Setenv(windowsHelperMode, "probe_parent")
	t.Setenv("FFMPEG_PARENT", parent)
	t.Setenv("FFMPEG_OLD_PARENT", filepath.Join(root, "configured-old"))
	t.Setenv("FFMPEG_REPLACEMENT_PARENT", replacementParent)
	t.Setenv("FFMPEG_ATTEMPT_MARKER", attemptMarker)
	t.Setenv("FFMPEG_REPLACEMENT_MARKER", replacementMarker)

	// When
	approval := Probe(t.Context(), path, 5*time.Second)
	t.Cleanup(func() {
		if err := approval.Close(); err != nil {
			t.Error(err)
		}
	})

	// Then
	assertWindowsProbeMutationBlocked(t, approval, windowsProbeMarkers{attempted: attemptMarker, replacement: replacementMarker})
}

func TestProbe_rejects_remote_location_without_opening_it(t *testing.T) {
	// Given
	path := `\\server\share\ffmpeg.exe`

	// When
	approval := Probe(t.Context(), path, time.Second)

	// Then
	if approval.Status != StatusUnavailable || approval.ErrorCode != string(executableUnsafeLocation) {
		t.Fatalf("diagnostic = %+v", approval.Diagnostic)
	}
}

func TestProbe_rejects_executable_and_parent_symlinks(t *testing.T) {
	// Given
	directory := t.TempDir()
	targetDirectory := filepath.Join(directory, "target")
	if err := os.Mkdir(targetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDirectory, "ffmpeg.exe")
	copyWindowsTestExecutable(t, target)
	leafLink := filepath.Join(directory, "ffmpeg-link.exe")
	if err := os.Symlink(target, leafLink); err != nil {
		t.Fatal(err)
	}
	parentLink := filepath.Join(directory, "parent-link")
	if err := os.Symlink(targetDirectory, parentLink); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{leafLink, filepath.Join(parentLink, "ffmpeg.exe")} {
		// When
		approval := Probe(t.Context(), path, time.Second)

		// Then
		if approval.Status != StatusUnavailable {
			t.Fatalf("symlink path %q diagnostic = %+v", path, approval.Diagnostic)
		}
		if err := approval.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestProvider_blocks_rename_and_releases_handles_on_close(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg.exe")
	replacement := filepath.Join(directory, "replacement.exe")
	copyWindowsTestExecutable(t, path)
	if err := os.WriteFile(replacement, []byte("replacement"), 0o700); err != nil {
		t.Fatal(err)
	}
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})
	if err := moveFileReplacing(replacement, path); err == nil {
		t.Fatal("replacement succeeded while approved handles were held")
	}

	// When
	closeErr := provider.Close()

	// Then
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := moveFileReplacing(replacement, path); err != nil {
		t.Fatalf("executable handles remained open after close: %v", err)
	}
}

func TestProvider_blocks_in_place_write_while_approved_handles_are_held(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "ffmpeg.exe")
	copyWindowsTestExecutable(t, path)
	newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})

	// When
	writeErr := os.WriteFile(path, []byte("replacement"), 0o700)

	// Then
	if writeErr == nil {
		t.Fatal("in-place write succeeded while approved handles were held")
	}
}

func TestProvider_blocks_parent_swap_while_approved_handles_are_held(t *testing.T) {
	// Given
	root := t.TempDir()
	parent := filepath.Join(root, "configured")
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "ffmpeg.exe")
	copyWindowsTestExecutable(t, path)
	newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})

	// When
	parentErr := moveFile(parent, filepath.Join(root, "configured-old"))

	// Then
	if parentErr == nil {
		t.Fatal("parent swap succeeded while approved handles were held")
	}
}

func TestProvider_returns_start_failure_for_bound_invalid_executable(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "ffmpeg.exe")
	if err := os.WriteFile(path, []byte("not a portable executable"), 0o700); err != nil {
		t.Fatal(err)
	}
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})

	// When
	_, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)

	// Then
	var processErr *ProcessError
	if !errors.As(err, &processErr) || processErr.Operation != "start" {
		t.Fatalf("error = %#v", err)
	}
}

func TestProvider_repeated_concurrent_launches_use_locked_approved_identity(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg.exe")
	copyWindowsTestExecutable(t, path)
	provider := newTestProvider(t, Config{
		Approval: availableApproval(t, path), StartTimeout: 5 * time.Second,
		environment: append(os.Environ(), windowsHelperEnabled+"=1", windowsHelperMode+"=runtime_blocking"),
	})

	for range 16 {
		firstSource, firstRelease := io.Pipe()
		secondSource, secondRelease := io.Pipe()

		// When
		first, err := provider.Open(t.Context(), firstSource, catalog.FormatFLAC)
		if err != nil {
			t.Fatal(err)
		}
		second, err := provider.Open(t.Context(), secondSource, catalog.FormatMP3)
		if err != nil {
			if closeErr := first.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			t.Fatal(err)
		}
		firstOutput := readAndReleaseWindowsStream(t, first, firstRelease)
		secondOutput := readAndReleaseWindowsStream(t, second, secondRelease)

		// Then
		if !bytes.Equal(firstOutput, []byte("approved")) || !bytes.Equal(secondOutput, []byte("approved")) {
			t.Fatalf("outputs = %q, %q", firstOutput, secondOutput)
		}
	}
}

type windowsProbeMarkers struct {
	attempted   string
	replacement string
}

func assertWindowsProbeMutationBlocked(t *testing.T, approval *Approval, markers windowsProbeMarkers) {
	t.Helper()
	if _, err := os.Stat(markers.attempted); err != nil {
		t.Fatalf("mutation was not attempted: %v", err)
	}
	if _, err := os.Stat(markers.replacement); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement succeeded during probe: %v", err)
	}
	if approval.Status != StatusAvailable {
		t.Fatalf("approved executable did not complete probe: %+v", approval.Diagnostic)
	}
}

func readAndReleaseWindowsStream(t *testing.T, stream io.ReadCloser, release *io.PipeWriter) []byte {
	t.Helper()
	output := make([]byte, len("approved"))
	_, readErr := io.ReadFull(stream, output)
	releaseErr := release.Close()
	closeErr := stream.Close()
	if err := errors.Join(readErr, releaseErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return output
}
