package playback

import (
	"context"
	"testing"
)

func Test_Reconcile_adopts_reserved_play_and_confirms_outbox(t *testing.T) {
	// Given
	store, _ := queuedStore(t)
	decision, err := store.ReserveNext(context.Background(), "zone-a", Boundary{ID: "start"})
	if err != nil {
		t.Fatalf("reserve: %v", err)
	}

	// When
	result, err := store.Reconcile(context.Background(), "zone-a", RendererObservation{OutcomeKnown: true, Playing: true, PlayID: decision.PlayID})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	commands, err := store.PendingOutbox(context.Background(), "zone-a")
	// Then
	if err != nil {
		t.Fatalf("outbox: %v", err)
	}
	if result.Transport != TransportPlaying || len(commands) != 0 {
		t.Fatalf("reconcile result=%#v pending=%#v", result, commands)
	}
}
