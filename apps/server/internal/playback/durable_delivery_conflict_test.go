package playback

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func Test_RendererResult_rejects_nested_secret_before_persistence(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	command := fixture.acquireCommand(t, session, fixture.now)
	if err := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), RendererCommandAcknowledgement{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		Sequence: command.Sequence, Status: CommandAckReceived, RecordedAt: fixture.now.Add(time.Second),
	}); err != nil {
		t.Fatalf("record acknowledgement: %v", err)
	}

	// When
	resultErr := fixture.store.RecordRendererTerminalResult(context.Background(), RendererTerminalResult{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		ResultID: "result-secret", Status: "failed", ObservedState: "failed",
		Payload:    json.RawMessage(`{"error":{"details":{"token":"must-not-persist"}}}`),
		RecordedAt: fixture.now.Add(2 * time.Second),
	})
	durable, loadErr := fixture.store.DurableCommand(context.Background(), command.ID)

	// Then
	if !errors.Is(resultErr, ErrSensitivePayload) || loadErr != nil {
		t.Fatalf("secret result/load errors = %v / %v", resultErr, loadErr)
	}
	if durable.ReceiptState != CommandReceiptReceived || string(durable.Result) != "{}" {
		t.Fatalf("secret result persisted: %+v", durable)
	}
}

func Test_Historical_result_on_reconnect_persists_without_blind_resume(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	firstSession := fixture.openSession(t, 0)
	command := fixture.acquireCommand(t, firstSession, fixture.now)
	if err := fixture.store.CloseRendererSession(context.Background(), RendererSessionClose{
		RendererID: fixture.rendererID, Epoch: firstSession.Epoch,
		DisconnectedAt: fixture.now.Add(time.Second),
	}); err != nil {
		t.Fatalf("close ambiguous session: %v", err)
	}
	reconnected := fixture.openSession(t, command.Sequence)
	if err := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), RendererCommandAcknowledgement{
		RendererID: fixture.rendererID, Epoch: reconnected.Epoch, CommandID: command.ID,
		Sequence: command.Sequence, Status: CommandAckDuplicate, RecordedAt: fixture.now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("record replay acknowledgement: %v", err)
	}

	// When
	resultErr := fixture.store.RecordRendererTerminalResult(context.Background(), RendererTerminalResult{
		RendererID: fixture.rendererID, Epoch: reconnected.Epoch, CommandID: command.ID,
		ResultID: "historical-result", Status: "succeeded", ObservedState: "playing",
		Payload: json.RawMessage(`{"positionMs":0}`), Historical: true,
		RecordedAt: fixture.now.Add(3 * time.Second),
	})
	snapshot, snapshotErr := fixture.store.Snapshot(context.Background(), fixture.zoneID)

	// Then
	if resultErr != nil || snapshotErr != nil {
		t.Fatalf("historical result/snapshot errors = %v / %v", resultErr, snapshotErr)
	}
	if snapshot.Transport != TransportSuspended || snapshot.Queue[0].State != QueueReserved {
		t.Fatalf("historical result resumed ambiguous playback: %+v", snapshot)
	}
}

func Test_Duplicate_acknowledgement_after_result_does_not_reopen_terminal_command(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	command := fixture.acquireCommand(t, session, fixture.now)
	ack := RendererCommandAcknowledgement{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		Sequence: command.Sequence, Status: CommandAckReceived, RecordedAt: fixture.now.Add(time.Second),
	}
	if err := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), ack); err != nil {
		t.Fatalf("record received acknowledgement: %v", err)
	}
	if err := fixture.store.RecordRendererTerminalResult(context.Background(), RendererTerminalResult{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		ResultID: "result-terminal", Status: "succeeded", ObservedState: "playing",
		Payload: json.RawMessage(`{"positionMs":0}`), RecordedAt: fixture.now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("record terminal result: %v", err)
	}

	// When
	ack.Status = CommandAckDuplicate
	ack.RecordedAt = fixture.now.Add(3 * time.Second)
	replayErr := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), ack)
	durable, loadErr := fixture.store.DurableCommand(context.Background(), command.ID)

	// Then
	if replayErr != nil || loadErr != nil {
		t.Fatalf("duplicate acknowledgement/load errors = %v / %v", replayErr, loadErr)
	}
	if durable.ReceiptState != CommandReceiptTerminal {
		t.Fatalf("duplicate acknowledgement reopened terminal command: %+v", durable)
	}
}

func Test_RendererResult_and_event_ID_conflicts_are_rejected(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	command := fixture.acquireCommand(t, session, fixture.now)
	if err := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), RendererCommandAcknowledgement{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		Sequence: command.Sequence, Status: CommandAckReceived, RecordedAt: fixture.now.Add(time.Second),
	}); err != nil {
		t.Fatalf("record acknowledgement: %v", err)
	}
	result := RendererTerminalResult{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		ResultID: "result-conflict", Status: "succeeded", ObservedState: "playing",
		Payload: json.RawMessage(`{"positionMs":0}`), RecordedAt: fixture.now.Add(2 * time.Second),
	}
	if err := fixture.store.RecordRendererTerminalResult(context.Background(), result); err != nil {
		t.Fatalf("record first result: %v", err)
	}

	// When
	conflictingResult := result
	conflictingResult.Status = "failed"
	resultErr := fixture.store.RecordRendererTerminalResult(context.Background(), conflictingResult)
	event := RendererPlaybackEvent{
		RendererID: fixture.rendererID, Epoch: session.Epoch, EventID: "event-conflict",
		PlayID: fixture.first.PlayID, Kind: PlaybackEventFailed, ObservedAt: fixture.now.Add(3 * time.Second),
	}
	if _, err := fixture.store.HandleRendererPlaybackEvent(context.Background(), event); err != nil {
		t.Fatalf("record first event: %v", err)
	}
	conflictingEvent := event
	conflictingEvent.Kind = PlaybackEventEnded
	_, eventErr := fixture.store.HandleRendererPlaybackEvent(context.Background(), conflictingEvent)

	// Then
	if !errors.Is(resultErr, ErrCommandResultConflict) || !errors.Is(eventErr, ErrPlaybackEventConflict) {
		t.Fatalf("conflict errors: result=%v event=%v", resultErr, eventErr)
	}
}
