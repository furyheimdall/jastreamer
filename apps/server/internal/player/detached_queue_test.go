package player

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

func testTransportCalls(devices *fakeDevices) []string {
	devices.mu.Lock()
	defer devices.mu.Unlock()
	return append([]string(nil), devices.calls...)
}

func testRevokedMedia(service *Service) []string {
	media := service.media.(*fakeMedia)
	media.mu.Lock()
	defer media.mu.Unlock()
	return append([]string(nil), media.revoked...)
}

func TestRemoveCurrentPreservesLoadedPlaybackAcrossStates(t *testing.T) {
	for _, state := range []string{StatePlaying, StatePaused, StateStopped, StateUnavailable} {
		t.Run(state, func(t *testing.T) {
			service, _, devices := newPlayerTestService(t)
			ctx := context.Background()
			queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
			if err != nil {
				t.Fatal(err)
			}
			currentID := queue.Entries[0].ID
			if _, err := service.Command(ctx, Command{Action: "play", EntryID: currentID}); err != nil {
				t.Fatal(err)
			}
			runAcceptedCommand(t, service)
			binding, err := service.loadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch state {
			case StatePlaying, StatePaused:
				service.applyObservation(ctx, output.Observation{
					State: state, URI: binding.currentURI, HasURI: true,
					PositionMS: 12345, DurationMS: 100000, HasPosition: true, ObservedAt: time.Now().UTC(),
				})
			case StateUnavailable:
				service.applyObservation(ctx, output.Observation{
					State: StatePlaying, URI: binding.currentURI, HasURI: true,
					PositionMS: 12345, DurationMS: 100000, HasPosition: true, ObservedAt: time.Now().UTC(),
				})
				service.applyObservationFailure(ctx, context.DeadlineExceeded)
			default:
				if _, err := service.Command(ctx, Command{Action: "stop"}); err != nil {
					t.Fatal(err)
				}
				runAcceptedCommand(t, service)
			}
			before := service.mustState(t)
			storedBefore, err := service.loadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			callsBefore := testTransportCalls(devices)
			revokedBefore := testRevokedMedia(service)
			currentQueue, err := service.Queue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			updated, err := service.MutateQueue(ctx, QueueMutation{Action: "remove", EntryID: currentID, Revision: currentQueue.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if len(updated.Entries) != 1 || updated.Entries[0].ID != queue.Entries[1].ID {
				t.Fatalf("current removal exposed the wrong queue: %#v", updated.Entries)
			}
			after := service.mustState(t)
			storedAfter, err := service.loadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if after.State != before.State || after.CurrentEntryID != currentID || after.Track == nil || after.Track.ID != "a" ||
				after.PositionMS != before.PositionMS || after.DurationMS != before.DurationMS {
				t.Fatalf("removing %s current changed loaded playback: before=%#v after=%#v", state, before, after)
			}
			if storedAfter.playID != storedBefore.playID || storedAfter.currentURI != storedBefore.currentURI ||
				storedAfter.currentSeekable != storedBefore.currentSeekable || storedAfter.resumeRequired != storedBefore.resumeRequired {
				t.Fatalf("removing %s current changed its transport binding: before=%#v after=%#v", state, storedBefore, storedAfter)
			}
			if callsAfter := testTransportCalls(devices); !reflect.DeepEqual(callsAfter, callsBefore) {
				t.Fatalf("current removal sent a renderer command: before=%v after=%v", callsBefore, callsAfter)
			}
			if revokedAfter := testRevokedMedia(service); !reflect.DeepEqual(revokedAfter, revokedBefore) {
				t.Fatalf("current removal revoked media: before=%v after=%v", revokedBefore, revokedAfter)
			}
			if state == StatePaused {
				if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
					t.Fatal(err)
				}
				runAcceptedCommand(t, service)
				calls := testTransportCalls(devices)
				if len(calls) != len(callsBefore)+1 || calls[len(calls)-1] != "play" || service.mustState(t).CurrentEntryID != currentID {
					t.Fatalf("detached paused playback did not resume in place: calls=%v state=%#v", calls, service.mustState(t))
				}
			}
		})
	}
}

func TestClearQueueFinishesDetachedCurrentForRepeatOffAndAllAndAcceptsNewNext(t *testing.T) {
	for _, repeatMode := range []string{RepeatOff, RepeatAll} {
		t.Run(repeatMode, func(t *testing.T) {
			service, _, devices := newPlayerTestService(t)
			ctx := context.Background()
			queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
				t.Fatal(err)
			}
			runAcceptedCommand(t, service)
			binding, err := service.loadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			service.applyObservation(ctx, output.Observation{
				State: "playing", URI: binding.currentURI, HasURI: true,
				PositionMS: 50000, DurationMS: 100000, HasPosition: true, ObservedAt: time.Now().UTC(),
			})
			if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeatMode}); err != nil {
				t.Fatal(err)
			}
			callsBefore := testTransportCalls(devices)
			currentQueue, err := service.Queue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			empty, err := service.MutateQueue(ctx, QueueMutation{Action: "clear", Revision: currentQueue.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if len(empty.Entries) != 0 {
				t.Fatalf("clear retained queue entries: %#v", empty.Entries)
			}
			preserved := service.mustState(t)
			if preserved.CurrentEntryID != queue.Entries[0].ID || preserved.Track == nil || preserved.Track.ID != "a" || preserved.PositionMS != 50000 {
				t.Fatalf("clear lost the loaded current track: %#v", preserved)
			}
			ended := output.Observation{
				State: "stopped", URI: binding.currentURI, HasURI: true,
				PositionMS: 100000, DurationMS: 100000, HasPosition: true,
				CompletionKnown: true, Completed: true, ObservedAt: time.Now().UTC(),
			}
			first := service.applyObservation(ctx, ended)
			second := service.applyObservation(ctx, ended)
			if first.commandQueued || second.commandQueued {
				t.Fatalf("empty detached completion replayed under repeat %s: first=%#v second=%#v", repeatMode, first, second)
			}
			finished := service.mustState(t)
			if finished.State != StateStopped || finished.CurrentEntryID != queue.Entries[0].ID || finished.Track == nil || finished.Track.ID != "a" {
				t.Fatalf("detached completion lost current identity: %#v", finished)
			}
			if callsAfter := testTransportCalls(devices); !reflect.DeepEqual(callsAfter, callsBefore) {
				t.Fatalf("clear or natural completion sent transport commands: before=%v after=%v", callsBefore, callsAfter)
			}
			nextQueue, err := service.MutateQueue(ctx, QueueMutation{Action: "next", TrackIDs: []string{"b"}, Revision: empty.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if len(nextQueue.Entries) != 1 || nextQueue.Entries[0].TrackID != "b" {
				t.Fatalf("play-next after clear produced %#v", nextQueue.Entries)
			}
			if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
				t.Fatal(err)
			}
			runAcceptedCommand(t, service)
			if next := service.mustState(t); next.CurrentEntryID != nextQueue.Entries[0].ID || next.Track == nil || next.Track.ID != "b" {
				t.Fatalf("play-next after clear did not start the new entry: %#v", next)
			}
		})
	}
}

func TestRepeatOneReplaysClearedDetachedCurrentOnNaturalCompletion(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	currentID := queue.Entries[0].ID
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: currentID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repeat := RepeatOne
	if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeat}); err != nil {
		t.Fatal(err)
	}
	currentQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	empty, err := service.MutateQueue(ctx, QueueMutation{Action: "clear", Revision: currentQueue.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Entries) != 0 {
		t.Fatalf("clear retained queue entries: %#v", empty.Entries)
	}
	callsBefore := testTransportCalls(devices)
	ended := output.Observation{
		State: "stopped", PlayID: binding.playID, URI: binding.currentURI, HasURI: true,
		PositionMS: 100000, DurationMS: 100000, HasPosition: true,
		CompletionKnown: true, Completed: true, ObservedAt: time.Now().UTC(),
	}
	first := service.applyObservation(ctx, ended)
	second := service.applyObservation(ctx, ended)
	if !first.commandQueued || second.commandQueued {
		t.Fatalf("cleared repeat-one completion was not queued exactly once: first=%#v second=%#v", first, second)
	}
	runAcceptedCommand(t, service)
	replayed := service.mustState(t)
	if replayed.State != StateStarting || replayed.CurrentEntryID != currentID || replayed.Track == nil ||
		replayed.Track.ID != "a" || replayed.PositionMS != 0 || replayed.PendingCommand != "" || replayed.Error != "" {
		t.Fatalf("repeat one did not restart the cleared current track: %#v", replayed)
	}
	if retained, err := service.Queue(ctx); err != nil || len(retained.Entries) != 0 {
		t.Fatalf("repeat one resurrected the cleared queue: queue=%#v err=%v", retained, err)
	}
	wantCalls := append(append([]string(nil), callsBefore...), "set_uri", "play")
	if calls := testTransportCalls(devices); !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("natural repeat transport calls=%v, want %v", calls, wantCalls)
	}
	if stale := service.applyObservation(ctx, ended); stale.commandQueued {
		t.Fatalf("stale completion from the previous playback queued another repeat: %#v", stale)
	}
	afterStale := service.mustState(t)
	if afterStale.State != StateStarting || afterStale.CurrentEntryID != currentID || afterStale.Track == nil || afterStale.Track.ID != "a" {
		t.Fatalf("stale completion displaced the replayed current track: %#v", afterStale)
	}
	if calls := testTransportCalls(devices); !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("stale completion sent transport commands: got %v want %v", calls, wantCalls)
	}
}

func TestRepeatOneReplaysRemovedCurrentInsteadOfRemainingSuccessor(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	currentID := queue.Entries[0].ID
	successorID := queue.Entries[1].ID
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: currentID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repeat := RepeatOne
	if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeat}); err != nil {
		t.Fatal(err)
	}
	currentQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := service.MutateQueue(ctx, QueueMutation{Action: "remove", EntryID: currentID, Revision: currentQueue.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if len(retained.Entries) != 1 || retained.Entries[0].ID != successorID {
		t.Fatalf("current removal did not retain only its successor: %#v", retained.Entries)
	}
	ended := output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		PositionMS: 100000, DurationMS: 100000, HasPosition: true,
		CompletionKnown: true, Completed: true, ObservedAt: time.Now().UTC(),
	}
	first := service.applyObservation(ctx, ended)
	second := service.applyObservation(ctx, ended)
	if !first.commandQueued || second.commandQueued {
		t.Fatalf("removed-current repeat was not queued exactly once: first=%#v second=%#v", first, second)
	}
	runAcceptedCommand(t, service)
	replayed := service.mustState(t)
	if replayed.State != StateStarting || replayed.CurrentEntryID != currentID || replayed.Track == nil || replayed.Track.ID != "a" {
		t.Fatalf("repeat one selected the remaining successor instead of the removed current: %#v", replayed)
	}
	afterRepeat, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterRepeat.Entries) != 1 || afterRepeat.Entries[0].ID != successorID {
		t.Fatalf("repeat one reinserted the removed current: %#v", afterRepeat.Entries)
	}
	callsAfterRepeat := testTransportCalls(devices)
	if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	advanced := service.mustState(t)
	if advanced.State != StateStarting || advanced.CurrentEntryID != successorID || advanced.Track == nil || advanced.Track.ID != "b" {
		t.Fatalf("explicit Next did not escape repeat one to the retained successor: %#v", advanced)
	}
	wantCalls := append(append([]string(nil), callsAfterRepeat...), "stop", "set_uri", "play")
	if calls := testTransportCalls(devices); !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("explicit Next transport calls=%v, want %v", calls, wantCalls)
	}
}

func TestRepeatOneReplaysClearedDetachedCurrentAtConfirmedTerminalPosition(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := t.Context()
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	currentID := queue.Entries[0].ID
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: currentID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repeat := RepeatOne
	if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeat}); err != nil {
		t.Fatal(err)
	}
	currentQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MutateQueue(ctx, QueueMutation{Action: "clear", Revision: currentQueue.Revision}); err != nil {
		t.Fatal(err)
	}
	observation := output.Observation{
		State: "playing", URI: binding.currentURI, HasURI: true,
		PositionMS: 99000, DurationMS: 100000, HasPosition: true, ObservedAt: now,
	}
	service.applyObservation(ctx, observation)
	observation.PositionMS = observation.DurationMS
	if service.applyObservation(ctx, observation).commandQueued {
		t.Fatal("one terminal position queued a detached repeat")
	}
	now = now.Add(time.Second)
	observation.ObservedAt = now
	if service.applyObservation(ctx, observation).commandQueued {
		t.Fatal("detached repeat queued before terminal position was stable")
	}
	now = now.Add(time.Second)
	observation.ObservedAt = now
	if result := service.applyObservation(ctx, observation); !result.commandQueued {
		t.Fatalf("confirmed terminal position did not queue detached repeat: %#v", result)
	}
	callsBefore := testTransportCalls(devices)
	before := service.mustState(t)
	if before.State != StatePlaying || before.CurrentEntryID != currentID {
		t.Fatalf("terminal detection changed playback before Stop was acknowledged: %#v", before)
	}
	if retained, err := service.Queue(ctx); err != nil || len(retained.Entries) != 0 {
		t.Fatalf("terminal detection resurrected the cleared queue: queue=%#v err=%v", retained, err)
	}
	runAcceptedCommand(t, service)
	replayed := service.mustState(t)
	if replayed.State != StateStarting || replayed.CurrentEntryID != currentID || replayed.Track == nil || replayed.Track.ID != "a" {
		t.Fatalf("terminal-position repeat did not restart the detached current: %#v", replayed)
	}
	wantCalls := append(append([]string(nil), callsBefore...), "stop", "set_uri", "play")
	if calls := testTransportCalls(devices); !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("terminal-position repeat transport calls=%v, want %v", calls, wantCalls)
	}
	if retained, err := service.Queue(ctx); err != nil || len(retained.Entries) != 0 {
		t.Fatalf("terminal-position repeat resurrected the cleared queue: queue=%#v err=%v", retained, err)
	}
}

func TestRepeatOneTerminalReplayRequiresAcknowledgedStop(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := t.Context()
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	currentID := queue.Entries[0].ID
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: currentID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repeat := RepeatOne
	if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeat}); err != nil {
		t.Fatal(err)
	}
	currentQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MutateQueue(ctx, QueueMutation{Action: "clear", Revision: currentQueue.Revision}); err != nil {
		t.Fatal(err)
	}
	observation := output.Observation{
		State: "playing", URI: binding.currentURI, HasURI: true,
		PositionMS: 99000, DurationMS: 100000, HasPosition: true, ObservedAt: now,
	}
	service.applyObservation(ctx, observation)
	observation.PositionMS = observation.DurationMS
	service.applyObservation(ctx, observation)
	now = now.Add(2 * time.Second)
	observation.ObservedAt = now
	if result := service.applyObservation(ctx, observation); !result.commandQueued {
		t.Fatalf("confirmed terminal position did not queue detached repeat: %#v", result)
	}
	devices.mu.Lock()
	devices.fail["stop"] = &output.ActionError{Kind: output.ErrorFault, Action: "Stop", Code: 701}
	devices.mu.Unlock()
	callsBefore := testTransportCalls(devices)
	runAcceptedCommand(t, service)
	failed := service.mustState(t)
	if failed.State != StateError || failed.CurrentEntryID != currentID || failed.Track == nil ||
		failed.Track.ID != "a" || failed.PendingCommand != "" || failed.Error == "" {
		t.Fatalf("failed Stop replayed or displaced the detached current: %#v", failed)
	}
	wantCalls := append(append([]string(nil), callsBefore...), "stop")
	if calls := testTransportCalls(devices); !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("failed Stop continued into replay: got %v want %v", calls, wantCalls)
	}
	if retained, err := service.Queue(ctx); err != nil || len(retained.Entries) != 0 {
		t.Fatalf("failed Stop resurrected the cleared queue: queue=%#v err=%v", retained, err)
	}
}

func TestDetachedCurrentTraversalPreservesDuplicateEntryOrder(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "a", "b", "a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	removedID := queue.Entries[1].ID
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: removedID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	currentQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	currentQueue, err = service.MutateQueue(ctx, QueueMutation{Action: "remove", EntryID: removedID, Revision: currentQueue.Revision})
	if err != nil {
		t.Fatal(err)
	}
	currentQueue, err = service.MutateQueue(ctx, QueueMutation{Action: "move", EntryID: queue.Entries[3].ID, Index: 1, Revision: currentQueue.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if len(currentQueue.Entries) != 3 || currentQueue.Entries[1].ID != queue.Entries[3].ID {
		t.Fatalf("moving a detached successor produced %#v", currentQueue.Entries)
	}
	currentQueue, err = service.MutateQueue(ctx, QueueMutation{Action: "remove", EntryID: queue.Entries[3].ID, Revision: currentQueue.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: removedID}); faultCode(err) != "QUEUE_ENTRY_NOT_FOUND" {
		t.Fatalf("explicit play accepted removed entry: %v", err)
	}
	if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	if state.CurrentEntryID != queue.Entries[2].ID || state.Track == nil || state.Track.ID != "b" {
		t.Fatalf("next after moving and deleting detached successors selected the wrong entry: %#v", state)
	}
	if calls := testTransportCalls(devices); !reflect.DeepEqual(calls, []string{"set_uri", "play", "stop", "set_uri", "play"}) {
		t.Fatalf("detached next transport order=%v", calls)
	}
}

func TestDetachedShuffledCurrentFinishesOnceAtStoredSuccessor(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	currentID := queue.Entries[1].ID
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: currentID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	shuffle := true
	repeat := RepeatAll
	if _, err := service.UpdateMode(ctx, ModeUpdate{Shuffle: &shuffle, RepeatMode: &repeat}); err != nil {
		t.Fatal(err)
	}
	var successorID string
	if err := db.QueryRowContext(ctx, "SELECT entry_id FROM player_shuffle WHERE ordinal=1").Scan(&successorID); err != nil {
		t.Fatal(err)
	}
	currentQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MutateQueue(ctx, QueueMutation{Action: "remove", EntryID: currentID, Revision: currentQueue.Revision}); err != nil {
		t.Fatal(err)
	}
	ended := output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		PositionMS: 100000, DurationMS: 100000, HasPosition: true,
		CompletionKnown: true, Completed: true, ObservedAt: time.Now().UTC(),
	}
	first := service.applyObservation(ctx, ended)
	second := service.applyObservation(ctx, ended)
	if !first.commandQueued || second.commandQueued {
		t.Fatalf("detached shuffled EOF was not exactly once: first=%#v second=%#v", first, second)
	}
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	if state.CurrentEntryID != successorID || state.CurrentEntryID == currentID {
		t.Fatalf("detached shuffled completion did not select its stored successor: successor=%q state=%#v", successorID, state)
	}
}

func TestDetachedCurrentSurvivesRestartWithoutReplay(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service.applyObservation(ctx, output.Observation{
		State: "playing", URI: binding.currentURI, HasURI: true,
		PositionMS: 42000, DurationMS: 100000, HasPosition: true, ObservedAt: time.Now().UTC(),
	})
	repeat := RepeatAll
	if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeat}); err != nil {
		t.Fatal(err)
	}
	currentQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MutateQueue(ctx, QueueMutation{Action: "clear", Revision: currentQueue.Revision}); err != nil {
		t.Fatal(err)
	}
	callsBefore := testTransportCalls(devices)
	recovered, err := newService(ctx, db, service.lib, devices, service.media, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := recovered.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != StateUnavailable || state.CurrentEntryID != queue.Entries[0].ID || state.Track == nil || state.Track.ID != "a" ||
		state.PositionMS != 42000 || state.RepeatMode != RepeatAll || state.PendingCommand != "" {
		t.Fatalf("restart lost detached playback state: %#v", state)
	}
	if recoveredQueue, err := recovered.Queue(ctx); err != nil || len(recoveredQueue.Entries) != 0 {
		t.Fatalf("restart resurrected the cleared queue: queue=%#v err=%v", recoveredQueue, err)
	}
	if callsAfter := testTransportCalls(devices); !reflect.DeepEqual(callsAfter, callsBefore) {
		t.Fatalf("restart replayed detached current: before=%v after=%v", callsBefore, callsAfter)
	}
}

func TestDetachedCurrentFailureHistoryKeepsRemovedTrackIdentity(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	history, err := errorhistory.New(t.Context(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.history = history
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	currentQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MutateQueue(ctx, QueueMutation{Action: "remove", EntryID: queue.Entries[0].ID, Revision: currentQueue.Revision}); err != nil {
		t.Fatal(err)
	}
	result := service.applyObservation(ctx, output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true, PlayID: binding.playID,
		PositionMS: 12000, DurationMS: 100000, HasPosition: true,
		TransportStatus: "ERROR_OCCURRED", MediaFailed: true, ObservedAt: time.Now().UTC(),
	})
	service.recordPlayerHistory(result.historyRecord)
	recorded, err := history.List(ctx, errorhistory.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if recorded.Total != 1 || recorded.Items[0].TrackID != "a" {
		t.Fatalf("detached media failure lost removed-track attribution: %#v", recorded)
	}
}

func TestLegacyQueueCurrentSurvivesPlaybackSnapshotUpgrade(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b", "a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	currentID := queue.Entries[1].ID
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: currentID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service.applyObservation(ctx, output.Observation{
		State: "playing", URI: binding.currentURI, HasURI: true,
		PositionMS: 23000, DurationMS: 90000, HasPosition: true, ObservedAt: time.Now().UTC(),
	})
	callsBefore := testTransportCalls(devices)
	// Released databases have the selected queue entry but no independent snapshot.
	if _, err := db.ExecContext(ctx, "DROP TABLE player_current"); err != nil {
		t.Fatal(err)
	}
	upgraded, err := newService(ctx, db, service.lib, devices, service.media, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	state := upgraded.mustState(t)
	if state.State != StateUnavailable || state.CurrentEntryID != currentID || state.Track == nil ||
		state.Track.ID != "b" || state.PositionMS != 23000 || state.PendingCommand != "" {
		t.Fatalf("upgrade lost legacy selected playback: %#v", state)
	}
	retained, err := upgraded.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained.Entries) != 3 || retained.Entries[0].ID != queue.Entries[0].ID ||
		retained.Entries[1].ID != currentID || retained.Entries[2].ID != queue.Entries[2].ID {
		t.Fatalf("upgrade changed the duplicate-preserving queue: %#v", retained)
	}
	if _, err := upgraded.MutateQueue(ctx, QueueMutation{Action: "remove", EntryID: currentID, Revision: retained.Revision}); err != nil {
		t.Fatal(err)
	}
	if callsAfter := testTransportCalls(devices); !reflect.DeepEqual(callsAfter, callsBefore) {
		t.Fatalf("upgrade or removal started playback: before=%v after=%v", callsBefore, callsAfter)
	}
	if _, err := upgraded.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, upgraded)
	if resumed := upgraded.mustState(t); resumed.CurrentEntryID != currentID || resumed.Track == nil ||
		resumed.Track.ID != "b" || resumed.PositionMS != 23000 || resumed.State != StateStarting {
		t.Fatalf("upgraded detached track did not resume at its saved position: %#v", resumed)
	}
}
