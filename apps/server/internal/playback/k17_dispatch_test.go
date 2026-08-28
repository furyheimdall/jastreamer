package playback

import (
	"context"
	"errors"
	"testing"
)

func naturalK17Dispatch(t *testing.T) (k17LifecycleFixture, K17LifecycleResult) {
	t.Helper()
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)
	confirmK17Playing(t, fixture)
	ended, err := fixture.store.ApplyK17Observation(context.Background(), stoppedK17Observation(fixture, true))
	if err != nil {
		t.Fatal(err)
	}
	return fixture, ended
}

func Test_K17Lifecycle_restart_exposes_pending_dispatch_for_replay(t *testing.T) {
	// Given
	fixture, ended := naturalK17Dispatch(t)
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := openTestStore(t, fixture.config)

	// When
	pending, err := restarted.PendingK17LifecycleDispatches(context.Background())
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Action != K17LifecycleNaturalEnd || pending[0].Decision.ID != ended.Decision.ID || pending[0].Decision.PlayID != ended.Decision.PlayID {
		t.Fatalf("restart pending dispatches = %+v", pending)
	}
	snapshot, err := restarted.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CurrentPlay != ended.Decision.PlayID || snapshot.Queue[0].State != QueueCompleted || snapshot.Queue[1].State != QueueReserved {
		t.Fatalf("restart changed advancement = %+v", snapshot)
	}
}

func Test_K17Dispatch_success_completion_is_idempotent(t *testing.T) {
	// Given
	fixture, ended := naturalK17Dispatch(t)
	identity := K17DispatchIdentity{ZoneID: ended.ZoneID, CommandID: ended.Decision.ID, PlayID: ended.Decision.PlayID}
	claim, err := fixture.store.ClaimK17TransportDispatch(context.Background(), identity)
	if err != nil || claim != K17DispatchClaimed {
		t.Fatalf("claim = %q, %v", claim, err)
	}
	if _, err := fixture.store.CompleteK17TransportDispatch(context.Background(), identity, K17DispatchSucceeded); err != nil {
		t.Fatal(err)
	}

	// When
	_, err = fixture.store.CompleteK17TransportDispatch(context.Background(), identity, K17DispatchSucceeded)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), ended.ZoneID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportStarting {
		t.Fatalf("success fabricated observation: %+v", snapshot)
	}
}

func Test_K17Dispatch_failure_completion_is_idempotent(t *testing.T) {
	// Given
	fixture, ended := naturalK17Dispatch(t)
	identity := K17DispatchIdentity{ZoneID: ended.ZoneID, CommandID: ended.Decision.ID, PlayID: ended.Decision.PlayID}
	claim, err := fixture.store.ClaimK17TransportDispatch(context.Background(), identity)
	if err != nil || claim != K17DispatchClaimed {
		t.Fatalf("claim = %q, %v", claim, err)
	}
	firstRevision, err := fixture.store.CompleteK17TransportDispatch(context.Background(), identity, K17DispatchAdapterFailed)
	if err != nil {
		t.Fatal(err)
	}

	// When
	secondRevision, err := fixture.store.CompleteK17TransportDispatch(context.Background(), identity, K17DispatchAdapterFailed)

	// Then
	if err != nil || secondRevision != firstRevision {
		t.Fatalf("duplicate failure = revision %d, %v; want %d", secondRevision, err, firstRevision)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), ended.ZoneID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportSuspended || snapshot.Revision != firstRevision {
		t.Fatalf("failure completion = %+v", snapshot)
	}
}

func Test_K17Dispatch_claim_rejects_wrong_zone_play_command_and_kind(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, k17LifecycleFixture, K17LifecycleResult) K17DispatchIdentity
	}{
		{
			name: "wrong zone",
			mutate: func(_ *testing.T, _ k17LifecycleFixture, ended K17LifecycleResult) K17DispatchIdentity {
				return K17DispatchIdentity{ZoneID: "other", CommandID: ended.Decision.ID, PlayID: ended.Decision.PlayID}
			},
		},
		{
			name: "wrong play",
			mutate: func(_ *testing.T, _ k17LifecycleFixture, ended K17LifecycleResult) K17DispatchIdentity {
				return K17DispatchIdentity{ZoneID: ended.ZoneID, CommandID: ended.Decision.ID, PlayID: "other"}
			},
		},
		{
			name: "wrong command",
			mutate: func(_ *testing.T, _ k17LifecycleFixture, ended K17LifecycleResult) K17DispatchIdentity {
				return K17DispatchIdentity{ZoneID: ended.ZoneID, CommandID: "other", PlayID: ended.Decision.PlayID}
			},
		},
		{
			name: "wrong kind",
			mutate: func(t *testing.T, fixture k17LifecycleFixture, ended K17LifecycleResult) K17DispatchIdentity {
				if err := fixture.store.db.exec("UPDATE renderer_outbox SET command_type='stop' WHERE command_id='" + ended.Decision.ID + "'"); err != nil {
					t.Fatal(err)
				}
				return K17DispatchIdentity{ZoneID: ended.ZoneID, CommandID: ended.Decision.ID, PlayID: ended.Decision.PlayID}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture, ended := naturalK17Dispatch(t)
			identity := test.mutate(t, fixture, ended)

			// When
			_, err := fixture.store.ClaimK17TransportDispatch(context.Background(), identity)

			// Then
			if !errors.Is(err, ErrCommandDeliveryConflict) {
				t.Fatalf("claim error = %v", err)
			}
		})
	}
}
