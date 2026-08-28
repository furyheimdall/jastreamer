package playback

import (
	"context"
	"testing"
	"time"
)

type k17LifecycleFixture struct {
	store   *Store
	config  Config
	started TransportMutationResult
	now     time.Time
}

func newK17LifecycleFixture(t *testing.T) k17LifecycleFixture {
	t.Helper()
	ctx := context.Background()
	config := testConfig(t)
	store := openTestStore(t, config)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	if _, err := store.CreateZone(ctx, ZoneDefinition{ID: "k17-zone", DisplayName: "K17"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertK17Renderer(ctx, K17Renderer{
		ID: "k17", DisplayName: "K17", State: RendererAvailable, UDN: "uuid:k17", Model: "FiiO K17",
		FirmwareVersion: "V262", ProtocolInfo: "http-get:*:audio/flac:*", Protocols: []string{"audio/flac"}, LastSeenAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: "k17-zone", RendererID: "k17"}); err != nil {
		t.Fatal(err)
	}
	enqueued, err := store.Enqueue(ctx, EnqueueRequest{
		ZoneID: "k17-zone", IdempotencyKey: "seed", Tracks: []QueueTrack{{ID: "track", Available: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	started, err := store.MutateTransport(ctx, TransportMutationRequest{
		ZoneID: "k17-zone", IdempotencyKey: "start", ExpectedRevision: enqueued.Revision, Command: TransportStart,
	})
	if err != nil {
		t.Fatal(err)
	}
	return k17LifecycleFixture{store: store, config: config, started: started, now: now}
}

func (fixture k17LifecycleFixture) observation(owned bool) K17Observation {
	return K17Observation{
		RendererID: "k17", ZoneID: "k17-zone", Transport: "PLAYING", Position: 7 * time.Second,
		CurrentURI: "https://server/media/v1/token", Owned: owned, ObservedAt: fixture.now.Add(time.Second),
	}
}

func Test_K17Lifecycle_disappearance_and_external_override_suspend_without_consuming_queue(t *testing.T) {
	// Given: a reserved queue head whose play command is in flight.
	ctx := context.Background()
	fixture := newK17LifecycleFixture(t)

	// When: the assigned K17 disappears during start.
	if err := fixture.store.MarkK17Unavailable(ctx, "k17"); err != nil {
		t.Fatal(err)
	}

	// Then: intent is suspended and the queue reservation is preserved.
	snapshot, err := fixture.store.Snapshot(ctx, "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportSuspended || snapshot.CurrentPlay != fixture.started.PlayID || snapshot.Queue[0].State != QueueReserved {
		t.Fatalf("disappearance state = %+v", snapshot)
	}

	// When: it reappears while externally playing, followed by confirmed Server-owned playback.
	if _, err := fixture.store.UpsertK17Renderer(ctx, K17Renderer{
		ID: "k17", DisplayName: "K17", State: RendererAvailable, UDN: "uuid:k17", Model: "FiiO K17",
		FirmwareVersion: "V262", ProtocolInfo: "http-get:*:audio/flac:*", Protocols: []string{"audio/flac"}, LastSeenAt: fixture.now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordK17Observation(ctx, fixture.observation(false)); err != nil {
		t.Fatal(err)
	}
	external, err := fixture.store.Snapshot(ctx, "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.RecordK17Observation(ctx, fixture.observation(true)); err != nil {
		t.Fatal(err)
	}
	reconciled, err := fixture.store.Snapshot(ctx, "k17-zone")
	if err != nil {
		t.Fatal(err)
	}

	// Then: external playback was never adopted, while owned playback safely reconciles the same play.
	if external.Transport != TransportSuspended || external.Queue[0].State != QueueReserved {
		t.Fatalf("external playback was adopted: %+v", external)
	}
	if reconciled.Transport != TransportPlaying || reconciled.CurrentPlay != fixture.started.PlayID || reconciled.Queue[0].State != QueuePlaying {
		t.Fatalf("owned reappearance did not reconcile: %+v", reconciled)
	}
	truth, err := fixture.store.RendererSessionTruth(ctx, "k17")
	if err != nil || truth.ObservedState != "playing" || truth.ObservedAt.IsZero() {
		t.Fatalf("observed truth = %+v, %v", truth, err)
	}
}

func Test_K17Lifecycle_external_state_change_suspends_active_play_without_advancing(t *testing.T) {
	// Given: Server-owned playback has been reconciled.
	ctx := context.Background()
	fixture := newK17LifecycleFixture(t)
	if err := fixture.store.RecordK17Observation(ctx, fixture.observation(true)); err != nil {
		t.Fatal(err)
	}

	// When: polling reports a URI ownership mismatch.
	if err := fixture.store.RecordK17Observation(ctx, fixture.observation(false)); err != nil {
		t.Fatal(err)
	}

	// Then: the current play and queue head remain intact under suspended intent.
	snapshot, err := fixture.store.Snapshot(ctx, "k17-zone")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Transport != TransportSuspended || snapshot.CurrentPlay != fixture.started.PlayID || snapshot.Queue[0].State != QueuePlaying {
		t.Fatalf("override state = %+v", snapshot)
	}
}
