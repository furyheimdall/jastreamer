package browseroutput

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

func newHistoryService(t *testing.T) (*errorhistory.Service, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := database.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	history, err := errorhistory.New(t.Context(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	return history, dir
}

func TestAcceptedBrowserErrorRecordedOnceAfterValidation(t *testing.T) {
	history, _ := newHistoryService(t)
	service, err := New(testMedia{}, nil, history)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := service.Register("192.0.2.10", "Phone", []string{"audio/wav"})
	if err != nil {
		t.Fatal(err)
	}
	resource := output.Resource{URL: "http://example.invalid/media", Mime: "audio/wav", Title: "Song", TrackID: "track-1", PlayID: "play-1"}
	commandResult := make(chan error, 1)
	go func() { commandResult <- service.SetURI(t.Context(), registration.ID, resource) }()
	command := waitForCommand(t, service, registration)
	latest := testPlaybackError("command")
	*latest.ErrorCode = 1004
	latest.ErrorName = "ERROR_CODE_FAILED_RUNTIME_CHECK"
	*latest.PositionMS = 41123
	latest.Causes = []PlaybackErrorCause{{Type: "java.lang.IllegalArgumentException", Stack: []string{"androidx.media3.exoplayer.audio.DefaultAudioSink#handleBuffer:939"}}}
	report := Report{Sequence: command.Sequence, Result: "failed", ErrorCode: "media_failed", PlaybackErrors: []PlaybackError{testPlaybackError("command"), latest}}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", report); err != nil {
		t.Fatal(err)
	}
	<-commandResult
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", report); err != nil {
		t.Fatalf("exact retry was not idempotent: %v", err)
	}
	stale := report
	stale.ErrorCode = "media_error"
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", stale); err == nil {
		t.Fatal("altered stale report was accepted")
	}
	result, err := history.List(t.Context(), errorhistory.ListOptions{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Items[0].RendererName != "Phone" || result.Items[0].TrackID != "track-1" || result.Items[0].Protocol != output.ProtocolBrowser {
		t.Fatalf("history=%#v", result)
	}
	if result.Items[0].Code != latest.ErrorName || result.Items[0].PositionMS == nil || *result.Items[0].PositionMS != *latest.PositionMS {
		t.Fatalf("an earlier recovery attempt replaced the terminal error: %#v", result.Items[0])
	}
}

func TestImportNativePlaybackHistoryRotationsIsSafeAndIdempotent(t *testing.T) {
	history, dir := newHistoryService(t)
	logDir := filepath.Join(dir, "logs")
	if err := os.Mkdir(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	playbackErrors, err := json.Marshal([]PlaybackError{testPlaybackError("playback")})
	if err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("2026/09/21 01:02:03.123456 diagnostic component=browseroutput event=native_playback_error renderer_id=%q play_id=%q sequence=%d command_id=%q playback_errors=%q\n", "browser:old-id", "play-old", 41, "browser:old-id/41", string(playbackErrors))
	if err := os.WriteFile(filepath.Join(logDir, "server.log.3"), []byte(strings.Repeat("x", maximumDiagnosticLogLine+1)+"\n"+line), 0o600); err != nil {
		t.Fatal(err)
	}
	malformed := "2026/09/21 01:02:04.000000 diagnostic component=browseroutput event=native_playback_error renderer_id=\"browser:bad\" play_id=\"play\" sequence=1 command_id=\"browser:bad/1\" playback_errors=\"/private/music/song.flac\"\n"
	if err := os.WriteFile(filepath.Join(logDir, "server.log"), []byte(malformed+line), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ImportErrorHistory(context.Background(), history, dir); err != nil {
			t.Fatal(err)
		}
	}
	result, err := history.List(t.Context(), errorhistory.ListOptions{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Items[0].RendererID != "browser:old-id" || result.Items[0].RendererName != "" || !result.Items[0].ReceivedAt.Equal(time.Date(2026, 9, 21, 1, 2, 3, 123456000, time.UTC)) {
		t.Fatalf("imported history=%#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "/private/music") {
		t.Fatal("malformed raw log content escaped into history")
	}
}
