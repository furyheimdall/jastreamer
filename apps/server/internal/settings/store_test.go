package settings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/settings"
)

func fixtureConfig(t *testing.T) settings.Config {
	t.Helper()
	dataDirectory := t.TempDir()
	mediaBase := filepath.Join(dataDirectory, "media")
	musicRoot := filepath.Join(mediaBase, "music")
	if err := os.MkdirAll(musicRoot, 0o750); err != nil {
		t.Fatalf("create media fixture: %v", err)
	}
	return settings.Config{
		Path: filepath.Join(dataDirectory, "config", "settings.json"),
		Defaults: settings.Values{
			DisplayName:       "Jake Streamer",
			CatalogRoots:      []settings.CatalogRoot{{ID: "music", DisplayName: "Music", Path: musicRoot}},
			PairingTTLSeconds: 300,
		},
		Locks: settings.Locks{
			ListenAddress: ":8443", CertificateFingerprint: "redacted-fingerprint",
			CertificateSANs: []string{"localhost", "127.0.0.1"}, DataDirectory: dataDirectory,
			AllowedCatalogBases: []string{mediaBase}, Environment: "test",
		},
	}
}

func TestStore_patch_persists_monotonic_revision_and_previous_valid_backup(t *testing.T) {
	// Given
	config := fixtureConfig(t)
	store, err := settings.Open(config)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	firstName := "Living Room Server"
	first, err := store.Patch(context.Background(), settings.Mutation{
		ExpectedRevision: 0, IdempotencyKey: "rename-1", Update: settings.Update{DisplayName: &firstName},
	})
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}
	secondName := "Whole Home Server"

	// When
	second, err := store.Patch(context.Background(), settings.Mutation{
		ExpectedRevision: first.Revision, IdempotencyKey: "rename-2", Update: settings.Update{DisplayName: &secondName},
	})
	if err != nil {
		t.Fatalf("second patch: %v", err)
	}
	restarted, err := settings.Open(config)
	if err != nil {
		t.Fatalf("restart settings: %v", err)
	}
	backup, err := settings.LoadDocument(config.Path + ".bak")
	// Then
	if err != nil {
		t.Fatalf("load backup: %v", err)
	}
	if second.Revision != 2 || restarted.Snapshot().Revision != 2 || restarted.Snapshot().Settings.DisplayName != secondName {
		t.Fatalf("active snapshots = %+v / %+v", second, restarted.Snapshot())
	}
	if backup.Revision != 1 || backup.Settings.DisplayName != firstName {
		t.Fatalf("backup = %+v", backup)
	}
}

func TestStore_open_recovers_previous_settings_after_interrupted_corrupt_primary(t *testing.T) {
	// Given
	config := fixtureConfig(t)
	store, err := settings.Open(config)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	name := "Valid Before Crash"
	if _, err := store.Patch(context.Background(), settings.Mutation{ExpectedRevision: 0, Update: settings.Update{DisplayName: &name}}); err != nil {
		t.Fatalf("patch: %v", err)
	}
	if err := os.WriteFile(config.Path, []byte(`{"schema_version":1,"revision":2`), 0o600); err != nil {
		t.Fatalf("simulate interrupted primary: %v", err)
	}

	// When
	recovered, err := settings.Open(config)
	// Then
	if err != nil {
		t.Fatalf("recover settings: %v", err)
	}
	if recovered.Snapshot().Revision != 0 || recovered.Snapshot().Settings.DisplayName != "Jake Streamer" {
		t.Fatalf("recovered snapshot = %+v", recovered.Snapshot())
	}
}

func TestStore_patch_interruption_before_replace_preserves_active_document(t *testing.T) {
	// Given
	config := fixtureConfig(t)
	store, err := settings.Open(config)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	if err := os.Mkdir(config.Path+".tmp", 0o700); err != nil {
		t.Fatalf("block staged replace: %v", err)
	}
	name := "Must Not Activate"

	// When
	_, patchErr := store.Patch(context.Background(), settings.Mutation{
		ExpectedRevision: 0, Update: settings.Update{DisplayName: &name},
	})
	if err := os.Remove(config.Path + ".tmp"); err != nil {
		t.Fatalf("remove interruption fixture: %v", err)
	}
	restarted, restartErr := settings.Open(config)

	// Then
	if patchErr == nil {
		t.Fatal("patch succeeded despite blocked atomic stage")
	}
	if restartErr != nil {
		t.Fatalf("restart after interruption: %v", restartErr)
	}
	if got := restarted.Snapshot(); got.Revision != 0 || got.Settings.DisplayName != "Jake Streamer" {
		t.Fatalf("interrupted patch activated: %+v", got)
	}
}

func TestStore_patch_replay_returns_exact_original_snapshot_after_later_mutation(t *testing.T) {
	// Given
	store, err := settings.Open(fixtureConfig(t))
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	firstName := "First Name"
	firstMutation := settings.Mutation{ExpectedRevision: 0, IdempotencyKey: "first", Update: settings.Update{DisplayName: &firstName}}
	first, err := store.Patch(context.Background(), firstMutation)
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}
	secondName := "Second Name"
	if _, err := store.Patch(context.Background(), settings.Mutation{
		ExpectedRevision: 1, IdempotencyKey: "second", Update: settings.Update{DisplayName: &secondName},
	}); err != nil {
		t.Fatalf("second patch: %v", err)
	}
	current := store.Snapshot()
	restarted, err := settings.Open(settings.Config{
		Path: filepath.Join(current.Locks.DataDirectory, "config", "settings.json"), Defaults: current.Settings, Locks: current.Locks,
	})
	if err != nil {
		t.Fatalf("restart settings: %v", err)
	}

	// When
	replayed, replayErr := restarted.Patch(context.Background(), firstMutation)

	// Then
	if replayErr != nil || !reflect.DeepEqual(replayed, first) {
		t.Fatalf("replay = %+v, %v; original = %+v", replayed, replayErr, first)
	}
}

func TestStore_patch_is_idempotent_and_rejects_stale_revision_without_mutation(t *testing.T) {
	// Given
	store, err := settings.Open(fixtureConfig(t))
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}
	name := "Idempotent Name"
	mutation := settings.Mutation{ExpectedRevision: 0, IdempotencyKey: "same-operation", Update: settings.Update{DisplayName: &name}}
	first, err := store.Patch(context.Background(), mutation)
	if err != nil {
		t.Fatalf("first patch: %v", err)
	}

	// When
	replayed, replayErr := store.Patch(context.Background(), mutation)
	other := "Stale Name"
	_, staleErr := store.Patch(context.Background(), settings.Mutation{ExpectedRevision: 0, Update: settings.Update{DisplayName: &other}})

	// Then
	if replayErr != nil || replayed.Revision != first.Revision {
		t.Fatalf("replay = %+v, %v", replayed, replayErr)
	}
	if !errors.Is(staleErr, settings.ErrRevisionMismatch) {
		t.Fatalf("stale error = %v", staleErr)
	}
	if got := store.Snapshot(); got.Revision != 1 || got.Settings.DisplayName != name {
		t.Fatalf("snapshot after stale patch = %+v", got)
	}
}
