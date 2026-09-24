package player

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func startPlayCountTrack(t *testing.T, service *Service, trackID string) storedState {
	t.Helper()
	ctx := context.Background()
	queue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Entries) == 0 {
		queue, err = service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{trackID}, Revision: queue.Revision})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	st, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func observePlayCountPosition(service *Service, st storedState, now time.Time, positionMS, durationMS int64) observationResult {
	return service.applyObservation(context.Background(), output.Observation{
		State: "playing", URI: st.currentURI, HasURI: true, PlayID: st.playID,
		PositionMS: positionMS, DurationMS: durationMS, HasPosition: true, ObservedAt: now,
	})
}

func storedPlayCount(t *testing.T, db *sql.DB, trackID string) int64 {
	t.Helper()
	var count int64
	err := db.QueryRow("SELECT play_count FROM track_play_counts WHERE track_id=?", trackID).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func TestListeningThresholdCountsEachPlayIdentityOnce(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	track := service.lib.(*fakeLibrary).tracks["a"]
	track.DurationMS = 0
	service.lib.(*fakeLibrary).tracks["a"] = track
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	st := startPlayCountTrack(t, service, "a")
	observePlayCountPosition(service, st, now, 0, 0)
	for second := int64(1); second < 30; second++ {
		now = now.Add(time.Second)
		if result := observePlayCountPosition(service, st, now, second*1000, 0); result.libraryChanged {
			t.Fatalf("play counted before threshold at %d seconds", second)
		}
	}
	if count := storedPlayCount(t, db, "a"); count != 0 {
		t.Fatalf("count before threshold = %d", count)
	}
	now = now.Add(time.Second)
	result := observePlayCountPosition(service, st, now, 30000, 0)
	if !result.libraryChanged || storedPlayCount(t, db, "a") != 1 {
		t.Fatalf("threshold observation did not persist one play: result=%#v", result)
	}
	observePlayCountPosition(service, st, now, 30000, 0)
	for second := int64(31); second <= 35; second++ {
		now = now.Add(time.Second)
		observePlayCountPosition(service, st, now, second*1000, 0)
	}
	if count := storedPlayCount(t, db, "a"); count != 1 {
		t.Fatalf("duplicate observations recounted one play identity: %d", count)
	}

	if _, err := service.Command(context.Background(), Command{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	replayed := startPlayCountTrack(t, service, "a")
	if replayed.playID == st.playID {
		t.Fatal("replay reused the prior play identity")
	}
	observePlayCountPosition(service, replayed, now, 0, 0)
	for second := int64(1); second <= 30; second++ {
		now = now.Add(time.Second)
		observePlayCountPosition(service, replayed, now, second*1000, 0)
	}
	if count := storedPlayCount(t, db, "a"); count != 2 {
		t.Fatalf("genuine replay count = %d, want 2", count)
	}
}

func TestListeningPauseRetainsPartialWithoutCreditingPause(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	now := time.Date(2026, time.September, 24, 13, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	st := startPlayCountTrack(t, service, "a")
	observePlayCountPosition(service, st, now, 0, 100000)
	for second := int64(1); second <= 15; second++ {
		now = now.Add(time.Second)
		observePlayCountPosition(service, st, now, second*1000, 100000)
	}
	if _, err := service.Command(context.Background(), Command{Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	service.applyObservation(context.Background(), output.Observation{
		State: "paused", URI: st.currentURI, HasURI: true, PlayID: st.playID,
		PositionMS: 15000, DurationMS: 100000, HasPosition: true, ObservedAt: now,
	})
	now = now.Add(20 * time.Second)
	service.applyObservation(context.Background(), output.Observation{
		State: "paused", URI: st.currentURI, HasURI: true, PlayID: st.playID,
		PositionMS: 15000, DurationMS: 100000, HasPosition: true, ObservedAt: now,
	})
	if count := storedPlayCount(t, db, "a"); count != 0 {
		t.Fatalf("pause time was credited: %d", count)
	}
	if _, err := service.Command(context.Background(), Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	observePlayCountPosition(service, st, now, 15000, 100000)
	for second := int64(16); second <= 30; second++ {
		now = now.Add(time.Second)
		observePlayCountPosition(service, st, now, second*1000, 100000)
	}
	if count := storedPlayCount(t, db, "a"); count != 1 {
		t.Fatalf("resumed listening did not retain qualified partial time: %d", count)
	}
}

func TestListeningCompletionCreditsFinalIntervalButSeekToEOFDoesNot(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	track := service.lib.(*fakeLibrary).tracks["a"]
	track.DurationMS = 40000
	service.lib.(*fakeLibrary).tracks["a"] = track
	now := time.Date(2026, time.September, 24, 14, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	first := startPlayCountTrack(t, service, "a")
	observePlayCountPosition(service, first, now, 0, 40000)
	now = now.Add(time.Second)
	observePlayCountPosition(service, first, now, 40000, 40000)
	now = now.Add(time.Second)
	service.applyObservation(context.Background(), output.Observation{
		State: "stopped", URI: first.currentURI, HasURI: true, PlayID: first.playID,
		DurationMS: 40000, ObservedAt: now, CompletionKnown: true, Completed: true,
	})
	if count := storedPlayCount(t, db, "a"); count != 0 {
		t.Fatalf("seek to EOF was counted: %d", count)
	}

	second := startPlayCountTrack(t, service, "a")
	observePlayCountPosition(service, second, now, 20000, 40000)
	for position := int64(21000); position <= 39000; position += 1000 {
		now = now.Add(time.Second)
		observePlayCountPosition(service, second, now, position, 40000)
	}
	now = now.Add(time.Second)
	result := service.applyObservation(context.Background(), output.Observation{
		State: "stopped", URI: second.currentURI, HasURI: true, PlayID: second.playID,
		DurationMS: 40000, ObservedAt: now, CompletionKnown: true, Completed: true,
	})
	if !result.libraryChanged || storedPlayCount(t, db, "a") != 1 {
		t.Fatalf("valid final interval did not qualify short track: result=%#v", result)
	}
}

func TestListeningFailureAndLongGapDoNotCreditMissingProgress(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	now := time.Date(2026, time.September, 24, 15, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	st := startPlayCountTrack(t, service, "a")
	observePlayCountPosition(service, st, now, 0, 100000)
	for second := int64(1); second <= 20; second++ {
		now = now.Add(time.Second)
		observePlayCountPosition(service, st, now, second*1000, 100000)
	}
	for range 10 {
		now = now.Add(time.Second)
		observePlayCountPosition(service, st, now, 20000, 100000)
	}
	if count := storedPlayCount(t, db, "a"); count != 0 {
		t.Fatalf("wall time without progress was credited: %d", count)
	}
	service.applyObservationFailure(context.Background(), errors.New("observation failed"))
	now = now.Add(20 * time.Second)
	observePlayCountPosition(service, st, now, 40000, 100000)
	if count := storedPlayCount(t, db, "a"); count != 0 {
		t.Fatalf("failed-observation gap was credited: %d", count)
	}
	for position := int64(41000); position <= 50000; position += 1000 {
		now = now.Add(time.Second)
		observePlayCountPosition(service, st, now, position, 100000)
	}
	if count := storedPlayCount(t, db, "a"); count != 1 {
		t.Fatalf("valid listening around failure was not accumulated: %d", count)
	}
}
