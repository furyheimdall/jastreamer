//go:build linux

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
	"golang.org/x/sys/unix"
)

func TestProbe_rejects_executable_and_parent_symlinks(t *testing.T) {
	// Given
	directory := t.TempDir()
	targetDirectory := filepath.Join(directory, "target")
	if err := os.Mkdir(targetDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDirectory, "ffmpeg")
	writeExecutable(t, target, fakeProgramGood)
	leafLink := filepath.Join(directory, "ffmpeg-link")
	if err := os.Symlink(target, leafLink); err != nil {
		t.Fatal(err)
	}
	parentLink := filepath.Join(directory, "parent-link")
	if err := os.Symlink(targetDirectory, parentLink); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{leafLink, filepath.Join(parentLink, "ffmpeg")} {
		// When
		approval := probe(t, path, time.Second)

		// Then
		if approval.Status != StatusUnavailable || approval.ErrorCode != string(executableUnsafeLocation) {
			t.Fatalf("path %q diagnostic = %+v", path, approval.Diagnostic)
		}
	}
}

func TestProvider_launches_approved_bytes_when_parent_is_swapped_after_configuration(t *testing.T) {
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
	path := filepath.Join(parent, "ffmpeg")
	marker := filepath.Join(root, "replacement-executed")
	writeExecutable(t, path, runtimeApprovedProgram)
	writeExecutable(t, filepath.Join(replacementParent, "ffmpeg"), runtimeReplacementProgram)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second, environment: append(os.Environ(), "FFMPEG_REPLACEMENT_MARKER="+marker)})
	if err := os.Rename(parent, filepath.Join(root, "configured-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacementParent, parent); err != nil {
		t.Fatal(err)
	}

	// When
	stream, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)
	if err != nil {
		t.Fatal(err)
	}
	output, readErr := io.ReadAll(stream)
	closeErr := stream.Close()

	// Then
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if string(output) != "approved" {
		t.Fatalf("output = %q", output)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement executable ran after parent swap: %v", err)
	}
}

func TestProbe_releases_bound_descriptor_when_start_fails(t *testing.T) {
	// Given
	path := fakeExecutable(t, "#!/definitely/missing-interpreter\n")
	before := openDescriptorCount(t)

	// When
	approval := probe(t, path, time.Second)
	after := openDescriptorCount(t)

	// Then
	if approval.Status != StatusUnavailable || approval.ErrorCode != "version_failed" {
		t.Fatalf("diagnostic = %+v", approval.Diagnostic)
	}
	if after != before {
		t.Fatalf("probe startup failure leaked descriptors: before=%d after=%d", before, after)
	}
}

func TestProvider_releases_bound_descriptor_after_startup_failure_and_close(t *testing.T) {
	// Given
	path := fakeExecutable(t, "#!/definitely/missing-interpreter\n")
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second})
	descriptor := provider.executable.file.Fd()
	before := openDescriptorCount(t)

	// When
	_, startErr := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)
	after := openDescriptorCount(t)
	closeErr := provider.Close()

	// Then
	var processErr *ProcessError
	if !errors.As(startErr, &processErr) || processErr.Operation != "start" {
		t.Fatalf("startup error = %#v", startErr)
	}
	if after != before {
		t.Fatalf("startup failure leaked descriptors: before=%d after=%d", before, after)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, err := unix.FcntlInt(descriptor, unix.F_GETFD, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("approved executable descriptor remained open: %v", err)
	}
}

func openDescriptorCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}
