package errorhistory

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jastreamer/jastreamer-server/internal/database"
)

func openHistory(t *testing.T, path string, notify func(string)) (*Service, func()) {
	t.Helper()
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(context.Background(), db, notify)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	return service, func() { _ = db.Close() }
}

func sampleEvent(key string, at time.Time) Event {
	return Event{
		Key: key, ReceivedAt: at, Kind: "renderer", RendererID: "renderer-1", RendererName: "Living room",
		Protocol: "upnp", TrackID: "track-1", TrackTitle: "Song", Stage: "Play", Code: "transport",
		Message: "The renderer did not confirm Play.", Outcome: "unknown", PlayID: "play-1", CommandID: "command-1",
		Details: json.RawMessage(`{"category":"transport"}`),
	}
}

func TestHistoryPersistsOrdersAndDeduplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	notifications := 0
	service, closeDB := openHistory(t, path, func(topic string) {
		if topic != "history" {
			t.Fatalf("notification topic=%q", topic)
		}
		notifications++
	})
	ctx := context.Background()
	older := sampleEvent("older", time.Date(2026, 9, 20, 1, 0, 0, 0, time.FixedZone("offset", 9*60*60)))
	newer := sampleEvent("newer", time.Date(2026, 9, 20, 0, 30, 0, 0, time.UTC))
	if err := service.Record(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, older); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if notifications != 2 {
		t.Fatalf("notifications=%d want 2", notifications)
	}
	closeDB()

	service, closeDB = openHistory(t, path, nil)
	defer closeDB()
	result, err := service.List(ctx, ListOptions{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || len(result.Items) != 2 {
		t.Fatalf("result=%#v", result)
	}
	if result.Items[0].Key != "" || result.Items[0].ReceivedAt.Format(time.RFC3339) != "2026-09-20T00:30:00Z" || result.Items[1].ReceivedAt.Format(time.RFC3339) != "2026-09-19T16:00:00Z" {
		t.Fatalf("ordering or JSON-only key contract failed: %#v", result.Items)
	}
}

func TestHistoryUsesIDAsReceivedTimeTieBreaker(t *testing.T) {
	service, closeDB := openHistory(t, filepath.Join(t.TempDir(), "state.db"), nil)
	defer closeDB()
	at := time.Now().UTC()
	first := sampleEvent("tie-first", at)
	first.Code = "first"
	second := sampleEvent("tie-second", at)
	second.Code = "second"
	if err := service.Record(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := service.Record(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	result, err := service.List(t.Context(), ListOptions{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 || result.Items[0].Code != "second" || result.Items[1].Code != "first" {
		t.Fatalf("same-time ordering=%#v", result.Items)
	}
}

func TestHistoryRetentionUsesReceivedTime(t *testing.T) {
	service, closeDB := openHistory(t, filepath.Join(t.TempDir(), "state.db"), nil)
	defer closeDB()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for index := range RetentionLimit {
		event := sampleEvent(fmt.Sprintf("event-%d", index), base.Add(time.Duration(index)*time.Second))
		if err := service.Record(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	lateImport := sampleEvent("old-import", base.Add(-time.Hour))
	if err := service.Record(ctx, lateImport); err != nil {
		t.Fatal(err)
	}
	result, err := service.List(ctx, ListOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != RetentionLimit || result.Items[0].Key != "" {
		t.Fatalf("retention result=%#v", result)
	}
	var oldCount int
	if err := service.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM error_history WHERE event_key='old-import'").Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 {
		t.Fatal("an old imported event displaced a newer retained event")
	}
}

func TestHistoryFiltersAndRendererSnapshots(t *testing.T) {
	service, closeDB := openHistory(t, filepath.Join(t.TempDir(), "state.db"), nil)
	defer closeDB()
	ctx := context.Background()
	at := time.Now().UTC()
	first := sampleEvent("first", at)
	second := sampleEvent("second", at.Add(time.Second))
	second.RendererName = "Renamed room"
	integrity := Event{Key: "integrity", ReceivedAt: at.Add(2 * time.Second), Kind: "integrity", TrackID: "track-2", TrackTitle: "Other", RootName: "Music", RelativePath: "album/song.flac", Stage: "decode", Code: "invalid_data", Message: "The audio file could not be decoded.", Outcome: "failed", Details: json.RawMessage(`{"engine":"ffmpeg"}`)}
	for _, event := range []Event{first, second, integrity} {
		if err := service.Record(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	result, err := service.List(ctx, ListOptions{Kind: "renderer", RendererID: "renderer-1", Offset: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || len(result.Items) != 1 || len(result.Renderers) != 1 || result.Renderers[0].Name != "Renamed room" || result.RetentionLimit != RetentionLimit {
		t.Fatalf("filtered result=%#v", result)
	}
}

func TestLongDisplayMetadataDoesNotDiscardAnError(t *testing.T) {
	service, closeDB := openHistory(t, filepath.Join(t.TempDir(), "state.db"), nil)
	defer closeDB()
	event := sampleEvent("long-metadata", time.Now())
	event.RendererName = strings.Repeat("재생 기기 ", 100)
	event.TrackTitle = strings.Repeat("긴 곡 제목 ", 100)
	event.RootName = strings.Repeat("보관함 ", 100)
	if err := service.Record(context.Background(), event); err != nil {
		t.Fatalf("display metadata discarded a renderer error: %v", err)
	}
	page, err := service.List(context.Background(), ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].RendererID != event.RendererID {
		t.Fatalf("renderer attribution was lost: %#v", page)
	}
	for _, text := range []string{page.Items[0].RendererName, page.Items[0].TrackTitle, page.Items[0].RootName} {
		if text == "" || len(text) > maxShortLength || !utf8.ValidString(text) {
			t.Fatalf("display text was not bounded valid UTF-8: %q", text)
		}
	}
}
