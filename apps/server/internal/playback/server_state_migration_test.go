package playback

import (
	"context"
	"errors"
	"os"
	"testing"
)

func Test_Migration_expands_schema_three_without_rewriting_existing_state(t *testing.T) {
	// Given
	config := testConfig(t)
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
		INSERT INTO playback_zones(zone_id,revision,queue_sequence) VALUES ('zone-upgrade',1,1);
		INSERT INTO playback_queue(entry_id,zone_id,position,track_id,available,state,created_revision)
		VALUES ('queue-1','zone-upgrade',1,'track-preserved',1,'pending',1);
	`); err != nil {
		t.Fatalf("seed schema three state: %v", err)
	}
	if err := db.close(); err != nil {
		t.Fatalf("close schema fixture: %v", err)
	}

	// When
	store := openTestStore(t, config)
	snapshot, err := store.Snapshot(context.Background(), "zone-upgrade")
	// Then
	if err != nil {
		t.Fatalf("snapshot upgraded state: %v", err)
	}
	if len(snapshot.Queue) != 1 || snapshot.Queue[0].TrackID != "track-preserved" {
		t.Fatalf("upgraded queue = %+v", snapshot.Queue)
	}
	assertSchemaVersion(t, store.db, CurrentSchemaVersion)
	for _, table := range []string{
		"server_settings", "server_catalog_roots", "server_catalog_scan_jobs",
		"renderer_registry", "renderer_capabilities", "server_zones",
		"renderer_assignments", "media_signing_keys", "server_event_epoch",
		"renderer_command_results", "ffmpeg_probe_status",
	} {
		assertTableExists(t, store.db, table)
	}
}

func Test_Migration_cutpoints_leave_schema_three_database_complete_or_unchanged(t *testing.T) {
	cutpoints := []migrationCutpoint{migrationAfterBackup, migrationAfterExpand, migrationBeforeCommit}
	for _, cutpoint := range cutpoints {
		t.Run(string(cutpoint), func(t *testing.T) {
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
			interrupted := errors.New("injected migration interruption")

			// When
			err = (migrationRun{db: db, config: config, cutpoint: func(reached migrationCutpoint) error {
				if reached == cutpoint {
					return interrupted
				}
				return nil
			}}).apply(context.Background())

			// Then
			if !errors.Is(err, interrupted) {
				t.Fatalf("migration error = %v", err)
			}
			assertSchemaVersion(t, db, 3)
			assertTableMissing(t, db, "renderer_registry")
			assertQueueTrack(t, db, "track-before-crash")
		})
	}
}

func Test_Migration_restart_twice_preserves_expanded_state(t *testing.T) {
	// Given
	config := testConfig(t)
	fixtureConfig := config
	fixtureConfig.Path = config.Path + ".pre-v3-fixture"
	seedSchemaThree(t, fixtureConfig)
	fixture, err := os.ReadFile(fixtureConfig.Path)
	if err != nil {
		t.Fatalf("read pre-v3 fixture: %v", err)
	}
	if err := os.WriteFile(config.Path, fixture, 0o600); err != nil {
		t.Fatalf("copy pre-v3 fixture: %v", err)
	}
	first := openTestStore(t, config)
	if err := first.db.exec(`
		INSERT INTO server_settings(singleton,schema_version,revision,settings_json,updated_at)
		VALUES (1,1,7,'{"display_name":"redacted"}','2026-08-25T00:00:00Z');
		INSERT INTO renderer_registry(renderer_id,kind,display_name,state,revision,created_at,updated_at)
		VALUES ('renderer-redacted','k17','K17','available',4,'2026-08-25T00:00:00Z','2026-08-25T00:00:00Z');
		UPDATE server_event_epoch SET epoch=9,revision=3 WHERE singleton=1;
	`); err != nil {
		t.Fatalf("seed expanded state: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first restart: %v", err)
	}

	// When
	second, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("second restart: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("close second restart: %v", err)
	}
	third, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("third restart: %v", err)
	}
	t.Cleanup(func() {
		if err := third.Close(); err != nil {
			t.Errorf("close third restart: %v", err)
		}
	})

	// Then
	assertScalar(t, third.db, "SELECT revision FROM server_settings WHERE singleton=1", 7)
	assertScalar(t, third.db, "SELECT revision FROM renderer_registry WHERE renderer_id='renderer-redacted'", 4)
	assertScalar(t, third.db, "SELECT epoch FROM server_event_epoch WHERE singleton=1", 9)
	assertQueueTrack(t, third.db, "track-before-crash")
}

func Test_Old_schema_three_reader_rejects_expanded_schema(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	if err := store.Close(); err != nil {
		t.Fatalf("close expanded store: %v", err)
	}
	config.SupportedSchema = 3

	// When
	_, err := Open(context.Background(), config)

	// Then
	if !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("old reader error = %v", err)
	}
}
