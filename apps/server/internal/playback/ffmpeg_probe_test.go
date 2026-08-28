package playback

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFFmpegProbe_persists_unconfigured_probe_with_empty_codec_array(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))

	// When
	err := store.SaveFFmpegProbe(t.Context(), FFmpegProbe{Status: FFmpegUnconfigured, ProbedAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)})
	// Then
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.FFmpegProbe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != FFmpegUnconfigured || len(loaded.Codecs) != 0 {
		t.Fatalf("loaded = %+v", loaded)
	}
}

func TestFFmpegProbe_roundtrips_redacted_diagnostics_and_revision(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	path := "/private/admin/ffmpeg"
	probe := FFmpegProbe{
		ConfiguredPath: path, ExecutableFingerprint: strings.Repeat("a", 64), Status: FFmpegUnavailable,
		Version: "ffmpeg fixture", Codecs: []string{"flac", "mp3"}, ErrorCode: "probe_failed",
		ErrorDetail: "could not start " + path, ProbedAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
	}

	// When
	if err := store.SaveFFmpegProbe(context.Background(), probe); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.FFmpegProbe(context.Background())
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 1 || loaded.ConfiguredPath != path || loaded.ExecutableFingerprint != probe.ExecutableFingerprint {
		t.Fatalf("loaded = %+v", loaded)
	}
	if strings.Contains(loaded.ErrorDetail, path) || loaded.ErrorDetail != "could not start [redacted]" {
		t.Fatalf("detail = %q", loaded.ErrorDetail)
	}
}
