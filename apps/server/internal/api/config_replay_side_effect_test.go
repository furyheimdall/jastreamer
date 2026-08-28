package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/security"
	"github.com/jastreamer/jastreamer-server/internal/settings"
)

func TestConfigHandler_PATCH_catalog_replay_during_active_scan_has_zero_side_effects(t *testing.T) {
	// Given
	directory := t.TempDir()
	media := filepath.Join(directory, "media")
	rootPath := filepath.Join(media, "music")
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		t.Fatal(err)
	}
	store := openReplaySettings(t, directory, media)
	manager, admin := newReplayAdmin(t, directory)
	started := make(chan struct{})
	release := make(chan struct{})
	coordinator, err := catalog.OpenCoordinator(t.Context(), catalog.CoordinatorConfig{
		StatePath:    filepath.Join(directory, "catalog", "coordinator.json"),
		AllowedBases: []string{media},
		Now:          time.Now,
		Scan: func(ctx context.Context, _ catalog.Root, previous catalog.Snapshot) (catalog.ScanResult, error) {
			close(started)
			select {
			case <-ctx.Done():
				return catalog.ScanResult{Snapshot: previous, Complete: false}, ctx.Err()
			case <-release:
				return catalog.ScanResult{Snapshot: previous, Complete: true}, nil
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = coordinator.Close() })
	events := 0
	handler := newConfigHandler(store, manager, func(uint64) { events++ })
	handler.reconcile = func(ctx context.Context, roots []settings.CatalogRoot) error {
		return coordinator.ReconcileRoots(ctx, desiredCatalogRoots(roots))
	}
	body := fmt.Sprintf(`{"catalog_roots":[{"id":"music","display_name":"Music","path":%q}]}`, rootPath)
	firstRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewBufferString(body))
	firstRequest.Header.Set("Authorization", "Bearer "+admin.Token)
	firstRequest.Header.Set("Content-Type", "application/json")
	firstRequest.Header.Set("If-Match", `"0"`)
	firstRequest.Header.Set("Idempotency-Key", "catalog-roots")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, firstRequest)
	if first.Code != http.StatusOK {
		t.Fatalf("first patch = %d %s", first.Code, first.Body.String())
	}
	job, err := coordinator.StartScan(t.Context(), "music")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	case <-started:
	}
	settingsBefore := store.Snapshot()
	rootBefore := coordinator.Roots()
	catalogBefore := coordinator.Snapshot()
	jobBefore, err := coordinator.Job(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(directory, "catalog", "coordinator.json")
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	replayRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewBufferString(body))
	replayRequest.Header = firstRequest.Header.Clone()
	replay := httptest.NewRecorder()

	// When
	handler.ServeHTTP(replay, replayRequest)

	// Then
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	jobAfter, err := coordinator.Job(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Code != first.Code || replay.Header().Get("ETag") != first.Header().Get("ETag") || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay differs: first=%d %q %s replay=%d %q %s", first.Code, first.Header().Get("ETag"), first.Body.String(), replay.Code, replay.Header().Get("ETag"), replay.Body.String())
	}
	if events != 1 || !reflect.DeepEqual(store.Snapshot(), settingsBefore) || !reflect.DeepEqual(coordinator.Roots(), rootBefore) ||
		!reflect.DeepEqual(coordinator.Snapshot(), catalogBefore) || jobAfter != jobBefore || !bytes.Equal(stateAfter, stateBefore) {
		t.Fatalf("replay side effects: events=%d settings=%+v roots=%+v catalog=%+v job=%+v", events, store.Snapshot(), coordinator.Roots(), coordinator.Snapshot(), jobAfter)
	}
	close(release)
	if _, err := coordinator.Wait(t.Context(), job.ID); err != nil {
		t.Fatal(err)
	}
}

func TestConfigHandler_PATCH_non_root_replay_after_restart_suppresses_event(t *testing.T) {
	// Given
	directory := t.TempDir()
	media := filepath.Join(directory, "media")
	if err := os.MkdirAll(media, 0o750); err != nil {
		t.Fatal(err)
	}
	store := openReplaySettings(t, directory, media)
	manager, admin := newReplayAdmin(t, directory)
	events := 0
	handler := newConfigHandler(store, manager, func(uint64) { events++ })
	body := `{"display_name":"Restart-safe replay"}`
	firstRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewBufferString(body))
	firstRequest.Header.Set("Authorization", "Bearer "+admin.Token)
	firstRequest.Header.Set("Content-Type", "application/json")
	firstRequest.Header.Set("If-Match", `"0"`)
	firstRequest.Header.Set("Idempotency-Key", "rename")
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, firstRequest)
	current := store.Snapshot()
	restarted, err := settings.Open(settings.Config{
		Path: filepath.Join(directory, "config", "settings.json"), Defaults: current.Settings, Locks: current.Locks,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler = newConfigHandler(restarted, manager, func(uint64) { events++ })
	replayRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/config", bytes.NewBufferString(body))
	replayRequest.Header = firstRequest.Header.Clone()
	replay := httptest.NewRecorder()

	// When
	handler.ServeHTTP(replay, replayRequest)

	// Then
	if replay.Code != first.Code || replay.Header().Get("ETag") != first.Header().Get("ETag") || replay.Body.String() != first.Body.String() || events != 1 || !reflect.DeepEqual(restarted.Snapshot(), current) {
		t.Fatalf("restart replay: first=%d %q %s replay=%d %q %s events=%d", first.Code, first.Header().Get("ETag"), first.Body.String(), replay.Code, replay.Header().Get("ETag"), replay.Body.String(), events)
	}
}

func openReplaySettings(t *testing.T, directory, media string) *settings.Store {
	t.Helper()
	store, err := settings.Open(settings.Config{
		Path:     filepath.Join(directory, "config", "settings.json"),
		Defaults: settings.Values{DisplayName: "Jake Streamer", PairingTTLSeconds: 300},
		Locks:    settings.Locks{DataDirectory: directory, AllowedCatalogBases: []string{media}, Environment: "test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func newReplayAdmin(t *testing.T, directory string) (*security.Manager, security.Credential) {
	t.Helper()
	manager, err := security.NewManager(security.Config{SetupSecret: "setup", StatePath: filepath.Join(directory, "security", "state.json")})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := manager.Bootstrap(t.Context(), "setup", security.Registration{Name: "Admin"})
	if err != nil {
		t.Fatal(err)
	}
	return manager, admin
}
