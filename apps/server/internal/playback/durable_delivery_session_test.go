package playback

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type rendererDeliveryFixture struct {
	store      *Store
	config     Config
	rendererID RendererID
	zoneID     ZoneID
	first      Decision
	now        time.Time
}

func newRendererDeliveryFixture(t *testing.T) rendererDeliveryFixture {
	t.Helper()
	ctx := context.Background()
	config := testConfig(t)
	store := openTestStore(t, config)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	rendererID := RendererID("renderer-delivery")
	zoneID := ZoneID("zone-delivery-session")
	if _, err := store.UpsertCustomRenderer(ctx, CustomRenderer{
		ID: rendererID, DisplayName: "Fixture renderer", State: RendererAvailable,
		ProtocolMajor: 3, Capabilities: []string{"play", "stop"}, LastSeenAt: now,
	}); err != nil {
		t.Fatalf("register renderer: %v", err)
	}
	if _, err := store.CreateZone(ctx, ZoneDefinition{ID: zoneID, DisplayName: "Delivery zone"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: zoneID, RendererID: rendererID}); err != nil {
		t.Fatalf("assign renderer: %v", err)
	}
	if _, err := store.Enqueue(ctx, EnqueueRequest{
		ZoneID: zoneID, IdempotencyKey: "delivery-queue",
		Tracks: []QueueTrack{{ID: "track-one", Available: true}, {ID: "track-two", Available: true}},
	}); err != nil {
		t.Fatalf("enqueue fixture tracks: %v", err)
	}
	first, err := store.ReserveNext(ctx, zoneID, Boundary{ID: "delivery-start"})
	if err != nil {
		t.Fatalf("reserve first track: %v", err)
	}
	return rendererDeliveryFixture{
		store: store, config: config, rendererID: rendererID, zoneID: zoneID, first: first, now: now,
	}
}

func (fixture rendererDeliveryFixture) openSession(t *testing.T, cursor CommandSequence) RendererSessionState {
	t.Helper()
	session, err := fixture.store.OpenRendererSession(context.Background(), RendererSessionRequest{
		RendererID: fixture.rendererID, LastServerSequence: cursor, ConnectedAt: fixture.now,
	})
	if err != nil {
		t.Fatalf("open renderer session: %v", err)
	}
	return session
}

func (fixture rendererDeliveryFixture) acquireCommand(
	t *testing.T,
	session RendererSessionState,
	at time.Time,
) DurableCommand {
	t.Helper()
	command, err := fixture.store.AcquireRendererCommand(context.Background(), RendererCommandRequest{
		RendererID: fixture.rendererID, Epoch: session.Epoch,
		AttemptedAt: at, Deadline: fixture.now.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("acquire renderer command: %v", err)
	}
	return command
}

func Test_AcquireRendererCommand_redelivers_unresolved_sequence_without_overlap(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)

	// When
	first := fixture.acquireCommand(t, session, fixture.now)
	replayed := fixture.acquireCommand(t, session, fixture.now.Add(time.Second))

	// Then
	if first.ID != replayed.ID || first.Sequence != replayed.Sequence || first.Sequence != 1 {
		t.Fatalf("redelivery changed identity: first=%+v replayed=%+v", first, replayed)
	}
	if first.SessionID == "" || first.PlayID != fixture.first.PlayID || first.Deadline.IsZero() || first.Attempts != 1 || replayed.Attempts != 2 {
		t.Fatalf("durable delivery fields: first=%+v replayed=%+v", first, replayed)
	}
	var payload struct {
		ZoneID    ZoneID    `json:"zoneId"`
		SessionID SessionID `json:"sessionId"`
		PlayID    PlayID    `json:"playId"`
		TrackID   TrackID   `json:"trackId"`
		Kind      string    `json:"kind"`
	}
	if err := json.Unmarshal(first.Payload, &payload); err != nil {
		t.Fatalf("decode durable command payload: %v", err)
	}
	if payload.ZoneID != fixture.zoneID || payload.SessionID != first.SessionID ||
		payload.PlayID != first.PlayID || payload.TrackID != "track-one" || payload.Kind != "play" {
		t.Fatalf("durable command payload = %+v", payload)
	}
}

func Test_RendererResult_and_natural_end_advance_once_with_separate_truth(t *testing.T) {
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
	result := RendererTerminalResult{
		RendererID: fixture.rendererID, Epoch: session.Epoch, CommandID: command.ID,
		ResultID: "result-one", Status: "succeeded", ObservedState: "playing",
		Payload: json.RawMessage(`{"positionMs":0}`), RecordedAt: fixture.now.Add(2 * time.Second),
	}
	if err := fixture.store.RecordRendererTerminalResult(context.Background(), result); err != nil {
		t.Fatalf("record terminal result: %v", err)
	}

	// When
	event := RendererPlaybackEvent{
		RendererID: fixture.rendererID, Epoch: session.Epoch, EventID: "event-ended-one",
		PlayID: fixture.first.PlayID, Kind: PlaybackEventEnded, ObservedAt: fixture.now.Add(3 * time.Minute),
	}
	advanced, err := fixture.store.HandleRendererPlaybackEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("handle natural end: %v", err)
	}
	replayed, err := fixture.store.HandleRendererPlaybackEvent(context.Background(), event)
	if err != nil {
		t.Fatalf("replay natural end: %v", err)
	}
	truth, err := fixture.store.RendererSessionTruth(context.Background(), fixture.rendererID)
	if err != nil {
		t.Fatalf("load renderer truth: %v", err)
	}
	next := fixture.acquireCommand(t, session, fixture.now.Add(4*time.Minute))

	// Then
	if advanced.ID == "" || replayed.ID != advanced.ID || advanced.PlayID == fixture.first.PlayID {
		t.Fatalf("natural-end decisions: advanced=%+v replayed=%+v", advanced, replayed)
	}
	if next.Sequence != command.Sequence+1 || next.PlayID != advanced.PlayID {
		t.Fatalf("next command overlapped or changed decision: first=%+v next=%+v", command, next)
	}
	if truth.IntentPlayID != advanced.PlayID || truth.IntentTransport != TransportStarting ||
		truth.ObservedPlayID != fixture.first.PlayID || truth.ObservedState != "ended" {
		t.Fatalf("intent and observed state were conflated: %+v", truth)
	}
}

func TestCloseRendererSessionResult_reports_only_committed_state_transition(t *testing.T) {
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)

	closed, err := fixture.store.CloseRendererSessionResult(t.Context(), RendererSessionClose{
		RendererID: fixture.rendererID, Epoch: session.Epoch, DisconnectedAt: fixture.now.Add(time.Second),
	})
	if err != nil || !closed.Changed || closed.Renderer.State != RendererAvailable {
		t.Fatalf("first close = %+v, %v", closed, err)
	}
	committed, err := fixture.store.Renderer(t.Context(), fixture.rendererID)
	if err != nil || committed.Revision != closed.Renderer.Revision || committed.State != RendererAvailable {
		t.Fatalf("committed renderer = %+v, %v; close = %+v", committed, err, closed)
	}
	duplicate, err := fixture.store.CloseRendererSessionResult(t.Context(), RendererSessionClose{
		RendererID: fixture.rendererID, Epoch: session.Epoch, DisconnectedAt: fixture.now.Add(2 * time.Second),
	})
	afterDuplicate, readErr := fixture.store.Renderer(t.Context(), fixture.rendererID)
	if err != nil || readErr != nil || duplicate.Changed || afterDuplicate.Revision != committed.Revision {
		t.Fatalf("duplicate close = %+v/%v, renderer = %+v/%v", duplicate, err, afterDuplicate, readErr)
	}
	stale, err := fixture.store.CloseRendererSessionResult(t.Context(), RendererSessionClose{
		RendererID: fixture.rendererID, Epoch: "stale-epoch", DisconnectedAt: fixture.now.Add(3 * time.Second),
	})
	if err != nil || stale.Changed {
		t.Fatalf("stale close = %+v, %v", stale, err)
	}
}

func TestCloseRendererSessionResult_suppresses_revocation_overlap_and_failed_close(t *testing.T) {
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	if err := fixture.store.RevokeRenderer(t.Context(), fixture.rendererID); err != nil {
		t.Fatalf("revoke renderer: %v", err)
	}
	revoked, err := fixture.store.Renderer(t.Context(), fixture.rendererID)
	if err != nil {
		t.Fatalf("read revoked renderer: %v", err)
	}
	overlap, err := fixture.store.CloseRendererSessionResult(t.Context(), RendererSessionClose{
		RendererID: fixture.rendererID, Epoch: session.Epoch, DisconnectedAt: fixture.now.Add(time.Second),
	})
	afterOverlap, readErr := fixture.store.Renderer(t.Context(), fixture.rendererID)
	if err != nil || readErr != nil || overlap.Changed || afterOverlap.State != RendererRevoked || afterOverlap.Revision != revoked.Revision {
		t.Fatalf("revocation overlap = %+v/%v, renderer = %+v/%v", overlap, err, afterOverlap, readErr)
	}

	closedFixture := newRendererDeliveryFixture(t)
	closedSession := closedFixture.openSession(t, 0)
	if err := closedFixture.store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	failed, err := closedFixture.store.CloseRendererSessionResult(t.Context(), RendererSessionClose{
		RendererID: closedFixture.rendererID, Epoch: closedSession.Epoch, DisconnectedAt: closedFixture.now.Add(time.Second),
	})
	if err == nil || failed.Changed {
		t.Fatalf("failed close = %+v, %v", failed, err)
	}
}

func Test_RendererSession_disconnect_suspends_ambiguous_start_without_consuming_queue(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	session := fixture.openSession(t, 0)
	_ = fixture.acquireCommand(t, session, fixture.now)

	// When
	err := fixture.store.CloseRendererSession(context.Background(), RendererSessionClose{
		RendererID: fixture.rendererID, Epoch: session.Epoch, DisconnectedAt: fixture.now.Add(time.Second),
	})
	snapshot, snapshotErr := fixture.store.Snapshot(context.Background(), fixture.zoneID)

	// Then
	if err != nil || snapshotErr != nil {
		t.Fatalf("close/snapshot errors: %v / %v", err, snapshotErr)
	}
	if snapshot.Transport != TransportSuspended || snapshot.Queue[0].State != QueueReserved || snapshot.CurrentPlay != fixture.first.PlayID {
		t.Fatalf("ambiguous disconnect mutated queue truth: %+v", snapshot)
	}
}

func Test_Stop_supersedes_unresolved_play_and_survives_reconnect(t *testing.T) {
	// Given
	fixture := newRendererDeliveryFixture(t)
	firstSession := fixture.openSession(t, 0)
	play := fixture.acquireCommand(t, firstSession, fixture.now)
	if err := fixture.store.Stop(context.Background(), fixture.zoneID); err != nil {
		t.Fatalf("commit stop intent: %v", err)
	}
	if err := fixture.store.CloseRendererSession(context.Background(), RendererSessionClose{
		RendererID: fixture.rendererID, Epoch: firstSession.Epoch, DisconnectedAt: fixture.now.Add(time.Second),
	}); err != nil {
		t.Fatalf("close first session: %v", err)
	}

	// When
	reconnected := fixture.openSession(t, play.Sequence)
	stop := fixture.acquireCommand(t, reconnected, fixture.now.Add(2*time.Second))

	// Then
	if stop.Type != "stop" || stop.PlayID != play.PlayID || stop.Sequence != play.Sequence+1 {
		t.Fatalf("reconnected stop command = %+v, play = %+v", stop, play)
	}
	playState, err := fixture.store.DurableCommand(context.Background(), play.ID)
	if err != nil {
		t.Fatalf("load superseded play: %v", err)
	}
	if playState.SupersededAt.IsZero() {
		t.Fatalf("play was not durably superseded: %+v", playState)
	}
}
