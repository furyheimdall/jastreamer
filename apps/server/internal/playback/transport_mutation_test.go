package playback

import (
	"context"
	"errors"
	"testing"
	"time"
)

func transportFixture(t *testing.T, state RendererState, capabilities []string) (*Store, Revision) {
	t.Helper()
	store := openTestStore(t, testConfig(t))
	if _, err := store.CreateZone(context.Background(), ZoneDefinition{ID: "transport-zone", DisplayName: "Transport"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	if _, err := store.UpsertCustomRenderer(context.Background(), CustomRenderer{
		ID: "renderer", DisplayName: "Renderer", State: state, ProtocolMajor: 3,
		Capabilities: capabilities, LastSeenAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create renderer: %v", err)
	}
	if _, err := store.AssignRenderer(context.Background(), AssignmentRequest{ZoneID: "transport-zone", RendererID: "renderer"}); err != nil {
		t.Fatalf("assign renderer: %v", err)
	}
	enqueued, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "transport-zone", IdempotencyKey: "seed", Tracks: []QueueTrack{{ID: "track", Available: true}},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return store, enqueued.Revision
}

func Test_Transport_start_records_pending_intent_once_on_idempotent_replay(t *testing.T) {
	// Given
	store, revision := transportFixture(t, RendererConnected, []string{"command:play", "command:pause", "command:resume", "command:stop", "command:seek"})
	request := TransportMutationRequest{ZoneID: "transport-zone", IdempotencyKey: "start", ExpectedRevision: revision, Command: TransportStart}

	// When
	first, err := store.MutateTransport(context.Background(), request)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	replay, err := store.MutateTransport(context.Background(), request)
	if err != nil {
		t.Fatalf("replay start: %v", err)
	}

	// Then
	commands, err := store.PendingOutbox(context.Background(), "transport-zone")
	if err != nil {
		t.Fatalf("pending commands: %v", err)
	}
	if first.Status != TransportMutationPending || !replay.Replayed || replay.CommandID != first.CommandID || len(commands) != 1 {
		t.Fatalf("start/replay/commands = %+v / %+v / %+v", first, replay, commands)
	}
}

func Test_Transport_start_is_not_deliverable_until_media_is_ready(t *testing.T) {
	// Given
	store, revision := transportFixture(t, RendererConnected, []string{"command:play"})
	started, err := store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: "transport-zone", IdempotencyKey: "start", ExpectedRevision: revision, Command: TransportStart,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	session, err := store.OpenRendererSession(context.Background(), RendererSessionRequest{
		RendererID: "renderer", ConnectedAt: time.Date(2026, 8, 25, 12, 1, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("open renderer session: %v", err)
	}
	request := RendererCommandRequest{
		RendererID: "renderer", Epoch: session.Epoch,
		AttemptedAt: time.Date(2026, 8, 25, 12, 1, 1, 0, time.UTC),
		Deadline:    time.Date(2026, 8, 25, 12, 6, 1, 0, time.UTC),
	}

	// When
	_, beforeErr := store.AcquireRendererCommand(context.Background(), request)
	if err := store.MarkTransportMediaReady(context.Background(), started.CommandID); err != nil {
		t.Fatalf("mark media ready: %v", err)
	}
	command, afterErr := store.AcquireRendererCommand(context.Background(), request)

	// Then
	if !errors.Is(beforeErr, ErrNoRendererCommand) || afterErr != nil || command.ID != started.CommandID {
		t.Fatalf("delivery gate = before %v, after %+v (%v)", beforeErr, command, afterErr)
	}
}

func Test_Transport_start_offline_failure_is_typed_and_has_zero_mutation(t *testing.T) {
	// Given
	store, revision := transportFixture(t, RendererUnavailable, []string{"command:play"})
	before, err := store.Snapshot(context.Background(), "transport-zone")
	if err != nil {
		t.Fatalf("snapshot before: %v", err)
	}

	// When
	_, err = store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: "transport-zone", IdempotencyKey: "offline", ExpectedRevision: revision, Command: TransportStart,
	})

	// Then
	if !errors.Is(err, ErrRendererOffline) {
		t.Fatalf("offline start error = %v", err)
	}
	after, snapshotErr := store.Snapshot(context.Background(), "transport-zone")
	if snapshotErr != nil {
		t.Fatalf("snapshot after: %v", snapshotErr)
	}
	if after.Revision != before.Revision || after.Transport != before.Transport || len(after.Queue) != len(before.Queue) {
		t.Fatalf("offline start mutated state: before=%+v after=%+v", before, after)
	}
}

func Test_Transport_skip_advances_only_after_stop_acknowledgement_and_replays_ack(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	if _, err := store.CreateZone(context.Background(), ZoneDefinition{ID: "skip-zone", DisplayName: "Skip"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	if _, err := store.UpsertCustomRenderer(context.Background(), CustomRenderer{
		ID: "skip-renderer", DisplayName: "Renderer", State: RendererConnected, ProtocolMajor: 3,
		Capabilities: []string{"command:play", "command:stop"}, LastSeenAt: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create renderer: %v", err)
	}
	if _, err := store.AssignRenderer(context.Background(), AssignmentRequest{ZoneID: "skip-zone", RendererID: "skip-renderer"}); err != nil {
		t.Fatalf("assign renderer: %v", err)
	}
	enqueued, err := store.Enqueue(context.Background(), EnqueueRequest{
		ZoneID: "skip-zone", IdempotencyKey: "seed", Tracks: []QueueTrack{{ID: "a", Available: true}, {ID: "b", Available: true}},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	started, err := store.MutateTransport(context.Background(), TransportMutationRequest{ZoneID: "skip-zone", IdempotencyKey: "start", ExpectedRevision: enqueued.Revision, Command: TransportStart})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	confirmed, err := store.ConfirmStart(context.Background(), "skip-zone", started.PlayID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	skip, err := store.MutateTransport(context.Background(), TransportMutationRequest{ZoneID: "skip-zone", IdempotencyKey: "skip", ExpectedRevision: confirmed.Revision, Command: TransportSkip})
	if err != nil {
		t.Fatalf("skip intent: %v", err)
	}
	beforeAck, err := store.Snapshot(context.Background(), "skip-zone")
	if err != nil {
		t.Fatalf("snapshot before ack: %v", err)
	}

	// When
	decision, err := store.CompleteAcknowledgedSkip(context.Background(), skip.CommandID, "skip-result")
	if err != nil {
		t.Fatalf("acknowledge skip: %v", err)
	}
	replay, err := store.CompleteAcknowledgedSkip(context.Background(), skip.CommandID, "skip-result")
	// Then
	if err != nil {
		t.Fatalf("replay skip acknowledgement: %v", err)
	}
	afterAck, err := store.Snapshot(context.Background(), "skip-zone")
	if err != nil {
		t.Fatalf("snapshot after ack: %v", err)
	}
	if beforeAck.Queue[0].State != QueuePlaying || decision.TrackID != "b" || replay.ID != decision.ID || afterAck.Queue[1].State != QueueReserved {
		t.Fatalf("skip states = before=%+v decision=%+v replay=%+v after=%+v", beforeAck, decision, replay, afterAck)
	}
}

func Test_Transport_seek_records_supported_position_as_pending_physical_work(t *testing.T) {
	// Given
	store, revision := transportFixture(t, RendererConnected, []string{"command:play", "command:seek"})
	started, err := store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: "transport-zone", IdempotencyKey: "start", ExpectedRevision: revision, Command: TransportStart,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "transport-zone", started.PlayID); err != nil {
		t.Fatalf("confirm start: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "transport-zone")
	if err != nil {
		t.Fatalf("snapshot before seek: %v", err)
	}

	// When
	result, err := store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: "transport-zone", IdempotencyKey: "seek", ExpectedRevision: before.Revision,
		Command: TransportSeek, PositionMS: 1_250,
	})
	// Then
	if err != nil {
		t.Fatalf("seek: %v", err)
	}
	commands, err := store.PendingOutbox(context.Background(), "transport-zone")
	if err != nil {
		t.Fatalf("pending outbox: %v", err)
	}
	if result.Status != TransportMutationPending || commands[len(commands)-1].Type != "seek" {
		t.Fatalf("seek result/commands = %+v / %+v", result, commands)
	}
}

func Test_Transport_seek_requires_renderer_capability_without_mutation(t *testing.T) {
	// Given
	store, revision := transportFixture(t, RendererConnected, []string{"command:play"})
	started, err := store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: "transport-zone", IdempotencyKey: "start", ExpectedRevision: revision, Command: TransportStart,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := store.ConfirmStart(context.Background(), "transport-zone", started.PlayID); err != nil {
		t.Fatalf("confirm start: %v", err)
	}
	before, err := store.Snapshot(context.Background(), "transport-zone")
	if err != nil {
		t.Fatalf("snapshot before seek: %v", err)
	}

	// When
	_, err = store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: "transport-zone", IdempotencyKey: "seek", ExpectedRevision: before.Revision,
		Command: TransportSeek, PositionMS: 1_000,
	})

	// Then
	if !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("seek error = %v", err)
	}
	after, snapshotErr := store.Snapshot(context.Background(), "transport-zone")
	if snapshotErr != nil {
		t.Fatalf("snapshot after seek: %v", snapshotErr)
	}
	if after.Revision != before.Revision {
		t.Fatalf("unsupported seek mutated revision: before=%d after=%d", before.Revision, after.Revision)
	}
}
