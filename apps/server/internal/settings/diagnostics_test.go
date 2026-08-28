package settings_test

import (
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/settings"
)

func TestStore_exposes_admin_warning_without_configured_path_when_FFmpeg_is_unavailable(t *testing.T) {
	// Given
	store, err := settings.Open(fixtureConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	path := "/private/admin/ffmpeg"

	// When
	store.SetFFmpegDiagnostic(settings.FFmpegDiagnostic{ConfiguredPath: path, Status: "unavailable", ErrorCode: "not_found", Warning: "configured executable missing at " + path})
	snapshot := store.Snapshot()

	// Then
	if snapshot.Diagnostics.FFmpeg.Status != "unavailable" || snapshot.Diagnostics.FFmpeg.ErrorCode != "not_found" {
		t.Fatalf("diagnostic = %+v", snapshot.Diagnostics.FFmpeg)
	}
	if strings.Contains(snapshot.Diagnostics.FFmpeg.Warning, path) {
		t.Fatalf("warning exposed path: %q", snapshot.Diagnostics.FFmpeg.Warning)
	}
}
