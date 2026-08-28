package playback

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func seedSchemaThree(t *testing.T, config Config) {
	t.Helper()
	db, err := openSQLite(config.Path)
	if err != nil {
		t.Fatalf("open schema fixture: %v", err)
	}
	base, err := os.ReadFile(config.MigrationPath)
	if err != nil {
		t.Fatalf("read base migration: %v", err)
	}
	expansion, err := os.ReadFile(config.ExpansionPath)
	if err != nil {
		t.Fatalf("read schema three migration: %v", err)
	}
	if err := db.exec(string(base) + "\n" + string(expansion)); err != nil {
		t.Fatalf("apply schema three: %v", err)
	}
	if err := db.exec(`
		INSERT INTO playback_zones(zone_id,revision,queue_sequence) VALUES ('zone-before-crash',1,1);
		INSERT INTO playback_queue(entry_id,zone_id,position,track_id,available,state,created_revision)
		VALUES ('queue-before-crash','zone-before-crash',1,'track-before-crash',1,'pending',1);
	`); err != nil {
		t.Fatalf("seed schema three: %v", err)
	}
	if err := db.close(); err != nil {
		t.Fatalf("close schema fixture: %v", err)
	}
}

func assertSchemaVersion(t *testing.T, db *sqliteDB, want int) {
	t.Helper()
	got, err := schemaVersion(db)
	if err != nil {
		t.Fatalf("schema version: %v", err)
	}
	if got != want {
		t.Fatalf("schema version = %d, want %d", got, want)
	}
}

func assertTableExists(t *testing.T, db *sqliteDB, table string) {
	t.Helper()
	assertTableCount(t, db, table, 1)
}

func assertTableMissing(t *testing.T, db *sqliteDB, table string) {
	t.Helper()
	assertTableCount(t, db, table, 0)
}

func assertTableCount(t *testing.T, db *sqliteDB, table string, want int64) {
	t.Helper()
	stmt, err := db.prepare("SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?")
	if err != nil {
		t.Fatalf("inspect table %s: %v", table, err)
	}
	defer stmt.close()
	if err := stmt.bindText(1, table); err != nil {
		t.Fatalf("bind table %s: %v", table, err)
	}
	row, err := stmt.step()
	if err != nil || !row || stmt.int64(0) != want {
		t.Fatalf("table %s count = %d, row=%v error=%v", table, stmt.int64(0), row, err)
	}
}

func assertQueueTrack(t *testing.T, db *sqliteDB, track string) {
	t.Helper()
	stmt, err := db.prepare("SELECT count(*) FROM playback_queue WHERE track_id=?")
	if err != nil {
		t.Fatalf("inspect queue: %v", err)
	}
	defer stmt.close()
	if err := stmt.bindText(1, track); err != nil {
		t.Fatalf("bind queue track: %v", err)
	}
	row, err := stmt.step()
	if err != nil || !row || stmt.int64(0) != 1 {
		t.Fatalf("queue track %q missing: row=%v count=%d error=%v", track, row, stmt.int64(0), err)
	}
}

func assertScalar(t *testing.T, db *sqliteDB, query string, want int64) {
	t.Helper()
	stmt, err := db.prepare(query)
	if err != nil {
		t.Fatalf("prepare scalar: %v", err)
	}
	defer stmt.close()
	row, err := stmt.step()
	if err != nil || !row || stmt.int64(0) != want {
		t.Fatalf("scalar = %d, want %d, row=%v error=%v", stmt.int64(0), want, row, err)
	}
}

func Test_Migration_corrupt_staged_backup_does_not_change_original(t *testing.T) {
	// Given
	config := testConfig(t)
	seedSchemaThree(t, config)
	db, err := openSQLite(config.Path)
	if err != nil {
		t.Fatalf("open schema fixture: %v", err)
	}
	t.Cleanup(func() {
		if err := db.close(); err != nil {
			t.Errorf("close schema fixture: %v", err)
		}
	})
	interrupted := errors.New("after backup")

	// When
	err = (migrationRun{db: db, config: config, cutpoint: func(reached migrationCutpoint) error {
		if reached != migrationAfterBackup {
			return nil
		}
		backups, globErr := filepath.Glob(filepath.Join(config.BackupDirectory, "*.sqlite"))
		if globErr != nil || len(backups) != 1 {
			return errors.Join(interrupted, globErr)
		}
		if writeErr := os.WriteFile(backups[0], []byte("corrupt staged copy"), 0o600); writeErr != nil {
			return errors.Join(interrupted, writeErr)
		}
		return interrupted
	}}).apply(context.Background())

	// Then
	if !errors.Is(err, interrupted) {
		t.Fatalf("migration error = %v", err)
	}
	backups, globErr := filepath.Glob(filepath.Join(config.BackupDirectory, "*.sqlite"))
	if globErr != nil || len(backups) != 1 {
		t.Fatalf("migration backups = %v, error=%v", backups, globErr)
	}
	assertSchemaVersion(t, db, 3)
	assertQueueTrack(t, db, "track-before-crash")
}
