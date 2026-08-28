package playback

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func Test_Migration_expands_schema_four_with_renderer_session_state(t *testing.T) {
	// Given
	config := testConfig(t)
	db, err := openSQLite(config.Path)
	if err != nil {
		t.Fatalf("open schema-four fixture: %v", err)
	}
	paths := []string{
		config.MigrationPath,
		config.ExpansionPath,
		filepath.Join(filepath.Dir(config.ExpansionPath), "004_server_state.sql"),
	}
	for _, path := range paths {
		migration, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read migration %s: %v", path, readErr)
		}
		if err := db.exec(string(migration)); err != nil {
			t.Fatalf("apply migration %s: %v", path, err)
		}
	}
	if err := db.exec(`
		INSERT INTO playback_zones(zone_id,revision) VALUES ('zone-four',1);
		INSERT INTO renderer_outbox(command_id,zone_id,play_id,command_type,created_revision)
		VALUES ('command-four','zone-four','play-four','play',1);
	`); err != nil {
		t.Fatalf("seed schema-four outbox: %v", err)
	}
	if err := db.close(); err != nil {
		t.Fatalf("close schema-four fixture: %v", err)
	}

	// When
	store := openTestStore(t, config)
	command, err := store.DurableCommand(context.Background(), "command-four")
	// Then
	if err != nil {
		t.Fatalf("load upgraded command: %v", err)
	}
	if command.MaxAttempts != MaxRendererCommandAttempts || command.ReceiptState != CommandReceiptPending {
		t.Fatalf("upgraded command defaults = %+v", command)
	}
	assertSchemaVersion(t, store.db, CurrentSchemaVersion)
	assertTableExists(t, store.db, "renderer_session_state")
	assertTableExists(t, store.db, "renderer_playback_events")
}

func Test_Acquired_renderer_command_session_identity_is_immutable(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	command := fixture.acquireCommand(t, session, fixture.now)

	// When
	deadlineErr := fixture.store.db.exec(`UPDATE renderer_outbox
		SET deadline='2035-01-01T00:00:00Z' WHERE command_id='` + command.ID + `'`)
	sessionErr := fixture.store.db.exec(`UPDATE renderer_outbox
		SET session_id='different-session' WHERE command_id='` + command.ID + `'`)

	// Then
	if deadlineErr == nil || sessionErr == nil {
		t.Fatalf("immutable session mutation errors = %v / %v", deadlineErr, sessionErr)
	}
}
