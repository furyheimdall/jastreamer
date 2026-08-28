package playback

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type previousFixture struct {
	store       *Store
	config      Config
	zoneID      ZoneID
	first       Decision
	second      Decision
	firstEntry  QueueEntryID
	secondEntry QueueEntryID
}

func newPreviousFixture(t *testing.T) previousFixture {
	t.Helper()
	ctx := context.Background()
	config := testConfig(t)
	store := openTestStore(t, config)
	const zoneID ZoneID = "previous-zone"
	if _, err := store.CreateZone(ctx, ZoneDefinition{ID: zoneID, DisplayName: "Previous"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	if _, err := store.UpsertCustomRenderer(ctx, CustomRenderer{
		ID: "previous-renderer", DisplayName: "Renderer", State: RendererConnected, ProtocolMajor: 3,
		Capabilities: []string{"command:play", "command:seek"},
		LastSeenAt:   time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("create renderer: %v", err)
	}
	if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: zoneID, RendererID: "previous-renderer"}); err != nil {
		t.Fatalf("assign renderer: %v", err)
	}
	enqueued, err := store.Enqueue(ctx, EnqueueRequest{
		ZoneID: zoneID, IdempotencyKey: "seed",
		Tracks: []QueueTrack{{ID: "first", Available: true}, {ID: "second", Available: true}},
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	first, err := store.MutateTransport(ctx, TransportMutationRequest{
		ZoneID: zoneID, IdempotencyKey: "start", ExpectedRevision: enqueued.Revision, Command: TransportStart,
	})
	if err != nil {
		t.Fatalf("start first: %v", err)
	}
	if _, err := store.ConfirmStart(ctx, zoneID, first.PlayID); err != nil {
		t.Fatalf("confirm first: %v", err)
	}
	playing, err := store.Snapshot(ctx, zoneID)
	if err != nil {
		t.Fatalf("snapshot first: %v", err)
	}
	second, err := store.CommitNext(ctx, NextRequest{
		ZoneID: zoneID, Boundary: Boundary{ID: "ended-first", PreviousPlayID: first.PlayID},
	})
	if err != nil {
		t.Fatalf("advance to second: %v", err)
	}
	if _, err := store.ConfirmStart(ctx, zoneID, second.PlayID); err != nil {
		t.Fatalf("confirm second: %v", err)
	}
	return previousFixture{
		store: store, config: config, zoneID: zoneID, first: Decision{PlayID: first.PlayID, TrackID: "first"}, second: second,
		firstEntry: playing.Queue[0].ID, secondEntry: playing.Queue[1].ID,
	}
}

func Test_Transport_previous_selects_prior_history_at_exact_five_second_boundary(t *testing.T) {
	// Given
	fixture := newPreviousFixture(t)
	before, err := fixture.store.Snapshot(context.Background(), fixture.zoneID)
	if err != nil {
		t.Fatalf("snapshot before previous: %v", err)
	}

	// When
	result, err := fixture.store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: fixture.zoneID, IdempotencyKey: "previous-at-boundary", ExpectedRevision: before.Revision,
		Command: TransportPrevious, PositionMS: 5_000,
	})
	// Then
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	commands, err := fixture.store.PendingOutbox(context.Background(), fixture.zoneID)
	if err != nil {
		t.Fatalf("pending commands: %v", err)
	}
	command := commands[len(commands)-1]
	if result.TrackID != "first" || result.QueueEntryID != fixture.firstEntry || result.SourcePlayID != fixture.first.PlayID ||
		result.PlayID == fixture.first.PlayID || result.PlayID == fixture.second.PlayID || command.Type != "play" || command.PlayID != result.PlayID {
		t.Fatalf("previous result/command = %+v / %+v", result, command)
	}
	if state := queueStateByID(t, fixture.store, fixture.firstEntry); state != QueueCompleted {
		t.Fatalf("completed source was reactivated: %s", state)
	}
}

func Test_Transport_previous_seeks_current_to_zero_only_above_five_seconds(t *testing.T) {
	// Given
	fixture := newPreviousFixture(t)
	before, err := fixture.store.Snapshot(context.Background(), fixture.zoneID)
	if err != nil {
		t.Fatalf("snapshot before previous: %v", err)
	}

	// When
	result, err := fixture.store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: fixture.zoneID, IdempotencyKey: "previous-above-boundary", ExpectedRevision: before.Revision,
		Command: TransportPrevious, PositionMS: 5_001,
	})
	// Then
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	commands, err := fixture.store.PendingOutbox(context.Background(), fixture.zoneID)
	if err != nil {
		t.Fatalf("pending commands: %v", err)
	}
	command := commands[len(commands)-1]
	if result.PlayID != fixture.second.PlayID || result.PhysicalCommand != TransportSeek || command.Type != "seek" || command.PlayID != fixture.second.PlayID {
		t.Fatalf("previous result/command = %+v / %+v", result, command)
	}
	var payload storedRendererCommandPayload
	stmt, err := fixture.store.db.prepare("SELECT payload_json FROM renderer_outbox WHERE command_id=?")
	if err != nil {
		t.Fatalf("prepare payload query: %v", err)
	}
	defer stmt.close()
	if err := stmt.bindText(1, result.CommandID); err != nil {
		t.Fatalf("bind payload query: %v", err)
	}
	row, err := stmt.step()
	if err != nil || !row {
		t.Fatalf("load payload: row=%v err=%v", row, err)
	}
	if err := json.Unmarshal([]byte(stmt.text(0)), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.PositionMS == nil || *payload.PositionMS != 0 {
		t.Fatalf("previous seek payload = %+v", payload)
	}
}
