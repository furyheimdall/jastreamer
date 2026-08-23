package playback

import (
	"context"
	"testing"
)

func Test_Stop_commits_renderer_command_with_session_end(t *testing.T) {
	// Given
	store, _ := queuedStore(t)
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-a", decision.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// When
	if err := store.Stop(context.Background(), "zone-a"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	commands, err := store.PendingOutbox(context.Background(), "zone-a")
	// Then
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if len(commands) != 1 || commands[0].Type != "stop" || commands[0].PlayID != decision.PlayID {
		t.Fatalf("stop outbox = %#v", commands)
	}
}

func TestStopOutboxAcknowledgementSurvivesRestart(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	enqueueAvailable(t, store, "zone-stop", "track-a")
	decision, err := store.ReserveNext(context.Background(), "zone-stop", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-stop", decision.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := store.Stop(context.Background(), "zone-stop"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	commands, err := store.PendingOutbox(context.Background(), "zone-stop")
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("pending commands = %+v, want one stop", commands)
	}

	// When
	if err := store.AcknowledgeOutbox(context.Background(), "zone-stop", commands[0].ID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("close reopened: %v", err)
		}
	})

	// Then
	pending, err := reopened.PendingOutbox(context.Background(), "zone-stop")
	if err != nil {
		t.Fatalf("pending after restart: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("acknowledged command reappeared: %+v", pending)
	}
	before, err := reopened.Snapshot(context.Background(), "zone-stop")
	if err != nil {
		t.Fatalf("before repeated stop: %v", err)
	}
	if err := reopened.Stop(context.Background(), "zone-stop"); err != nil {
		t.Fatalf("repeated stop: %v", err)
	}
	after, err := reopened.Snapshot(context.Background(), "zone-stop")
	if err != nil {
		t.Fatalf("after repeated stop: %v", err)
	}
	if after.Revision != before.Revision {
		t.Fatalf("repeated stop changed revision: before=%d after=%d", before.Revision, after.Revision)
	}
}

func TestPauseAndResumeCommitRendererCommandsWithoutQueueAdvance(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	enqueueAvailable(t, store, "zone-controls", "track-a")
	decision, err := store.ReserveNext(context.Background(), "zone-controls", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-controls", decision.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	// When
	if err := store.Pause(context.Background(), "zone-controls"); err != nil {
		t.Fatalf("pause: %v", err)
	}

	// Then
	paused, err := store.Snapshot(context.Background(), "zone-controls")
	if err != nil {
		t.Fatalf("paused snapshot: %v", err)
	}
	commands, err := store.PendingOutbox(context.Background(), "zone-controls")
	if err != nil {
		t.Fatalf("pause outbox: %v", err)
	}
	if paused.Transport != TransportPaused || len(paused.Queue) != 1 || paused.Queue[0].State != QueuePlaying ||
		len(commands) != 1 || commands[0].Type != "pause" {
		t.Fatalf("pause state/outbox = %+v / %+v", paused, commands)
	}
	if err := store.AcknowledgeOutbox(context.Background(), "zone-controls", commands[0].ID); err != nil {
		t.Fatalf("ack pause: %v", err)
	}

	// When
	if err := store.Resume(context.Background(), "zone-controls"); err != nil {
		t.Fatalf("resume: %v", err)
	}

	// Then
	resumed, err := store.Snapshot(context.Background(), "zone-controls")
	if err != nil {
		t.Fatalf("resumed snapshot: %v", err)
	}
	commands, err = store.PendingOutbox(context.Background(), "zone-controls")
	if err != nil {
		t.Fatalf("resume outbox: %v", err)
	}
	if resumed.Transport != TransportPlaying || resumed.SessionID != paused.SessionID ||
		len(commands) != 1 || commands[0].Type != "resume" {
		t.Fatalf("resume state/outbox = %+v / %+v", resumed, commands)
	}
}

func TestStopBeforeStartSupersedesPlayCommandAndRestoresQueue(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	enqueueAvailable(t, store, "zone-cancel", "track-a")
	decision, err := store.ReserveNext(context.Background(), "zone-cancel", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// When
	if err := store.Stop(context.Background(), "zone-cancel"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	// Then
	snapshot, err := store.Snapshot(context.Background(), "zone-cancel")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	commands, err := store.PendingOutbox(context.Background(), "zone-cancel")
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if snapshot.Queue[0].State != QueuePending || len(commands) != 1 ||
		commands[0].Type != "stop" || commands[0].PlayID != decision.PlayID {
		t.Fatalf("stop-before-start state/outbox = %+v / %+v", snapshot, commands)
	}
}
