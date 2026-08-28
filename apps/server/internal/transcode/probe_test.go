//go:build !windows

package transcode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProbe_reports_available_when_explicit_executable_has_required_codecs(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramGood)

	// When
	diagnostic := probe(t, path, time.Second)

	// Then
	if diagnostic.Status != StatusAvailable || len(diagnostic.SHA256) != 64 || diagnostic.Version != "6.1.1" {
		t.Fatalf("diagnostic = %+v", diagnostic)
	}
	if !diagnostic.Supports("flac") || !diagnostic.Supports("mp3") || !diagnostic.Supports("vorbis") || !diagnostic.Supports("opus") || !diagnostic.Supports("wav") {
		t.Fatalf("decoder support = %#v", diagnostic.Decoders)
	}
}

func TestProbe_disables_fallback_without_exposing_configured_path_when_executable_is_invalid(t *testing.T) {
	tests := []struct {
		name string
		path func(*testing.T) string
		code string
	}{
		{name: "missing", path: func(t *testing.T) string { return filepath.Join(t.TempDir(), "secret-ffmpeg") }, code: "not_found"},
		{name: "relative", path: func(*testing.T) string { return "ffmpeg" }, code: "path_not_absolute"},
		{name: "not executable", path: func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "private-ffmpeg")
			if err := os.WriteFile(path, []byte("binary"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}, code: "not_executable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			path := test.path(t)

			// When
			diagnostic := probe(t, path, time.Second)

			// Then
			if diagnostic.Status == StatusAvailable || diagnostic.ErrorCode != test.code || strings.Contains(diagnostic.Detail, path) {
				t.Fatalf("diagnostic = %+v", diagnostic)
			}
		})
	}
}

func TestProbe_typed_rejects_malformed_and_unsupported_versions(t *testing.T) {
	tests := []struct {
		name, version, code string
	}{
		{name: "malformed", version: "fixture-1", code: "malformed_version"},
		{name: "too old", version: "5.1.6", code: "unsupported_version"},
		{name: "too new", version: "8.0.0", code: "unsupported_version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			path := fakeExecutable(t, strings.Replace(fakeProgramGood, "6.1.1", test.version, 1))

			// When
			diagnostic := probe(t, path, time.Second)

			// Then
			if diagnostic.Status != StatusIncompatible || diagnostic.ErrorCode != test.code || strings.Contains(diagnostic.Detail, test.version) {
				t.Fatalf("diagnostic = %+v", diagnostic)
			}
			var versionErr *VersionError
			if _, err := parseFFmpegVersion("ffmpeg version " + test.version); !errors.As(err, &versionErr) {
				t.Fatalf("version error = %#v", err)
			}
		})
	}
}

func TestProbe_accepts_explicit_supported_version_range(t *testing.T) {
	for _, version := range []string{"6.0.0", "6.1.1-3ubuntu5", "7.1.2"} {
		t.Run(version, func(t *testing.T) {
			// Given
			path := fakeExecutable(t, strings.Replace(fakeProgramGood, "6.1.1", version, 1))

			// When
			diagnostic := probe(t, path, time.Second)

			// Then
			if diagnostic.Status != StatusAvailable {
				t.Fatalf("diagnostic = %+v", diagnostic)
			}
		})
	}
}

func TestProbe_rejects_missing_decoder_and_hung_probe(t *testing.T) {
	tests := []struct {
		name, program, code string
	}{
		{name: "decoder", program: fakeProgramMissingOpus, code: "missing_decoder"},
		{name: "hung", program: fakeProgramHung, code: "probe_timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			path := fakeExecutable(t, test.program)

			// When
			diagnostic := probe(t, path, 100*time.Millisecond)

			// Then
			if diagnostic.Status != StatusIncompatible && diagnostic.Status != StatusUnavailable {
				t.Fatalf("status = %q", diagnostic.Status)
			}
			if diagnostic.ErrorCode != test.code || strings.Contains(diagnostic.Detail, path) {
				t.Fatalf("diagnostic = %+v", diagnostic)
			}
		})
	}
}

func TestProbe_detects_executable_hash_change(t *testing.T) {
	// Given
	path := fakeExecutable(t, fakeProgramGood)
	first := probe(t, path, time.Second)
	if first.Status != StatusAvailable {
		t.Fatalf("first = %+v", first)
	}
	if err := os.WriteFile(path, []byte(fakeProgramMissingOpus), 0o700); err != nil {
		t.Fatal(err)
	}

	// When
	second := probe(t, path, time.Second)

	// Then
	if second.SHA256 == first.SHA256 || second.Status != StatusIncompatible {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestNewProvider_requires_successful_probe(t *testing.T) {
	// Given / When
	_, err := NewProvider(Config{Approval: newApproval(Diagnostic{Status: StatusUnavailable}, nil)})

	// Then
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func probe(t *testing.T, path string, timeout time.Duration) *Approval {
	t.Helper()
	approval := Probe(t.Context(), path, timeout)
	t.Cleanup(func() {
		if err := approval.Close(); err != nil {
			t.Error(err)
		}
	})
	return approval
}

func fakeExecutable(t *testing.T, program string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ffmpeg-fixture")
	if err := os.WriteFile(path, []byte(program), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

const fakeProgramGood = `#!/bin/sh
case "$1" in
-version) printf '%s\n' 'ffmpeg version 6.1.1 Copyright fixture' ;;
-hide_banner)
  case "$2" in
  -decoders) printf '%s\n' ' A....D flac' ' A....D mp3' ' A....D vorbis' ' A....D opus' ' A....D pcm_s16le' ;;
  -encoders) printf '%s\n' ' A..... pcm_s16be' ;;
  esac ;;
esac
`

const fakeProgramMissingOpus = `#!/bin/sh
case "$1" in
-version) printf '%s\n' 'ffmpeg version 6.1.1 Copyright fixture' ;;
-hide_banner)
  case "$2" in
  -decoders) printf '%s\n' ' A....D flac' ' A....D mp3' ' A....D vorbis' ' A....D pcm_s16le' ;;
  -encoders) printf '%s\n' ' A..... pcm_s16be' ;;
  esac ;;
esac
`

const fakeProgramHung = `#!/bin/sh
while :; do :; done
`
