package playback

import (
	"context"
	"testing"
)

func enqueueK17Follower(t *testing.T, fixture k17LifecycleFixture, available bool) {
	t.Helper()
	_, err := fixture.store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "k17-zone", IdempotencyKey: "follower", ExpectedRevision: fixture.started.Revision,
		Tracks: []QueueTrack{{ID: "follower", Available: available}},
	})
	if err != nil {
		t.Fatalf("enqueue follower: %v", err)
	}
}

func confirmK17Playing(t *testing.T, fixture k17LifecycleFixture) {
	t.Helper()
	if _, err := fixture.store.ApplyK17Observation(context.Background(), fixture.observation(true)); err != nil {
		t.Fatalf("confirm K17 playing: %v", err)
	}
}

func stoppedK17Observation(fixture k17LifecycleFixture, owned bool) K17Observation {
	observation := fixture.observation(owned)
	observation.Transport = "STOPPED"
	observation.ObservedAt = observation.ObservedAt.Add(1)
	return observation
}

func Test_K17Lifecycle_owned_playing_to_stopped_advances_once(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)
	confirmK17Playing(t, fixture)

	// When
	result, err := fixture.store.ApplyK17Observation(context.Background(), stoppedK17Observation(fixture, true))
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if result.Action != K17LifecycleNaturalEnd || result.PreviousPlayID != fixture.started.PlayID || result.Decision.Kind != DecisionPlay || result.Decision.TrackID != "follower" {
		t.Fatalf("natural-end result = %+v", result)
	}
	if result.BoundaryID != BoundaryID("k17-ended:"+fixture.started.PlayID) {
		t.Fatalf("ended boundary = %q", result.BoundaryID)
	}
}

func Test_K17Lifecycle_duplicate_stopped_does_not_advance_twice(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)
	confirmK17Playing(t, fixture)
	observation := stoppedK17Observation(fixture, true)
	first, err := fixture.store.ApplyK17Observation(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}

	// When
	duplicate, err := fixture.store.ApplyK17Observation(context.Background(), observation)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if first.Action != K17LifecycleNaturalEnd || duplicate.Action != K17LifecycleIgnored {
		t.Fatalf("first/duplicate = %+v / %+v", first, duplicate)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CurrentPlay != first.Decision.PlayID || snapshot.Queue[0].State != QueueCompleted || snapshot.Queue[1].State != QueueReserved {
		t.Fatalf("duplicate advanced queue: %+v", snapshot)
	}
}

func Test_K17Lifecycle_crash_restart_replay_does_not_double_advance(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)
	confirmK17Playing(t, fixture)
	observation := stoppedK17Observation(fixture, true)
	first, err := fixture.store.ApplyK17Observation(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := openTestStore(t, fixture.config)

	// When
	replay, err := restarted.ApplyK17Observation(context.Background(), observation)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if replay.Action != K17LifecycleIgnored {
		t.Fatalf("restart replay = %+v", replay)
	}
	snapshot, err := restarted.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CurrentPlay != first.Decision.PlayID || snapshot.Queue[1].State != QueueReserved {
		t.Fatalf("restart double advance = %+v", snapshot)
	}
}

func Test_K17Lifecycle_explicit_stop_does_not_advance(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)
	confirmK17Playing(t, fixture)
	snapshot, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: "k17-zone", IdempotencyKey: "explicit-stop", ExpectedRevision: snapshot.Revision, Command: TransportStop,
	}); err != nil {
		t.Fatal(err)
	}

	// When
	result, err := fixture.store.ApplyK17Observation(context.Background(), stoppedK17Observation(fixture, true))

	// Then
	if err != nil || result.Action != K17LifecycleIgnored {
		t.Fatalf("explicit stop observation = %+v, %v", result, err)
	}
	after, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if after.Transport != TransportIdle || after.Queue[1].State != QueuePending {
		t.Fatalf("explicit stop advanced = %+v", after)
	}
}

func Test_K17Lifecycle_failed_start_stopped_does_not_advance(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)

	// When
	result, err := fixture.store.ApplyK17Observation(context.Background(), stoppedK17Observation(fixture, true))

	// Then
	if err != nil || result.Action != K17LifecycleIgnored {
		t.Fatalf("failed start observation = %+v, %v", result, err)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportStarting || snapshot.Queue[0].State != QueueReserved || snapshot.Queue[1].State != QueuePending {
		t.Fatalf("failed start advanced = %+v", snapshot)
	}
}

func Test_K17Lifecycle_external_URI_stopped_does_not_advance(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)
	confirmK17Playing(t, fixture)

	// When
	result, err := fixture.store.ApplyK17Observation(context.Background(), stoppedK17Observation(fixture, false))

	// Then
	if err != nil || result.Action != K17LifecycleSuspended {
		t.Fatalf("external stopped = %+v, %v", result, err)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportSuspended || snapshot.Queue[0].State != QueuePlaying || snapshot.Queue[1].State != QueuePending {
		t.Fatalf("external URI advanced = %+v", snapshot)
	}
}

func Test_K17Lifecycle_stale_stopped_after_next_reservation_is_ignored(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)
	confirmK17Playing(t, fixture)
	observation := stoppedK17Observation(fixture, true)
	first, err := fixture.store.ApplyK17Observation(context.Background(), observation)
	if err != nil {
		t.Fatal(err)
	}

	// When
	stale, err := fixture.store.ApplyK17Observation(context.Background(), observation)

	// Then
	if err != nil || stale.Action != K17LifecycleIgnored {
		t.Fatalf("stale stopped = %+v, %v", stale, err)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportStarting || snapshot.CurrentPlay != first.Decision.PlayID {
		t.Fatalf("stale observation changed reservation = %+v", snapshot)
	}
}

func Test_K17Lifecycle_unavailable_does_not_advance(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, true)
	confirmK17Playing(t, fixture)

	// When
	err := fixture.store.MarkK17Unavailable(context.Background(), "k17")
	// Then
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportSuspended || snapshot.Queue[0].State != QueuePlaying || snapshot.Queue[1].State != QueuePending {
		t.Fatalf("unavailable advanced = %+v", snapshot)
	}
}

func Test_K17Lifecycle_queue_exhausted_reaches_idle(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	confirmK17Playing(t, fixture)

	// When
	result, err := fixture.store.ApplyK17Observation(context.Background(), stoppedK17Observation(fixture, true))

	// Then
	if err != nil || result.Action != K17LifecycleNaturalEnd || result.Decision.Kind != DecisionStop {
		t.Fatalf("exhausted result = %+v, %v", result, err)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportIdle || snapshot.CurrentPlay != "" || snapshot.Queue[0].State != QueueCompleted {
		t.Fatalf("exhausted snapshot = %+v", snapshot)
	}
}

func Test_K17Lifecycle_blocked_head_stays_blocked(t *testing.T) {
	// Given
	fixture := newK17LifecycleFixture(t)
	enqueueK17Follower(t, fixture, false)
	confirmK17Playing(t, fixture)

	// When
	result, err := fixture.store.ApplyK17Observation(context.Background(), stoppedK17Observation(fixture, true))

	// Then
	if err != nil || result.Action != K17LifecycleNaturalEnd || result.Decision.Kind != DecisionBlock {
		t.Fatalf("blocked result = %+v, %v", result, err)
	}
	snapshot, err := fixture.store.Snapshot(context.Background(), "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportBlocked || snapshot.Queue[1].State != QueueBlocked {
		t.Fatalf("blocked snapshot = %+v", snapshot)
	}
}
