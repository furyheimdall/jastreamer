package playback

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func Test_Migration_creates_integrity_checked_backup_before_apply(t *testing.T) {
	// Given
	config := testConfig(t)
	db, err := openSQLite(config.Path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.close(); err != nil {
			t.Errorf("close raw database: %v", err)
		}
	})
	interrupted := errors.New("injected interruption")

	// When
	err = (migrationRun{db: db, config: config, hook: func(version int) error {
		if version != CurrentSchemaVersion {
			t.Fatalf("migration version = %d", version)
		}
		return interrupted
	}}).apply(context.Background())

	// Then
	if !errors.Is(err, interrupted) {
		t.Fatalf("migration error = %v", err)
	}
	version, err := schemaVersion(db)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if version != 0 {
		t.Fatalf("interrupted migration changed version to %d", version)
	}
	stmt, err := db.prepare("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='playback_zones'")
	if err != nil {
		t.Fatalf("prepare rollback check: %v", err)
	}
	row, err := stmt.step()
	if err != nil || !row || stmt.int64(0) != 0 {
		stmt.close()
		t.Fatalf("partial DDL survived rollback: row=%v count=%d error=%v", row, stmt.int64(0), err)
	}
	stmt.close()
	backups, err := filepath.Glob(filepath.Join(config.BackupDirectory, "*.sqlite"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup files = %v, error = %v", backups, err)
	}
}

func Test_Backup_restore_recovers_durable_queue(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	_, err := store.Enqueue(context.Background(), EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "saved", Tracks: []QueueTrack{{ID: "saved-track", Available: true}}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	backup := filepath.Join(t.TempDir(), "online.sqlite")
	if err := store.Backup(context.Background(), backup); err != nil {
		t.Fatalf("backup: %v", err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.sqlite")

	// When
	if err := RestoreBackup(context.Background(), RestoreRequest{BackupPath: backup, TargetPath: restoredPath, SupportedSchema: CurrentSchemaVersion}); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restoredConfig := config
	restoredConfig.Path = restoredPath
	restoredConfig.BackupDirectory = filepath.Join(t.TempDir(), "restore-backups")
	restored := openTestStore(t, restoredConfig)
	snapshot, err := restored.Snapshot(context.Background(), "zone-a")
	// Then
	if err != nil {
		t.Fatalf("restored snapshot: %v", err)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].TrackID != "saved-track" {
		t.Fatalf("restored queue = %#v", snapshot.Queue)
	}
}

func Test_Open_rejects_corruption(t *testing.T) {
	// Given
	config := testConfig(t)
	if err := os.WriteFile(config.Path, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("write corruption fixture: %v", err)
	}

	// When
	_, err := Open(context.Background(), config)

	// Then
	if !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("corruption error = %v", err)
	}
}

func Test_OldBinary_rejects_newer_schema(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	if err := store.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	config.SupportedSchema = CurrentSchemaVersion - 1

	// When
	_, err := Open(context.Background(), config)

	// Then
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("old binary error = %v", err)
	}
}

func TestCompatibleNewerSchemaUsesMinimumReaderVersion(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	if err := store.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	setSchemaCompatibility(t, config.Path, CurrentSchemaVersion+1, CurrentSchemaVersion)

	// When
	compatible, err := Open(context.Background(), config)
	// Then
	if err != nil {
		t.Fatalf("open compatible newer schema: %v", err)
	}
	if err := compatible.Close(); err != nil {
		t.Fatalf("close compatible newer schema: %v", err)
	}
}

func TestIncompatibleNewerSchemaRejectsOldReader(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	if err := store.Close(); err != nil {
		t.Fatalf("close current store: %v", err)
	}
	setSchemaCompatibility(t, config.Path, CurrentSchemaVersion+1, CurrentSchemaVersion+1)

	// When
	_, err := Open(context.Background(), config)

	// Then
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("open error = %v, want schema too new", err)
	}
}

func Test_WAL_rejects_unfixed_SQLite_build(t *testing.T) {
	tests := []struct {
		name    string
		mode    JournalMode
		version int
		wantErr bool
	}{
		{"unfixed WAL", JournalWAL, 3_051_002, true},
		{"fixed WAL", JournalWAL, 3_051_003, false},
		{"rollback journal", JournalRollback, 3_040_000, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateJournalVersion(test.mode, test.version)
			if test.wantErr != errors.Is(err, ErrUnsafeWAL) {
				t.Fatalf("version validation error = %v", err)
			}
		})
	}
}

func TestEmbeddedSQLiteIncludesRequiredWALFix(t *testing.T) {
	store := openTestStore(t, testConfig(t))
	if store.SQLiteVersion() < 3_051_003 {
		t.Fatalf("embedded SQLite version = %d, want 3.51.3+", store.SQLiteVersion())
	}
}

func TestOpenRejectsNonLocalDatabasePaths(t *testing.T) {
	for _, path := range []string{"file:https://storage.example/playback.sqlite", "//server/share/playback.sqlite"} {
		t.Run(path, func(t *testing.T) {
			// Given
			config := testConfig(t)
			config.Path = path

			// When
			_, err := Open(context.Background(), config)

			// Then
			if !errors.Is(err, ErrNonLocalDatabase) {
				t.Fatalf("open error = %v, want non-local database", err)
			}
		})
	}
}

func TestRestoreRejectsCorruptBackupWithoutTouchingTarget(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	if _, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "zone-preserve", IdempotencyKey: "enqueue-1",
		Tracks: []QueueTrack{{ID: "track-a", Available: true}},
	}); err != nil {
		t.Fatalf("enqueue target: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close target: %v", err)
	}
	corrupt := filepath.Join(t.TempDir(), "corrupt.sqlite")
	if err := os.WriteFile(corrupt, []byte("not a database"), 0o600); err != nil {
		t.Fatalf("write corrupt backup: %v", err)
	}

	// When
	err := RestoreBackup(context.Background(), RestoreRequest{
		BackupPath: corrupt, TargetPath: config.Path, SupportedSchema: CurrentSchemaVersion,
	})

	// Then
	if !errors.Is(err, ErrCorruptDatabase) {
		t.Fatalf("restore error = %v, want corruption", err)
	}
	reopened, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("reopen preserved target: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close preserved target: %v", err)
		}
	})
	snapshot, err := reopened.Snapshot(context.Background(), "zone-preserve")
	if err != nil {
		t.Fatalf("snapshot preserved target: %v", err)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].TrackID != "track-a" {
		t.Fatalf("restore touched target: %+v", snapshot)
	}
}

func setSchemaCompatibility(t *testing.T, path string, version, minimumReader int) {
	t.Helper()
	db, err := openSQLite(path)
	if err != nil {
		t.Fatalf("open schema fixture: %v", err)
	}
	if err := db.exec(fmt.Sprintf(
		"UPDATE playback_schema SET version=%d,minimum_reader_version=%d WHERE singleton=1; PRAGMA user_version=%d",
		version, minimumReader, version,
	)); err != nil {
		closeErr := db.close()
		t.Fatalf("set schema fixture: %v; close: %v", err, closeErr)
	}
	if err := db.close(); err != nil {
		t.Fatalf("close schema fixture: %v", err)
	}
}
