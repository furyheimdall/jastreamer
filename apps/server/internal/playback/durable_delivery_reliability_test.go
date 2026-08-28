package playback

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func Test_RendererCommand_retry_budget_is_bounded_and_persisted(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	var command DurableCommand
	for attempt := range MaxRendererCommandAttempts {
		command = fixture.acquireCommand(t, session, fixture.now.Add(time.Duration(attempt)*time.Second))
	}

	// When
	_, retryErr := fixture.store.AcquireRendererCommand(context.Background(), RendererCommandRequest{
		RendererID: fixture.rendererID, Epoch: session.Epoch,
		AttemptedAt: fixture.now.Add(MaxRendererCommandAttempts * time.Second),
		Deadline:    fixture.now.Add(10 * time.Minute),
	})
	durable, loadErr := fixture.store.DurableCommand(context.Background(), command.ID)
	snapshot, snapshotErr := fixture.store.Snapshot(context.Background(), fixture.zoneID)

	// Then
	if !errors.Is(retryErr, ErrCommandRetryExhausted) || loadErr != nil || snapshotErr != nil {
		t.Fatalf("retry/load/snapshot errors = %v / %v / %v", retryErr, loadErr, snapshotErr)
	}
	if durable.Attempts != MaxRendererCommandAttempts || durable.LastErrorCode != "RETRY_EXHAUSTED" ||
		durable.NextAttemptAt.IsZero() || snapshot.Transport != TransportSuspended {
		t.Fatalf("bounded retry state = command=%+v snapshot=%+v", durable, snapshot)
	}
}

func Test_RendererCommand_rejected_acknowledgement_cannot_be_rewritten_as_received(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	command := fixture.acquireCommand(t, session, fixture.now)
	rejected := RendererCommandAcknowledgement{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		Sequence: command.Sequence, Status: CommandAckRejected,
		Error: json.RawMessage(`{"code":"OUTPUT_UNAVAILABLE"}`), RecordedAt: fixture.now.Add(time.Second),
	}
	if err := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), rejected); err != nil {
		t.Fatalf("record rejected acknowledgement: %v", err)
	}

	// When
	replayErr := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), rejected)
	conflict := rejected
	conflict.Status = CommandAckReceived
	conflict.Error = json.RawMessage("null")
	conflictErr := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), conflict)

	// Then
	if replayErr != nil || !errors.Is(conflictErr, ErrCommandDeliveryConflict) {
		t.Fatalf("rejected acknowledgement replay/conflict = %v / %v", replayErr, conflictErr)
	}
}

func Test_RendererPlaybackEvent_ended_before_terminal_result_does_not_advance(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	command := fixture.acquireCommand(t, session, fixture.now)
	if err := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), RendererCommandAcknowledgement{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		Sequence: command.Sequence, Status: CommandAckReceived, RecordedAt: fixture.now.Add(time.Second),
	}); err != nil {
		t.Fatalf("record received acknowledgement: %v", err)
	}

	// When
	_, eventErr := fixture.store.HandleRendererPlaybackEvent(context.Background(), RendererPlaybackEvent{
		RendererID: fixture.rendererID, Epoch: session.Epoch, EventID: "premature-ended",
		PlayID: fixture.first.PlayID, Kind: PlaybackEventEnded, ObservedAt: fixture.now.Add(2 * time.Second),
	})
	snapshot, snapshotErr := fixture.store.Snapshot(context.Background(), fixture.zoneID)

	// Then
	if !errors.Is(eventErr, ErrInvalidObservation) || snapshotErr != nil {
		t.Fatalf("premature end/snapshot errors = %v / %v", eventErr, snapshotErr)
	}
	if snapshot.Queue[0].State != QueueReserved || snapshot.Queue[1].State != QueuePending ||
		snapshot.CurrentPlay != fixture.first.PlayID {
		t.Fatalf("premature end advanced queue: %+v", snapshot)
	}
}

func Test_RendererResult_preserves_unknown_status_and_suspends_instead_of_guessing(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	command := fixture.acquireCommand(t, session, fixture.now)
	if err := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), RendererCommandAcknowledgement{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		Sequence: command.Sequence, Status: CommandAckReceived, RecordedAt: fixture.now.Add(time.Second),
	}); err != nil {
		t.Fatalf("record command acknowledgement: %v", err)
	}

	// When
	err := fixture.store.RecordRendererTerminalResult(context.Background(), RendererTerminalResult{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		ResultID: "result-future", Status: "future-terminal", ObservedState: "future-state",
		Payload: json.RawMessage(`{"status":"future-terminal"}`), RecordedAt: fixture.now.Add(2 * time.Second),
	})
	truth, truthErr := fixture.store.RendererSessionTruth(context.Background(), fixture.rendererID)

	// Then
	if err != nil || truthErr != nil {
		t.Fatalf("unknown result/truth errors = %v / %v", err, truthErr)
	}
	if truth.ObservedState != "future-state" || truth.IntentTransport != TransportSuspended {
		t.Fatalf("unknown result was discarded or guessed: %+v", truth)
	}
}

func Test_Stop_supersedes_pending_pause_before_renderer_delivery(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	if _, err := fixture.store.ConfirmStart(context.Background(), fixture.zoneID, fixture.first.PlayID); err != nil {
		t.Fatalf("confirm fixture start: %v", err)
	}
	if err := fixture.store.Pause(context.Background(), fixture.zoneID); err != nil {
		t.Fatalf("commit pending pause: %v", err)
	}
	if err := fixture.store.Stop(context.Background(), fixture.zoneID); err != nil {
		t.Fatalf("commit stop after pause: %v", err)
	}
	session := fixture.openSession(t, 0)

	// When
	command := fixture.acquireCommand(t, session, fixture.now)

	// Then
	if command.Type != "stop" {
		t.Fatalf("stale control preceded stop: %+v", command)
	}
}

func Test_Superseded_play_result_cannot_resurrect_stopped_intent(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	play := fixture.acquireCommand(t, session, fixture.now)
	if err := fixture.store.RecordRendererCommandAcknowledgement(context.Background(), RendererCommandAcknowledgement{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: play.ID,
		Sequence: play.Sequence, Status: CommandAckReceived, RecordedAt: fixture.now.Add(time.Second),
	}); err != nil {
		t.Fatalf("record play acknowledgement: %v", err)
	}
	if err := fixture.store.Stop(context.Background(), fixture.zoneID); err != nil {
		t.Fatalf("commit stop intent: %v", err)
	}

	// When
	resultErr := fixture.store.RecordRendererTerminalResult(context.Background(), RendererTerminalResult{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: play.ID,
		ResultID: "late-play-result", Status: "succeeded", ObservedState: "playing",
		Payload: json.RawMessage(`{"positionMs":0}`), RecordedAt: fixture.now.Add(2 * time.Second),
	})
	truth, truthErr := fixture.store.RendererSessionTruth(context.Background(), fixture.rendererID)
	stop := fixture.acquireCommand(t, session, fixture.now.Add(3*time.Second))

	// Then
	if resultErr != nil || truthErr != nil {
		t.Fatalf("late result/truth errors = %v / %v", resultErr, truthErr)
	}
	if truth.IntentTransport != TransportIdle || truth.IntentPlayID != "" || truth.ObservedState != "playing" ||
		stop.Type != "stop" || stop.Sequence != play.Sequence+1 {
		t.Fatalf("late result changed intent or lost stop: truth=%+v stop=%+v", truth, stop)
	}
}

func Test_RendererSession_restart_redelivers_same_command_under_new_epoch(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	firstSession := fixture.openSession(t, 0)
	firstCommand := fixture.acquireCommand(t, firstSession, fixture.now)
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close store before restart: %v", err)
	}
	restarted, err := Open(context.Background(), fixture.config)
	if err != nil {
		t.Fatalf("restart durable store: %v", err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted store: %v", err)
		}
	})
	fixture.store = restarted
	recovered, recoveryErr := restarted.RendererSessionTruth(context.Background(), fixture.rendererID)
	if recoveryErr != nil {
		t.Fatalf("load startup recovery truth: %v", recoveryErr)
	}

	// When
	reconnected := fixture.openSession(t, firstCommand.Sequence)
	replayed := fixture.acquireCommand(t, reconnected, fixture.now.Add(time.Second))
	truth, truthErr := restarted.RendererSessionTruth(context.Background(), fixture.rendererID)

	// Then
	if truthErr != nil {
		t.Fatalf("load restarted renderer truth: %v", truthErr)
	}
	if recovered.ConnectionState != "disconnected" || recovered.IntentTransport != TransportSuspended {
		t.Fatalf("startup recovery guessed physical state: %+v", recovered)
	}
	if reconnected.Epoch == firstSession.Epoch || replayed.ID != firstCommand.ID ||
		replayed.Sequence != firstCommand.Sequence || replayed.Attempts != firstCommand.Attempts+1 ||
		truth.IntentTransport != TransportSuspended {
		t.Fatalf("restart redelivery = session=%+v command=%+v truth=%+v", reconnected, replayed, truth)
	}
}

func Test_RendererSession_rejects_cursor_ahead_of_durable_sequence(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)

	// When
	_, err := fixture.store.OpenRendererSession(context.Background(), RendererSessionRequest{
		RendererID: fixture.rendererID, LastServerSequence: 99, ConnectedAt: fixture.now,
	})

	// Then
	if !errors.Is(err, ErrCommandSequenceGap) {
		t.Fatalf("future renderer cursor error = %v", err)
	}
}
