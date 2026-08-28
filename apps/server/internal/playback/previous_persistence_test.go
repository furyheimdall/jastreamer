package playback

import (
	"context"
	"errors"
	"testing"
	"time"
)

func Test_Transport_previous_replays_exact_history_selection_after_restart(t *testing.T) {
	// Given
	fixture := newPreviousFixture(t)
	before, err := fixture.store.Snapshot(context.Background(), fixture.zoneID)
	if err != nil {
		t.Fatalf("snapshot before previous: %v", err)
	}
	request := TransportMutationRequest{
		ZoneID: fixture.zoneID, IdempotencyKey: "restart-previous", ExpectedRevision: before.Revision,
		Command: TransportPrevious, PositionMS: 5_000,
	}
	first, err := fixture.store.MutateTransport(context.Background(), request)
	if err != nil {
		t.Fatalf("previous: %v", err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted := openTestStore(t, fixture.config)

	// When
	replay, err := restarted.MutateTransport(context.Background(), request)
	// Then
	if err != nil {
		t.Fatalf("replay previous: %v", err)
	}
	if !replay.Replayed || replay.CommandID != first.CommandID || replay.PlayID != first.PlayID ||
		replay.TrackID != first.TrackID || replay.QueueEntryID != first.QueueEntryID || replay.SourcePlayID != first.SourcePlayID {
		t.Fatalf("first/replay = %+v / %+v", first, replay)
	}
	commands, err := restarted.PendingOutbox(context.Background(), fixture.zoneID)
	if err != nil || len(commands) != 1 {
		t.Fatalf("restarted pending commands = %+v (%v)", commands, err)
	}
}

func Test_Transport_previous_uses_removed_history_without_reactivating_source(t *testing.T) {
	// Given
	fixture := newPreviousFixture(t)
	if err := fixture.store.db.exec("UPDATE playback_queue SET state='removed' WHERE entry_id='" + string(fixture.firstEntry) + "'"); err != nil {
		t.Fatalf("mark source removed: %v", err)
	}
	before, err := fixture.store.Snapshot(context.Background(), fixture.zoneID)
	if err != nil {
		t.Fatalf("snapshot before previous: %v", err)
	}

	// When
	result, err := fixture.store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: fixture.zoneID, IdempotencyKey: "removed-history", ExpectedRevision: before.Revision,
		Command: TransportPrevious, PositionMS: 0,
	})

	// Then
	if err != nil || result.QueueEntryID != fixture.firstEntry || result.TrackID != "first" {
		t.Fatalf("removed history result = %+v (%v)", result, err)
	}
	state := queueStateByID(t, fixture.store, fixture.firstEntry)
	if state != QueueRemoved {
		t.Fatalf("removed source was reactivated: %s", state)
	}
	if queueStateByID(t, fixture.store, fixture.secondEntry) != QueueCompleted {
		t.Fatal("superseded current queue entry was not terminalized")
	}
}

func Test_Transport_previous_failures_are_typed_and_do_not_consume_history(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*testing.T, previousFixture) Revision
		wantError error
	}{
		{
			name: "stale revision",
			prepare: func(t *testing.T, fixture previousFixture) Revision {
				t.Helper()
				snapshot, err := fixture.store.Snapshot(context.Background(), fixture.zoneID)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				return snapshot.Revision - 1
			},
			wantError: ErrRevisionConflict,
		},
		{
			name: "unsupported historical play",
			prepare: func(t *testing.T, fixture previousFixture) Revision {
				t.Helper()
				if _, err := fixture.store.UpsertCustomRenderer(context.Background(), CustomRenderer{
					ID: "previous-renderer", DisplayName: "Renderer", State: RendererConnected, ProtocolMajor: 3,
					Capabilities: []string{"command:seek"}, LastSeenAt: time.Date(2026, 8, 25, 12, 1, 0, 0, time.UTC),
				}); err != nil {
					t.Fatalf("remove play capability: %v", err)
				}
				snapshot, err := fixture.store.Snapshot(context.Background(), fixture.zoneID)
				if err != nil {
					t.Fatalf("snapshot: %v", err)
				}
				return snapshot.Revision
			},
			wantError: ErrUnsupportedCapability,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture := newPreviousFixture(t)
			revision := test.prepare(t, fixture)
			before := availableHistoryCount(t, fixture.store, fixture.zoneID)

			// When
			_, err := fixture.store.MutateTransport(context.Background(), TransportMutationRequest{
				ZoneID: fixture.zoneID, IdempotencyKey: "failed-previous", ExpectedRevision: revision,
				Command: TransportPrevious, PositionMS: 5_000,
			})

			// Then
			if !errors.Is(err, test.wantError) {
				t.Fatalf("previous error = %v, want %v", err, test.wantError)
			}
			after := availableHistoryCount(t, fixture.store, fixture.zoneID)
			if after != before {
				t.Fatalf("failed previous consumed history: before=%d after=%d", before, after)
			}
		})
	}
}

func Test_Transport_previous_invalid_state_is_typed_and_does_not_consume_history(t *testing.T) {
	// Given
	fixture := newPreviousFixture(t)
	if err := fixture.store.Stop(context.Background(), fixture.zoneID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	stopped, err := fixture.store.Snapshot(context.Background(), fixture.zoneID)
	if err != nil {
		t.Fatalf("snapshot stopped: %v", err)
	}
	before := availableHistoryCount(t, fixture.store, fixture.zoneID)

	// When
	_, err = fixture.store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: fixture.zoneID, IdempotencyKey: "invalid-state-previous", ExpectedRevision: stopped.Revision,
		Command: TransportPrevious, PositionMS: 0,
	})

	// Then
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid-state previous error = %v", err)
	}
	if after := availableHistoryCount(t, fixture.store, fixture.zoneID); after != before {
		t.Fatalf("invalid-state previous consumed history: before=%d after=%d", before, after)
	}
}

func Test_Transport_previous_without_history_is_typed_and_zero_mutation(t *testing.T) {
	// Given
	fixture := newPreviousFixture(t)
	if err := fixture.store.db.exec("UPDATE playback_previous_history SET consumed_revision=999 WHERE zone_id='previous-zone'"); err != nil {
		t.Fatalf("consume fixture history: %v", err)
	}
	before, err := fixture.store.Snapshot(context.Background(), fixture.zoneID)
	if err != nil {
		t.Fatalf("snapshot before previous: %v", err)
	}

	// When
	_, err = fixture.store.MutateTransport(context.Background(), TransportMutationRequest{
		ZoneID: fixture.zoneID, IdempotencyKey: "missing-history", ExpectedRevision: before.Revision,
		Command: TransportPrevious, PositionMS: 0,
	})

	// Then
	if !errors.Is(err, ErrPlaybackHistoryEmpty) {
		t.Fatalf("missing history error = %v", err)
	}
	after, snapshotErr := fixture.store.Snapshot(context.Background(), fixture.zoneID)
	if snapshotErr != nil || after.Revision != before.Revision || after.CurrentPlay != before.CurrentPlay || after.Transport != before.Transport {
		t.Fatalf("missing history mutated state: before=%+v after=%+v (%v)", before, after, snapshotErr)
	}
}

func Test_Transport_duplicate_ended_records_one_prior_history_identity(t *testing.T) {
	// Given
	fixture := newPreviousFixture(t)
	request := NextRequest{ZoneID: fixture.zoneID, Boundary: Boundary{ID: "duplicate-ended", PreviousPlayID: fixture.second.PlayID}}

	// When
	first, firstErr := fixture.store.CommitNext(context.Background(), request)
	replay, replayErr := fixture.store.CommitNext(context.Background(), request)

	// Then
	if firstErr != nil || replayErr != nil || replay.ID != first.ID {
		t.Fatalf("ended/replay = %+v (%v) / %+v (%v)", first, firstErr, replay, replayErr)
	}
	if count := historySourceCount(t, fixture.store, fixture.second.PlayID); count != 1 {
		t.Fatalf("duplicate ended history count = %d", count)
	}
}

func queueStateByID(t *testing.T, store *Store, entryID QueueEntryID) QueueState {
	t.Helper()
	stmt, err := store.db.prepare("SELECT state FROM playback_queue WHERE entry_id=?")
	if err != nil {
		t.Fatalf("prepare queue state: %v", err)
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(entryID)); err != nil {
		t.Fatalf("bind queue state: %v", err)
	}
	row, err := stmt.step()
	if err != nil || !row {
		t.Fatalf("queue state row=%v err=%v", row, err)
	}
	return QueueState(stmt.text(0))
}

func availableHistoryCount(t *testing.T, store *Store, zoneID ZoneID) int64 {
	t.Helper()
	return scalarCount(t, store, "SELECT count(*) FROM playback_previous_history WHERE zone_id=? AND consumed_revision IS NULL", string(zoneID))
}

func historySourceCount(t *testing.T, store *Store, playID PlayID) int64 {
	t.Helper()
	return scalarCount(t, store, "SELECT count(*) FROM playback_previous_history WHERE source_play_id=?", string(playID))
}

func scalarCount(t *testing.T, store *Store, query, value string) int64 {
	t.Helper()
	stmt, err := store.db.prepare(query)
	if err != nil {
		t.Fatalf("prepare count: %v", err)
	}
	defer stmt.close()
	if err := stmt.bindText(1, value); err != nil {
		t.Fatalf("bind count: %v", err)
	}
	row, err := stmt.step()
	if err != nil || !row {
		t.Fatalf("count row=%v err=%v", row, err)
	}
	return stmt.int64(0)
}
