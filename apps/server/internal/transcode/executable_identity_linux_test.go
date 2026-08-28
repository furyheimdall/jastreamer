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
)

func TestProbe_never_launches_replacement_after_approved_version_command(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg-fixture")
	replacement := filepath.Join(directory, "replacement")
	marker := filepath.Join(directory, "replacement-executed")
	writeExecutable(t, path, probeReplacingProgram)
	writeExecutable(t, replacement, probeReplacementProgram)
	t.Setenv("FFMPEG_SELF", path)
	t.Setenv("FFMPEG_REPLACEMENT", replacement)
	t.Setenv("FFMPEG_REPLACEMENT_MARKER", marker)

	// When
	diagnostic := probe(t, path, time.Second)

	// Then
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement executable ran during probe: %v", err)
	}
	if diagnostic.Status != StatusAvailable {
		t.Fatalf("approved executable did not complete probe: %+v", diagnostic)
	}
}

func TestProbeExecutable_launches_bound_bytes_when_path_replaced_before_first_command(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg-fixture")
	replacement := filepath.Join(directory, "replacement")
	marker := filepath.Join(directory, "replacement-executed")
	writeExecutable(t, path, fakeProgramGood)
	writeExecutable(t, replacement, probeReplacementProgram)
	t.Setenv("FFMPEG_REPLACEMENT_MARKER", marker)
	executable, err := bindExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := executable.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	// When
	diagnostic := probeExecutable(t.Context(), executable, time.Second)

	// Then
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement executable ran during bound probe: %v", err)
	}
	if diagnostic.Status != StatusAvailable || diagnostic.SHA256 != executable.fingerprint {
		t.Fatalf("approved executable did not complete fingerprint-bound probe: %+v", diagnostic)
	}
}

func TestProvider_launches_approved_bytes_when_path_replaced_after_configuration(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg-fixture")
	replacement := filepath.Join(directory, "replacement")
	marker := filepath.Join(directory, "replacement-executed")
	writeExecutable(t, path, runtimeApprovedProgram)
	writeExecutable(t, replacement, runtimeReplacementProgram)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second, environment: append(os.Environ(), "FFMPEG_REPLACEMENT_MARKER="+marker)})
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	// When
	stream, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)
	if err != nil {
		t.Fatalf("launch approved executable after replacement: %v", err)
	}
	output, readErr := io.ReadAll(stream)
	closeErr := stream.Close()

	// Then
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if string(output) != "approved" {
		t.Fatalf("output = %q, want approved bytes", output)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement executable ran during transcode: %v", err)
	}
}

func writeExecutable(t *testing.T, path, program string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(program), 0o700); err != nil {
		t.Fatal(err)
	}
}

const probeReplacingProgram = `#!/bin/sh
case "$1" in
-version)
  mv "$FFMPEG_REPLACEMENT" "$FFMPEG_SELF"
  printf '%s\n' 'ffmpeg version 6.1.1 Copyright fixture'
  ;;
-hide_banner)
  case "$2" in
  -decoders) printf '%s\n' ' A....D flac' ' A....D mp3' ' A....D vorbis' ' A....D opus' ' A....D pcm_s16le' ;;
  -encoders) printf '%s\n' ' A..... pcm_s16be' ;;
  esac
  ;;
esac
`

const probeReplacementProgram = `#!/bin/sh
printf 'replacement\n' >>"$FFMPEG_REPLACEMENT_MARKER"
case "$1" in
-version) printf '%s\n' 'ffmpeg version 6.1.1 Copyright replacement' ;;
-hide_banner)
  case "$2" in
  -decoders) printf '%s\n' ' A....D flac' ' A....D mp3' ' A....D vorbis' ' A....D opus' ' A....D pcm_s16le' ;;
  -encoders) printf '%s\n' ' A..... pcm_s16be' ;;
  esac
  ;;
esac
`

const runtimeApprovedProgram = `#!/bin/sh
printf 'approved'
`

const runtimeReplacementProgram = `#!/bin/sh
printf 'replacement\n' >>"$FFMPEG_REPLACEMENT_MARKER"
printf 'replacement'
`
