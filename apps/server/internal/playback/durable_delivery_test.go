package playback

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func Test_DurableCommand_survives_attempt_receipt_result_and_restart(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	if err := store.db.exec(`
		INSERT INTO playback_zones(zone_id,revision) VALUES ('zone-delivery',1);
		INSERT INTO renderer_outbox(command_id,zone_id,play_id,command_type,created_revision)
		VALUES ('command-1','zone-delivery','play-1','play',1);
	`); err != nil {
		t.Fatalf("seed command: %v", err)
	}
	created := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if err := store.BindCommandDelivery(context.Background(), CommandDelivery{
		CommandID: "command-1", RendererID: "renderer-1", Sequence: 17,
		Payload: []byte(`{"media_id":"redacted"}`), CreatedAt: created,
	}); err != nil {
		t.Fatalf("bind delivery: %v", err)
	}
	if err := store.RecordCommandAttempt(context.Background(), CommandAttempt{
		CommandID: "command-1", AttemptedAt: created.Add(time.Second),
	}); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	if err := store.RecordCommandReceipt(context.Background(), CommandReceipt{
		CommandID: "command-1", ReceivedAt: created.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("record receipt: %v", err)
	}

	// When
	if err := store.RecordCommandResult(context.Background(), CommandResult{
		CommandID: "command-1", RendererID: "renderer-1", Sequence: 17,
		Outcome: "succeeded", Result: []byte(`{"position_ms":0}`),
		RecordedAt: created.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("record result: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("restart store: %v", err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted store: %v", err)
		}
	})
	command, err := restarted.DurableCommand(context.Background(), "command-1")
	// Then
	if err != nil {
		t.Fatalf("load durable command: %v", err)
	}
	if command.RendererID != "renderer-1" || command.Sequence != 17 ||
		command.ReceiptState != CommandReceiptTerminal || command.Attempts != 1 {
		t.Fatalf("durable command = %+v", command)
	}
	if string(command.Payload) != `{"media_id":"redacted"}` ||
		string(command.Result) != `{"position_ms":0}` || !command.TerminalAt.Equal(created.Add(3*time.Second)) {
		t.Fatalf("durable payload/result = %+v", command)
	}
}

func Test_BindCommandDelivery_rejects_raw_secret_fields(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	payloads := []string{
		`{"token":"must-not-persist"}`,
		`{"media":{"headers":{"secret":"must-not-persist"}}}`,
	}

	for index, payload := range payloads {
		// When
		err := store.BindCommandDelivery(context.Background(), CommandDelivery{
			CommandID: fmt.Sprintf("command-secret-%d", index), RendererID: "renderer-1",
			Sequence: CommandSequence(index + 1), Payload: []byte(payload), CreatedAt: time.Now().UTC(),
		})

		// Then
		if !errors.Is(err, ErrSensitivePayload) {
			t.Fatalf("secret payload %d error = %v", index, err)
		}
	}
}

func Test_BindCommandDelivery_rejects_conflicting_replay(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	if err := store.db.exec(`
		INSERT INTO playback_zones(zone_id) VALUES ('zone-conflict');
		INSERT INTO renderer_outbox(command_id,zone_id,play_id,command_type,created_revision)
		VALUES ('command-conflict','zone-conflict','play-conflict','play',1);
	`); err != nil {
		t.Fatalf("seed command: %v", err)
	}
	created := time.Now().UTC()
	first := CommandDelivery{
		CommandID: "command-conflict", RendererID: "renderer-1", Sequence: 1,
		Payload: []byte(`{"media_id":"one"}`), CreatedAt: created,
	}
	if err := store.BindCommandDelivery(context.Background(), first); err != nil {
		t.Fatalf("bind first delivery: %v", err)
	}

	// When
	conflict := first
	conflict.Sequence = 2
	err := store.BindCommandDelivery(context.Background(), conflict)

	// Then
	if !errors.Is(err, ErrCommandDeliveryConflict) {
		t.Fatalf("conflicting delivery error = %v", err)
	}
}

func Test_RecordCommandResult_is_idempotent_and_rejects_conflict(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	if err := store.db.exec(`
		INSERT INTO playback_zones(zone_id) VALUES ('zone-result');
		INSERT INTO renderer_outbox(
			command_id,zone_id,play_id,command_type,created_revision,renderer_id,sequence,payload_json,created_at
		) VALUES ('command-result','zone-result','play-result','play',1,'renderer-1',8,'{}','2026-08-25T00:00:00Z');
	`); err != nil {
		t.Fatalf("seed command: %v", err)
	}
	result := CommandResult{
		CommandID: "command-result", RendererID: "renderer-1", Sequence: 8,
		Outcome: "succeeded", Result: []byte(`{"position_ms":0}`),
		RecordedAt: time.Date(2026, 8, 25, 0, 0, 1, 0, time.UTC),
	}
	if err := store.RecordCommandResult(context.Background(), result); err != nil {
		t.Fatalf("record first result: %v", err)
	}

	// When
	replayErr := store.RecordCommandResult(context.Background(), result)
	conflict := result
	conflict.Outcome = "failed"
	conflictErr := store.RecordCommandResult(context.Background(), conflict)

	// Then
	if replayErr != nil {
		t.Fatalf("idempotent result replay: %v", replayErr)
	}
	if !errors.Is(conflictErr, ErrCommandResultConflict) {
		t.Fatalf("conflicting result error = %v", conflictErr)
	}
}

func Test_BoundCommandDelivery_is_immutable(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	if err := store.db.exec(`
		INSERT INTO playback_zones(zone_id) VALUES ('zone-immutable');
		INSERT INTO renderer_outbox(command_id,zone_id,play_id,command_type,created_revision)
		VALUES ('command-immutable','zone-immutable','play-immutable','play',1);
	`); err != nil {
		t.Fatalf("seed command: %v", err)
	}
	bound := CommandDelivery{
		CommandID: "command-immutable", RendererID: "renderer-1", Sequence: 1,
		Payload: []byte(`{"media_id":"one"}`), CreatedAt: time.Now().UTC(),
	}
	if err := store.BindCommandDelivery(context.Background(), bound); err != nil {
		t.Fatalf("bind command: %v", err)
	}

	// When
	err := store.db.exec(`UPDATE renderer_outbox SET payload_json='{"media_id":"two"}'
		WHERE command_id='command-immutable'`)

	// Then
	if err == nil {
		t.Fatal("bound command payload mutation succeeded")
	}
	command, loadErr := store.DurableCommand(context.Background(), "command-immutable")
	if loadErr != nil {
		t.Fatalf("load immutable command: %v", loadErr)
	}
	if string(command.Payload) != `{"media_id":"one"}` {
		t.Fatalf("immutable payload = %s", command.Payload)
	}
}
