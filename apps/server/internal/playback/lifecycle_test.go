package playback

import (
	"context"
	"testing"
)

func queuedStore(t *testing.T) (*Store, Config) {
	t.Helper()
	config := testConfig(t)
	store := openTestStore(t, config)
	_, err := store.Enqueue(context.Background(), EnqueueRequest{ZoneID: "zone-a", IdempotencyKey: "queue", Tracks: []QueueTrack{{ID: "a", Available: true}, {ID: "b", Available: true}}})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return store, config
}

func Test_DuplicateBoundary_returns_one_decision_and_outbox_command(t *testing.T) {
	// Given
	store, _ := queuedStore(t)
	boundary := Boundary{ID: "boundary-1"}

	// When
	first, err := store.ReserveNext(context.Background(), "zone-a", boundary)
	if err != nil {
		t.Fatalf("first decision: %v", err)
	}
	second, err := store.ReserveNext(context.Background(), "zone-a", boundary)
	if err != nil {
		t.Fatalf("duplicate decision: %v", err)
	}

	// Then
	if first.ID != second.ID || first.PlayID != second.PlayID {
		t.Fatalf("duplicate boundary changed decision: %#v %#v", first, second)
	}
	commands, err := store.PendingOutbox(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if len(commands) != 1 || commands[0].PlayID != first.PlayID {
		t.Fatalf("outbox commands = %#v", commands)
	}
}

func Test_ExplicitEntry_is_not_consumed_before_confirmed_start(t *testing.T) {
	// Given
	store, _ := queuedStore(t)

	// When
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	snapshot, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	// Then
	if snapshot.Queue[0].State != QueueReserved || snapshot.Queue[1].State != QueuePending {
		t.Fatalf("queue consumed before confirmation: %#v", snapshot.Queue)
	}
	if snapshot.CurrentPlay != decision.PlayID {
		t.Fatalf("current play = %q", snapshot.CurrentPlay)
	}
}

func TestRestartBetweenReserveAndAcknowledge(t *testing.T) {
	// Given
	store, config := queuedStore(t)
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer restarted.Close()

	// When
	result, err := restarted.Reconcile(context.Background(), "zone-a", RendererObservation{OutcomeKnown: false})
	// Then
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Transport != TransportSuspended {
		t.Fatalf("transport = %s", result.Transport)
	}
	snapshot, err := restarted.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.Queue[0].State != QueueReserved || snapshot.CurrentPlay != decision.PlayID {
		t.Fatalf("reserved entry changed after crash: %#v", snapshot)
	}
}

func Test_RestartAfterConfirmedStart_adopts_renderer_play(t *testing.T) {
	// Given
	store, config := queuedStore(t)
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-a", decision.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	restarted, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	defer restarted.Close()

	// When
	result, err := restarted.Reconcile(context.Background(), "zone-a", RendererObservation{OutcomeKnown: true, Playing: true, PlayID: decision.PlayID})
	// Then
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.Transport != TransportPlaying || result.PlayID != decision.PlayID {
		t.Fatalf("reconciliation = %#v", result)
	}
}

func Test_Session_persists_pause_disconnect_and_restart_then_stop_ends_it(t *testing.T) {
	// Given
	store, _ := queuedStore(t)
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "zone-a", decision.PlayID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "zone-a")
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if err := store.Pause(context.Background(), "zone-a"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := store.Disconnect(context.Background(), "zone-a"); err != nil {
		t.Fatalf("disconnect: %v", err)
	}

	// When
	if err := store.Stop(context.Background(), "zone-a"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	after, err := store.Snapshot(context.Background(), "zone-a")
	// Then
	if err != nil {
		t.Fatalf("snapshot after stop: %v", err)
	}
	if before.SessionID == "" || before.SessionSeed == "" || after.SessionID != "" || after.Transport != TransportIdle {
		t.Fatalf("session lifecycle before=%#v after=%#v", before, after)
	}
}
