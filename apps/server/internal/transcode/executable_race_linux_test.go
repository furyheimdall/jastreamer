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

func TestProbe_never_launches_in_place_rewrite_after_approved_version_command(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg-fixture")
	replacement := filepath.Join(directory, "replacement")
	marker := filepath.Join(directory, "replacement-executed")
	writeExecutable(t, path, probeRewritingProgram)
	writeExecutable(t, replacement, probeReplacementProgram)
	t.Setenv("FFMPEG_SELF", path)
	t.Setenv("FFMPEG_REPLACEMENT", replacement)
	t.Setenv("FFMPEG_REPLACEMENT_MARKER", marker)

	// When
	approval := probe(t, path, time.Second)

	// Then
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("in-place replacement executable ran during probe: %v", err)
	}
	if approval.Status != StatusAvailable {
		t.Fatalf("approved executable did not complete probe: %+v", approval.Diagnostic)
	}
}

func TestProbe_never_launches_replacement_after_parent_swap(t *testing.T) {
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
	writeExecutable(t, path, probeParentReplacingProgram)
	writeExecutable(t, filepath.Join(replacementParent, "ffmpeg"), probeReplacementProgram)
	t.Setenv("FFMPEG_PARENT", parent)
	t.Setenv("FFMPEG_OLD_PARENT", filepath.Join(root, "configured-old"))
	t.Setenv("FFMPEG_REPLACEMENT_PARENT", replacementParent)
	t.Setenv("FFMPEG_REPLACEMENT_MARKER", marker)

	// When
	approval := probe(t, path, time.Second)

	// Then
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("parent replacement executable ran during probe: %v", err)
	}
	if approval.Status != StatusAvailable {
		t.Fatalf("approved executable did not complete probe: %+v", approval.Diagnostic)
	}
}

func TestProvider_repeated_concurrent_launches_use_approved_identity_after_replacement(t *testing.T) {
	// Given
	directory := t.TempDir()
	path := filepath.Join(directory, "ffmpeg-fixture")
	replacement := filepath.Join(directory, "replacement")
	marker := filepath.Join(directory, "replacement-executed")
	writeExecutable(t, path, runtimeBlockingApprovedProgram)
	writeExecutable(t, replacement, runtimeReplacementProgram)
	provider := newTestProvider(t, Config{Approval: availableApproval(t, path), StartTimeout: time.Second, environment: append(os.Environ(), "FFMPEG_REPLACEMENT_MARKER="+marker)})
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}

	for range 16 {
		// When
		first, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatFLAC)
		if err != nil {
			t.Fatal(err)
		}
		second, err := provider.Open(t.Context(), bytes.NewReader(nil), catalog.FormatMP3)
		if err != nil {
			if closeErr := first.Close(); closeErr != nil {
				t.Error(closeErr)
			}
			t.Fatal(err)
		}
		firstOutput := readExactAndClose(t, first, len("approved"))
		secondOutput := readExactAndClose(t, second, len("approved"))

		// Then
		if string(firstOutput) != "approved" || string(secondOutput) != "approved" {
			t.Fatalf("outputs = %q, %q", firstOutput, secondOutput)
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement executable ran under repeated concurrency: %v", err)
	}
}

func readExactAndClose(t *testing.T, stream io.ReadCloser, size int) []byte {
	t.Helper()
	output := make([]byte, size)
	_, readErr := io.ReadFull(stream, output)
	closeErr := stream.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		t.Fatal(err)
	}
	return output
}

const probeRewritingProgram = `#!/bin/sh
case "$1" in
-version)
  cat "$FFMPEG_REPLACEMENT" >"$FFMPEG_SELF"
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

const probeParentReplacingProgram = `#!/bin/sh
case "$1" in
-version)
  mv "$FFMPEG_PARENT" "$FFMPEG_OLD_PARENT"
  mv "$FFMPEG_REPLACEMENT_PARENT" "$FFMPEG_PARENT"
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

const runtimeBlockingApprovedProgram = `#!/bin/sh
printf 'approved'
while :; do :; done
`
