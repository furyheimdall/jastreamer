package player

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
	_ "modernc.org/sqlite"
)

type fakeLibrary struct {
	tracks map[string]library.Track
}

func (f *fakeLibrary) Track(_ context.Context, id string) (library.Track, error) {
	track, ok := f.tracks[id]
	if !ok {
		return library.Track{}, errors.New("not found")
	}
	return track, nil
}

type fakeDevices struct {
	mu          sync.Mutex
	device      output.Device
	observation output.Observation
	fail        map[string]error
	calls       []string
}

func (f *fakeDevices) Device(id string) (output.Device, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.device, id == f.device.ID
}

func (f *fakeDevices) action(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	return f.fail[name]
}

func (f *fakeDevices) SetURI(context.Context, string, output.Resource) error {
	return f.action("set_uri")
}
func (f *fakeDevices) Play(context.Context, string) error  { return f.action("play") }
func (f *fakeDevices) Pause(context.Context, string) error { return f.action("pause") }
func (f *fakeDevices) Stop(context.Context, string) error  { return f.action("stop") }
func (f *fakeDevices) Seek(_ context.Context, _ string, position int64) error {
	return f.action(fmt.Sprintf("seek:%d", position))
}
func (f *fakeDevices) Observe(context.Context, string) (output.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.observation, f.fail["observe"]
}
func (f *fakeDevices) Pair(_ context.Context, id string, _ output.PairingRequest) (output.PairingStatus, error) {
	if _, found := f.Device(id); !found {
		return output.PairingStatus{}, output.ErrNotFound
	}
	return output.PairingStatus{Required: false}, f.action("pair")
}

type fakeMedia struct {
	mu         sync.Mutex
	revoked    []string
	unseekable bool
}

func (f *fakeMedia) Prepare(_ context.Context, _ output.Device, track library.Track, playID string) (output.Resource, error) {
	f.mu.Lock()
	unseekable := f.unseekable
	f.mu.Unlock()
	return output.Resource{URL: "http://10.0.0.2/media/" + playID, Mime: track.Mime, DurationMS: track.DurationMS, Seekable: !unseekable}, nil
}
func (f *fakeMedia) Revoke(playID string) {
	f.mu.Lock()
	f.revoked = append(f.revoked, playID)
	f.mu.Unlock()
}

func newPlayerTestService(t *testing.T) (*Service, *sql.DB, *fakeDevices) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	lib := &fakeLibrary{tracks: map[string]library.Track{
		"a": {ID: "a", Title: "A", DurationMS: 100000, Mime: "audio/flac", Available: true},
		"b": {ID: "b", Title: "B", DurationMS: 90000, Mime: "audio/mpeg", Available: true},
	}}
	devices := &fakeDevices{device: output.Device{
		ID: "renderer", Online: true,
		Capabilities: output.Capabilities{Play: true, Pause: true, Stop: true, Seek: true},
	}, fail: make(map[string]error)}
	service, err := newService(context.Background(), db, lib, devices, &fakeMedia{}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectOutput(context.Background(), "renderer"); err != nil {
		t.Fatal(err)
	}
	return service, db, devices
}

func runAcceptedCommand(t *testing.T, service *Service) {
	t.Helper()
	command, found, err := service.claimCommand(context.Background())
	if err != nil || !found {
		t.Fatalf("claim command: found=%v err=%v", found, err)
	}
	service.executeSafely(context.Background(), command)
}

type failingPreparation struct {
	fakeMedia
	err error
}

func (f *failingPreparation) Prepare(context.Context, output.Device, library.Track, string) (output.Resource, error) {
	return output.Resource{}, f.err
}

func TestStartFailurePreservesSafeCauseAndQueue(t *testing.T) {
	for _, test := range []struct {
		name, command, stage, code, failedAction string
	}{
		{"media preparation", "play", "PrepareMedia", "MEDIA_STALE", ""},
		{"URI rejection", "play", "SetAVTransportURI", "714", "set_uri"},
		{"play rejection", "play", "Play", "701", "play"},
		{"next rejection", "next", "SetAVTransportURI", "714", "set_uri"},
		{"previous rejection", "previous", "SetAVTransportURI", "714", "set_uri"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, devices := newPlayerTestService(t)
			ctx := context.Background()
			queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b", "a"}, Revision: 0})
			if err != nil {
				t.Fatal(err)
			}
			if test.command != "play" {
				current := 0
				if test.command == "previous" {
					current = 1
				}
				if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[current].ID}); err != nil {
					t.Fatal(err)
				}
				runAcceptedCommand(t, service)
			}
			const privateCause = "http://private-endpoint/media/secret-grant"
			if test.failedAction == "" {
				service.media = &failingPreparation{err: fmt.Errorf("%s: %w", privateCause, fault.New(409, "MEDIA_STALE", "track changed since the last library scan"))}
			} else {
				code := 714
				if test.failedAction == "play" {
					code = 701
				}
				devices.fail[test.failedAction] = output.NewActionError(output.ErrorFault, test.stage, code, errors.New(privateCause))
			}
			if _, err := service.Command(ctx, Command{Action: test.command}); err != nil {
				t.Fatal(err)
			}
			runAcceptedCommand(t, service)
			state := service.mustState(t)
			if state.State != StateError || !strings.Contains(state.Error, test.stage) || !strings.Contains(state.Error, test.code) {
				t.Fatalf("start failure lost its actionable cause: %+v", state)
			}
			if strings.Contains(state.Error, privateCause) || strings.Contains(state.Error, "secret-grant") {
				t.Fatalf("start failure exposed private transport details: %s", state.Error)
			}
			finished, err := service.Queue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(finished.Entries) != len(queue.Entries) {
				t.Fatalf("start failure changed the queue: %+v", finished)
			}
			for index, entry := range queue.Entries {
				if finished.Entries[index].ID != entry.ID {
					t.Fatalf("start failure reordered or replaced a queue entry: %+v", finished)
				}
			}
		})
	}
}
func TestObservationFailurePreservesOngoingPlaybackWithoutReplay(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.observation = output.Observation{State: "playing", URI: binding.currentURI, HasURI: true, HasPosition: true, PositionMS: 10000, DurationMS: 100000}
	service.observeSelected(ctx)
	calls := len(devices.calls)
	devices.fail["observe"] = output.NewActionError(output.ErrorTimeout, "Observe", 0, context.DeadlineExceeded)
	service.observeSelected(ctx)
	uncertain := service.mustState(t)
	queue, err = service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if uncertain.State != StateUnavailable || uncertain.Error != "" || uncertain.StatusWarning != nil || queue.Entries[0].Status != EntryPlaying {
		t.Fatalf("failed status query interrupted the ongoing track: state=%+v queue=%+v", uncertain, queue)
	}
	if len(service.media.(*fakeMedia).revoked) != 0 {
		t.Fatal("failed status query revoked the renderer's ongoing media")
	}
	delete(devices.fail, "observe")
	devices.observation.PositionMS = 11000
	service.observeSelected(ctx)
	recovered := service.mustState(t)
	if recovered.State != StatePlaying || recovered.PositionMS != 11000 || recovered.Error != "" || recovered.StatusWarning != nil || len(devices.calls) != calls {
		t.Fatalf("status recovery did not observe existing playback without replay: %+v", recovered)
	}
	devices.device.Online = false
	service.observeSelected(ctx)
	queue, err = service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Entries[0].Status != EntryPending || len(service.media.(*fakeMedia).revoked) != 1 {
		t.Fatal("actual renderer disconnection did not revoke playback and require explicit resume")
	}
}

func TestObservationWarningRequiresConsecutiveFailuresAndClearsOnRecovery(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := context.Background()
	if _, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.observation = output.Observation{State: "playing", URI: binding.currentURI, HasURI: true, HasPosition: true, PositionMS: 10000, DurationMS: 100000}
	service.observeSelected(ctx)
	mutationCalls := len(devices.calls)
	var previousWarningID int64
	for range 2 {
		devices.fail["observe"] = output.NewActionError(output.ErrorTransport, "GetPositionInfo", 0, errors.New("connection reset"))
		var warningID, warningRevision int64
		for attempt := 1; attempt <= 4; attempt++ {
			service.observeSelected(ctx)
			state := service.mustState(t)
			if state.Error != "" {
				t.Fatalf("status failure was exposed as an immediate command error: %+v", state)
			}
			if attempt < 3 {
				if state.StatusWarning != nil {
					t.Fatalf("warning appeared after only %d consecutive failures: %+v", attempt, state)
				}
				continue
			}
			if state.StatusWarning == nil {
				t.Fatalf("missing warning after %d consecutive failures: %+v", attempt, state)
			}
			if attempt == 3 {
				warningID, warningRevision = state.StatusWarning.ID, state.Revision
				if warningID == previousWarningID {
					t.Fatal("new failure streak reused the dismissed warning's identity")
				}
			} else if state.StatusWarning.ID != warningID || state.Revision != warningRevision {
				t.Fatal("continued failure republished the same warning as a new episode")
			}
		}
		previousWarningID = warningID
		delete(devices.fail, "observe")
		devices.observation.PositionMS += 1000
		service.observeSelected(ctx)
		recovered := service.mustState(t)
		if recovered.StatusWarning != nil || recovered.State != StatePlaying || recovered.PositionMS != devices.observation.PositionMS {
			t.Fatalf("successful observation did not clear the warning and recover position: %+v", recovered)
		}
	}
	if len(devices.calls) != mutationCalls || len(service.media.(*fakeMedia).revoked) != 0 {
		t.Fatal("status warning or recovery sent a playback command or revoked the stream")
	}
}

func TestExplicitNextStopsTheBindingAfterObservationFailure(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	devices.fail["observe"] = output.NewActionError(output.ErrorTimeout, "Observe", 0, context.DeadlineExceeded)
	service.observeSelected(ctx)
	if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	if strings.Join(devices.calls, ",") != "set_uri,play,stop,set_uri,play" {
		t.Fatalf("uncertain playback was replaced without stopping it first: %v", devices.calls)
	}
	if service.mustState(t).CurrentEntryID != queue.Entries[1].ID {
		t.Fatal("explicit next did not select the successor")
	}
}

func TestTerminalPositionRequiresStableOwnedPlaybackAndStopsBeforeAdvancing(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := context.Background()
	now := time.Now()
	service.now = func() time.Time { return now }
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observation := output.Observation{State: "playing", URI: binding.currentURI, HasURI: true, HasPosition: true, PositionMS: 99000, DurationMS: 100000, ObservedAt: now}
	service.applyObservation(ctx, observation)
	observation.PositionMS = 100000
	if service.applyObservation(ctx, observation).commandQueued {
		t.Fatal("one rounded terminal position was treated as EOF")
	}
	now = now.Add(time.Second)
	if service.applyObservation(ctx, observation).commandQueued {
		t.Fatal("terminal position advanced before the quantization grace")
	}
	observation.State = "paused"
	now = now.Add(3 * time.Second)
	if service.applyObservation(ctx, observation).commandQueued {
		t.Fatal("paused terminal position advanced")
	}
	observation.State = "playing"
	service.applyObservation(ctx, observation)
	service.applyObservation(ctx, observation)
	now = now.Add(2 * time.Second)
	if !service.applyObservation(ctx, observation).commandQueued {
		t.Fatal("renderer held PLAYING at the confirmed terminal position without advancing")
	}
	before, err := service.Queue(ctx)
	if err != nil || before.Entries[0].Status != EntryPlaying {
		t.Fatal("terminal position completed the track before Stop was acknowledged")
	}
	runAcceptedCommand(t, service)
	if service.mustState(t).CurrentEntryID != queue.Entries[1].ID || strings.Join(devices.calls, ",") != "set_uri,play,stop,set_uri,play" {
		t.Fatalf("terminal advance did not stop before the successor: %v", devices.calls)
	}
}

func TestQueuePreservesDuplicatesOrderAndCurrentCursor(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Entries) != 3 || queue.Entries[0].TrackID != "a" || queue.Entries[1].TrackID != "a" || queue.Entries[0].ID == queue.Entries[1].ID {
		t.Fatalf("duplicate queue entries were not preserved independently: %#v", queue.Entries)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[1].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	queue, err = service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.MutateQueue(ctx, QueueMutation{Action: "remove", EntryID: queue.Entries[1].ID, Revision: queue.Revision}); faultCode(err) != "CURRENT_ENTRY_IMMUTABLE" {
		t.Fatalf("remove current error=%v", err)
	}
	queue, err = service.MutateQueue(ctx, QueueMutation{Action: "clear", Revision: queue.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Entries) != 2 || queue.Entries[1].ID != service.mustState(t).CurrentEntryID {
		t.Fatalf("clear did not preserve history and current cursor: %#v", queue.Entries)
	}
}

func TestQueueRevisionSerializesConcurrentEditors(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := context.Background()
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, trackID := range []string{"a", "b"} {
		trackID := trackID
		go func() {
			<-start
			_, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{trackID}, Revision: 0})
			results <- err
		}()
	}
	close(start)
	first, second := <-results, <-results
	if (first == nil) == (second == nil) {
		t.Fatalf("expected one commit and one conflict, got %v and %v", first, second)
	}
	conflict := first
	if conflict == nil {
		conflict = second
	}
	if faultCode(conflict) != "REVISION_CONFLICT" {
		t.Fatalf("losing editor error=%v", conflict)
	}
}

func TestFailedRendererActionAlwaysResolvesDurableIntent(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	devices.fail["set_uri"] = errors.New("renderer rejected URI")
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	if state.PendingCommand != "" || state.State != StateError {
		t.Fatalf("failed action remained active: %#v", state)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM player_commands ORDER BY created_at DESC LIMIT 1").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "failed" {
		t.Fatalf("command outcome=%q", status)
	}
}

func TestOfflineStopEndsServerIntentWithoutClaimingRendererAck(t *testing.T) {
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
	before, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.mu.Lock()
	devices.device.Online = false
	devices.mu.Unlock()
	if _, err := service.Command(ctx, Command{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	if state.State != StateStopped || state.PendingCommand != "" || state.CurrentEntryID != queue.Entries[0].ID || state.Error == "" {
		t.Fatalf("offline stop did not preserve a selectable stopped cursor: %#v", state)
	}
	warning := state.Error
	for range 3 {
		service.observeSelected(ctx)
		state = service.mustState(t)
		if state.State != StateStopped || state.Error != warning || state.CurrentEntryID != queue.Entries[0].ID {
			t.Fatalf("later offline poll undid explicit stopped intent: %#v", state)
		}
	}
	var outcome string
	if err := db.QueryRow("SELECT status FROM player_commands ORDER BY created_at DESC LIMIT 1").Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "unknown" {
		t.Fatalf("offline physical stop was incorrectly acknowledged: %q", outcome)
	}
	media := service.media.(*fakeMedia)
	media.mu.Lock()
	revoked := append([]string(nil), media.revoked...)
	media.mu.Unlock()
	if len(revoked) == 0 || revoked[len(revoked)-1] != before.playID {
		t.Fatalf("offline stop did not revoke active media binding: %v", revoked)
	}
	if _, err := service.SelectOutput(ctx, "renderer"); err != nil {
		t.Fatalf("later offline polls continued to block output selection: %v", err)
	}
}

func TestRestartMarksUnknownIntentWithoutReplay(t *testing.T) {
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
	if _, err := service.Command(ctx, Command{Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	beforeCalls := len(devices.calls)
	recovered, err := newService(ctx, db, service.lib, devices, service.media, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := recovered.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.PendingCommand != "" || state.State != StateUnavailable || state.CurrentEntryID != queue.Entries[0].ID || len(devices.calls) != beforeCalls {
		t.Fatalf("restart replayed or lost recovery state: %#v calls=%v", state, devices.calls)
	}
	recoveredQueue, err := recovered.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recoveredQueue.Entries) != 1 || recoveredQueue.Entries[0].ID != queue.Entries[0].ID || recoveredQueue.Entries[0].Status != EntryPending {
		t.Fatalf("restart did not preserve queue/cursor: %#v", recoveredQueue)
	}
	var status string
	if err := db.QueryRow("SELECT status FROM player_commands WHERE action='pause'").Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "unknown" {
		t.Fatalf("interrupted outcome=%q", status)
	}
}

func TestRecoveredStopClearsServerIntentWithoutClaimingRendererAcknowledgement(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
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
	recovered, err := newService(ctx, db, service.lib, devices, service.media, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	devices.mu.Lock()
	devices.fail["stop"] = output.NewActionError(output.ErrorUnsupported, "Stop", 0, errors.New("no owned media"))
	beforeCalls := len(devices.calls)
	devices.mu.Unlock()
	if _, err := recovered.Command(ctx, Command{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, recovered)
	state := recovered.mustState(t)
	if state.State != StateStopped || state.Error == "" || state.CurrentEntryID != queue.Entries[0].ID {
		t.Fatalf("recovered stop did not leave a selectable cursor with an honest warning: %#v", state)
	}
	if err := recovered.StopForRestart(ctx); err == nil {
		t.Fatal("server restart accepted a prior unconfirmed physical stop")
	}
	devices.mu.Lock()
	afterCalls := len(devices.calls)
	devices.mu.Unlock()
	if afterCalls != beforeCalls {
		t.Fatalf("recovered stop attempted an unowned renderer command: before=%d after=%d", beforeCalls, afterCalls)
	}
	finished, err := recovered.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(finished.Entries) != 2 || finished.Entries[0].ID != queue.Entries[0].ID ||
		finished.Entries[0].Status != EntryPending || finished.Entries[1].ID != queue.Entries[1].ID ||
		finished.Entries[1].Status != EntryPending {
		t.Fatalf("recovered stop changed queue ownership: %#v", finished)
	}
	var outcome string
	if err := db.QueryRow("SELECT status FROM player_commands WHERE action='stop' ORDER BY created_at DESC LIMIT 1").Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if outcome != "unknown" {
		t.Fatalf("unconfirmed recovered stop outcome=%q, want unknown", outcome)
	}
	media := service.media.(*fakeMedia)
	media.mu.Lock()
	revoked := append([]string(nil), media.revoked...)
	media.mu.Unlock()
	if len(revoked) != 1 || revoked[0] != binding.playID {
		t.Fatalf("recovered stop did not revoke the stale media grant: %v", revoked)
	}
	if _, err := recovered.SelectOutput(ctx, "renderer"); err != nil {
		t.Fatalf("recovered stop continued to block output selection: %v", err)
	}
}

func TestRecoveredPlayReplacesServerIntentWithoutStoppingUnownedMedia(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
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
	recovered, err := newService(ctx, db, service.lib, devices, service.media, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	devices.mu.Lock()
	devices.fail["stop"] = output.NewActionError(output.ErrorUnsupported, "Stop", 0, errors.New("no owned media"))
	beforeCalls := len(devices.calls)
	devices.mu.Unlock()
	if _, err := recovered.Command(ctx, Command{Action: "play", EntryID: queue.Entries[1].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, recovered)
	state := recovered.mustState(t)
	if state.State != StateStarting || state.Error != "" || state.CurrentEntryID != queue.Entries[1].ID {
		t.Fatalf("recovered play did not establish the requested queue binding: %#v", state)
	}
	devices.mu.Lock()
	calls := append([]string(nil), devices.calls[beforeCalls:]...)
	devices.mu.Unlock()
	if len(calls) != 2 || calls[0] != "set_uri" || calls[1] != "play" {
		t.Fatalf("recovered play renderer calls=%v, want new load and play without stop", calls)
	}
	finished, err := recovered.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(finished.Entries) != 2 || finished.Entries[0].ID != queue.Entries[0].ID ||
		finished.Entries[0].Status != EntryPending || finished.Entries[1].ID != queue.Entries[1].ID ||
		finished.Entries[1].Status != EntryPlaying {
		t.Fatalf("recovered play changed queue ownership: %#v", finished)
	}
	media := service.media.(*fakeMedia)
	media.mu.Lock()
	revoked := append([]string(nil), media.revoked...)
	media.mu.Unlock()
	if len(revoked) != 1 || revoked[0] != binding.playID {
		t.Fatalf("recovered play did not revoke the stale media grant: %v", revoked)
	}
}

func TestNaturalEndRequiresReliableEvidenceAndAdvancesExactlyOnce(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = db.Exec(`UPDATE player_state SET current_entry_id=?,play_id='play_one',current_uri='http://10.0.0.2/media/one',current_seekable=1,state='playing',position_ms=99000,duration_ms=100000,last_observed_state='playing',last_observed_uri='http://10.0.0.2/media/one',last_observed_position_ms=99000,last_observed_duration_ms=100000,last_observed_has_position=1,last_observed_at=?,resume_required=0,control_action='' WHERE singleton=1`, queue.Entries[0].ID, startedAt)
	if err != nil {
		t.Fatal(err)
	}
	observation := output.Observation{State: "stopped", URI: "", HasURI: true, ObservedAt: time.Now().UTC()}
	first := service.applyObservation(ctx, observation)
	second := service.applyObservation(ctx, observation)
	if !first.commandQueued || second.commandQueued {
		t.Fatalf("natural end dispatch flags first=%#v second=%#v", first, second)
	}
	var commands, ends int
	if err := db.QueryRow("SELECT COUNT(*) FROM player_commands WHERE action='natural_next'").Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM player_natural_ends WHERE play_id='play_one'").Scan(&ends); err != nil {
		t.Fatal(err)
	}
	if commands != 1 || ends != 1 {
		t.Fatalf("natural end was not exactly once: commands=%d ends=%d", commands, ends)
	}
}

func TestQueueRunsBrowserIndependentlyThroughNaturalEnd(t *testing.T) {
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
	for index, duration := range []int64{100000, 90000} {
		stored, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		service.applyObservation(ctx, output.Observation{State: "playing", URI: stored.currentURI, HasURI: true, PositionMS: duration - 1000, DurationMS: duration, HasPosition: true, ObservedAt: now})
		ended := service.applyObservation(ctx, output.Observation{State: "stopped", URI: "", HasURI: true, DurationMS: duration, ObservedAt: now.Add(time.Second)})
		if index == 0 {
			if !ended.commandQueued {
				t.Fatal("first natural end did not enqueue the stored next entry")
			}
			runAcceptedCommand(t, service)
			started, err := service.loadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if started.currentEntryID != queue.Entries[1].ID || started.playID == "" || started.currentURI == "" || started.state != StateStarting {
				t.Fatalf("natural successor was acknowledged without an owned playback binding: %#v", started)
			}
			devices.mu.Lock()
			calls := append([]string(nil), devices.calls...)
			devices.mu.Unlock()
			if len(calls) != 4 || calls[2] != "set_uri" || calls[3] != "play" {
				t.Fatalf("natural successor did not execute renderer start: %v", calls)
			}
		} else if ended.commandQueued {
			t.Fatal("queue end enqueued a nonexistent entry")
		}
	}
	state := service.mustState(t)
	if state.State != StateStopped || state.PendingCommand != "" || state.CurrentEntryID != queue.Entries[1].ID {
		t.Fatalf("queue end state=%#v", state)
	}
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Entries[0].Status != EntryCompleted || finished.Entries[1].Status != EntryCompleted {
		t.Fatalf("queue completion statuses=%#v", finished.Entries)
	}
}

func TestExplicitCompletionPreservesQueueOwnership(t *testing.T) {
	for _, test := range []struct {
		name      string
		completed bool
		nearEnd   bool
		foreign   bool
	}{
		{name: "finished before first position observation", completed: true},
		{name: "cancelled near end", nearEnd: true},
		{name: "another session finished", completed: true, foreign: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, _ := newPlayerTestService(t)
			ctx := context.Background()
			queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
				t.Fatal(err)
			}
			runAcceptedCommand(t, service)
			stored, err := service.loadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			if test.nearEnd {
				service.applyObservation(ctx, output.Observation{
					State: "playing", URI: stored.currentURI, HasURI: true,
					PositionMS: 99000, DurationMS: 100000, HasPosition: true, ObservedAt: now,
				})
			}
			uri := stored.currentURI
			if test.foreign {
				uri += "-another-session"
			}
			ended := output.Observation{
				State: "stopped", URI: uri, HasURI: true, ObservedAt: now.Add(time.Second),
				CompletionKnown: true, Completed: test.completed,
			}
			service.applyObservation(ctx, ended)
			service.applyObservation(ctx, ended)
			if test.completed && !test.foreign {
				runAcceptedCommand(t, service)
				state := service.mustState(t)
				if state.CurrentEntryID != queue.Entries[1].ID || state.PendingCommand != "" {
					t.Fatalf("owned completion did not start exactly one successor: %#v", state)
				}
				updated, err := service.Queue(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if updated.Entries[0].Status != EntryCompleted {
					t.Fatalf("finished entry was not completed: %#v", updated)
				}
			} else {
				state := service.mustState(t)
				if state.CurrentEntryID != queue.Entries[0].ID || state.PendingCommand != "" {
					t.Fatalf("non-completion advanced the queue: %#v", state)
				}
				updated, err := service.Queue(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if updated.Entries[0].Status == EntryCompleted || updated.Entries[1].Status != EntryPending {
					t.Fatalf("non-completion consumed queue entries: %#v", updated)
				}
			}
		})
	}
}

func TestCompletionAwarePlaybackWaitsForExplicitFinish(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := context.Background()
	now := time.Now().UTC()
	service.now = func() time.Time { return now }
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	stored, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	observation := output.Observation{
		State: "playing", URI: stored.currentURI, HasURI: true,
		PositionMS: 100000, DurationMS: 100000, HasPosition: true,
		CompletionKnown: true, ObservedAt: now,
	}
	for range 4 {
		service.applyObservation(ctx, observation)
		now = now.Add(time.Second)
		observation.ObservedAt = now
	}
	state := service.mustState(t)
	if state.PendingCommand != "" || state.State != StatePlaying || state.CurrentEntryID != queue.Entries[0].ID {
		t.Fatalf("position replaced explicit completion evidence: %#v", state)
	}
	observation.State = "stopped"
	observation.Completed = true
	service.applyObservation(ctx, observation)
	runAcceptedCommand(t, service)
	if service.mustState(t).CurrentEntryID != queue.Entries[1].ID {
		t.Fatal("explicit FINISHED did not advance to the successor")
	}
}

func TestStoppedAloneIsNotNaturalEnd(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE player_state SET current_entry_id=?,play_id='play_early',current_uri='http://10.0.0.2/media/early',state='playing',position_ms=10000,duration_ms=100000,last_observed_state='playing',last_observed_uri='http://10.0.0.2/media/early',last_observed_position_ms=10000,last_observed_duration_ms=100000,last_observed_has_position=1,resume_required=0,control_action='' WHERE singleton=1`, queue.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	result := service.applyObservation(ctx, output.Observation{State: "stopped", URI: "http://10.0.0.2/media/early", HasURI: true, ObservedAt: time.Now().UTC()})
	if result.commandQueued || service.mustState(t).State != StateStopped {
		t.Fatalf("early stop was mistaken for EOF: %#v", result)
	}
}

func TestPreviousFiveSecondBoundaryUsesStoredOrder(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE player_state SET current_entry_id=?,play_id='play_b',current_uri='http://10.0.0.2/media/b',current_seekable=1,state='playing',position_ms=5001,duration_ms=90000,resume_required=0 WHERE singleton=1`, queue.Entries[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "previous"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	devices.mu.Lock()
	calls := append([]string(nil), devices.calls...)
	devices.mu.Unlock()
	if len(calls) != 1 || calls[0] != "seek:0" {
		t.Fatalf("previous above five seconds did not seek current: %v", calls)
	}
	if _, err := db.Exec("UPDATE player_state SET position_ms=5000 WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	devices.mu.Lock()
	devices.calls = nil
	devices.mu.Unlock()
	if _, err := service.Command(ctx, Command{Action: "previous"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	if state.CurrentEntryID != queue.Entries[0].ID {
		t.Fatalf("previous at five seconds did not select prior stored entry: %#v", state)
	}
	devices.mu.Lock()
	calls = append([]string(nil), devices.calls...)
	devices.mu.Unlock()
	if len(calls) != 3 || calls[0] != "stop" || calls[1] != "set_uri" || calls[2] != "play" {
		t.Fatalf("previous at boundary did not perform ordered restart: %v", calls)
	}
}

func TestOutputSelectionRequiresStoppedStateAndNoCommand(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectOutput(ctx, "renderer"); faultCode(err) != "COMMAND_IN_PROGRESS" {
		t.Fatalf("selection with pending command error=%v", err)
	}
	runAcceptedCommand(t, service)
	if _, err := service.SelectOutput(ctx, "renderer"); faultCode(err) != "PLAYER_NOT_STOPPED" {
		t.Fatalf("selection while starting error=%v", err)
	}
}

func TestPairOutputRequiresStoppedState(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := context.Background()
	status, err := service.PairOutput(ctx, "renderer", output.PairingRequest{PIN: "1234"})
	if err != nil || status.Required {
		t.Fatalf("pairing while stopped failed: status=%#v err=%v", status, err)
	}
	devices.mu.Lock()
	devices.fail["pair"] = output.NewActionError(output.ErrorResponse, "Pair", 0, errors.New("rejected"))
	devices.mu.Unlock()
	if _, err := service.PairOutput(ctx, "renderer", output.PairingRequest{Password: "wrong"}); faultCode(err) != "PAIRING_REJECTED" {
		t.Fatalf("receiver pairing rejection error=%v", err)
	}
	if _, err := db.Exec("UPDATE player_state SET state='playing' WHERE singleton=1"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PairOutput(ctx, "renderer", output.PairingRequest{Password: "secret"}); faultCode(err) != "PLAYER_NOT_STOPPED" {
		t.Fatalf("pairing during playback error=%v", err)
	}
	devices.mu.Lock()
	defer devices.mu.Unlock()
	if len(devices.calls) != 2 || devices.calls[0] != "pair" || devices.calls[1] != "pair" {
		t.Fatalf("stopped-only pairing call order is incorrect: %v", devices.calls)
	}
}

func TestMissingURIObservationPreservesOwnedPlaybackAndPublishesPlayer(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	service.applyObservation(ctx, output.Observation{
		State: "playing", PositionMS: 0, DurationMS: 100000,
		HasPosition: true, HasURI: false, ObservedAt: time.Now().UTC(),
	})
	before, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var topics []string
	service.notify = func(topic string) {
		topics = append(topics, topic)
	}
	devices.mu.Lock()
	devices.observation = output.Observation{
		State: "playing", PositionMS: 12000, DurationMS: 100000,
		HasPosition: true, HasURI: false, ObservedAt: time.Now().UTC(),
	}
	devices.mu.Unlock()
	service.observeSelected(ctx)
	after, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if after.state != StatePlaying || after.playID != before.playID || after.currentURI != before.currentURI {
		t.Fatalf("missing optional URI revoked owned playback: before=%#v after=%#v", before, after)
	}
	devices.mu.Lock()
	devices.observation = output.Observation{State: "unknown", HasURI: false, ObservedAt: time.Now().UTC()}
	devices.mu.Unlock()
	service.observeSelected(ctx)
	unknown, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.state != StatePlaying || unknown.playID != before.playID || unknown.currentURI != before.currentURI {
		t.Fatalf("unknown transport without URI evidence revoked owned playback: %#v", unknown)
	}
	media := service.media.(*fakeMedia)
	media.mu.Lock()
	revoked := append([]string(nil), media.revoked...)
	media.mu.Unlock()
	if len(revoked) != 0 {
		t.Fatalf("missing optional URI revoked media binding: %v", revoked)
	}
	if len(topics) != 1 || topics[0] != "player" {
		t.Fatalf("position-only observation published unconsumed topics: %v", topics)
	}
}

func TestSnapshotIntersectsRendererAndCurrentStreamSeekCapability(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := context.Background()
	media := service.media.(*fakeMedia)
	media.mu.Lock()
	media.unseekable = true
	media.mu.Unlock()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	if state.Capabilities.Seek || !state.Capabilities.Play || !state.Capabilities.Pause || !state.Capabilities.Stop {
		t.Fatalf("snapshot did not negotiate current stream capabilities: %#v", state.Capabilities)
	}
}

func TestUncertainRendererActionsRemainUnknown(t *testing.T) {
	tests := []struct {
		name          string
		failingAction string
		kind          output.ErrorKind
		command       Command
		failStart     bool
	}{
		{name: "set URI timeout", failingAction: "set_uri", kind: output.ErrorTimeout, command: Command{Action: "play"}, failStart: true},
		{name: "play transport", failingAction: "play", kind: output.ErrorTransport, command: Command{Action: "play"}, failStart: true},
		{name: "pause invalid response", failingAction: "pause", kind: output.ErrorResponse, command: Command{Action: "pause"}},
		{name: "stop cancelled", failingAction: "stop", kind: output.ErrorCancelled, command: Command{Action: "stop"}},
		{name: "seek timeout", failingAction: "seek:1000", kind: output.ErrorTimeout, command: Command{Action: "seek", PositionMS: 1000}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, db, devices := newPlayerTestService(t)
			ctx := context.Background()
			queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
			if err != nil {
				t.Fatal(err)
			}
			test.command.EntryID = ""
			if test.failStart {
				test.command.EntryID = queue.Entries[0].ID
			} else {
				if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
					t.Fatal(err)
				}
				runAcceptedCommand(t, service)
				devices.mu.Lock()
				devices.calls = nil
				devices.mu.Unlock()
			}
			devices.mu.Lock()
			devices.fail[test.failingAction] = &output.ActionError{Kind: test.kind, Action: test.failingAction}
			devices.mu.Unlock()
			if _, err := service.Command(ctx, test.command); err != nil {
				t.Fatal(err)
			}
			runAcceptedCommand(t, service)
			state := service.mustState(t)
			if state.PendingCommand != "" || state.State != StateUnavailable || !strings.Contains(strings.ToLower(state.Error), "unknown") {
				t.Fatalf("uncertain action was falsely resolved: %#v", state)
			}
			var outcome string
			if err := db.QueryRow("SELECT status FROM player_commands ORDER BY created_at DESC LIMIT 1").Scan(&outcome); err != nil {
				t.Fatal(err)
			}
			if outcome != "unknown" {
				t.Fatalf("uncertain action outcome=%q", outcome)
			}
			finished, err := service.Queue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if finished.Entries[0].Status != EntryPending {
				t.Fatalf("uncertain action did not preserve replayable cursor: %#v", finished.Entries)
			}
			devices.mu.Lock()
			calls := append([]string(nil), devices.calls...)
			devices.mu.Unlock()
			count := 0
			for _, call := range calls {
				if call == test.failingAction {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("uncertain mutating action was retried: %v", calls)
			}
		})
	}
}

func TestUncertainNaturalSuccessorStartRemainsUnknown(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	first, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	service.applyObservation(ctx, output.Observation{
		State: "playing", URI: first.currentURI, HasURI: true,
		PositionMS: 99000, DurationMS: 100000, HasPosition: true, ObservedAt: now,
	})
	ended := service.applyObservation(ctx, output.Observation{
		State: "stopped", URI: "", HasURI: true, DurationMS: 100000, ObservedAt: now.Add(time.Second),
	})
	if !ended.commandQueued {
		t.Fatal("natural successor was not queued")
	}
	devices.mu.Lock()
	devices.fail["set_uri"] = &output.ActionError{Kind: output.ErrorResponse, Action: "SetAVTransportURI"}
	devices.mu.Unlock()
	runAcceptedCommand(t, service)
	var outcome string
	if err := db.QueryRow("SELECT status FROM player_commands WHERE action='natural_next'").Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	state := service.mustState(t)
	if outcome != "unknown" || state.State != StateUnavailable || state.CurrentEntryID != queue.Entries[1].ID {
		t.Fatalf("uncertain successor start was falsely resolved: outcome=%q state=%#v", outcome, state)
	}
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Entries[0].Status != EntryCompleted || finished.Entries[1].Status != EntryPending {
		t.Fatalf("uncertain successor start corrupted queue order: %#v", finished.Entries)
	}
}

func TestRendererSOAPFaultIsConfirmedFailure(t *testing.T) {
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
	devices.mu.Lock()
	devices.fail["stop"] = &output.ActionError{Kind: output.ErrorFault, Action: "Stop", Code: 701}
	devices.mu.Unlock()
	if _, err := service.Command(ctx, Command{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	var outcome string
	if err := db.QueryRow("SELECT status FROM player_commands ORDER BY created_at DESC LIMIT 1").Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	state := service.mustState(t)
	if outcome != "failed" || state.State != StateError || state.Error == "" || state.StatusWarning != nil {
		t.Fatalf("confirmed SOAP rejection was not retained as an immediate failure: outcome=%q state=%#v", outcome, state)
	}
}

func TestAdaptiveObservationDeadlinesCoverEveryLegalPollInterval(t *testing.T) {
	far := storedState{
		lastObservedState: "playing", lastObservedHasPosition: true,
		lastObservedPositionMS: 0, lastObservedDurationMS: 100000,
	}
	near := far
	near.lastObservedPositionMS = 98000
	for seconds := 1; seconds <= 300; seconds++ {
		configured := time.Duration(seconds) * time.Second
		delay := adaptiveObservationDelay(configured, far)
		if delay <= 0 || delay > configured || delay > 98*time.Second {
			t.Fatalf("poll=%s missed pre-EOF evidence deadline: %s", configured, delay)
		}
		nearDelay := adaptiveObservationDelay(configured, near)
		if nearDelay <= 0 || nearDelay > 500*time.Millisecond {
			t.Fatalf("poll=%s did not sample near EOF frequently: %s", configured, nearDelay)
		}
	}
	longTrack := far
	longTrack.lastObservedPositionMS = 10000
	longTrack.lastObservedDurationMS = int64(time.Hour / time.Millisecond)
	if delay := adaptiveObservationDelay(300*time.Second, longTrack); delay != 300*time.Second {
		t.Fatalf("long-track polling increased renderer load: %s", delay)
	}
}

func TestRendererErrorNearEOFDoesNotAdvanceQueue(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	stored, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	service.applyObservation(ctx, output.Observation{
		State: "playing", URI: stored.currentURI, HasURI: true,
		PositionMS: 99000, DurationMS: 100000, HasPosition: true, ObservedAt: now,
	})
	result := service.applyObservation(ctx, output.Observation{
		State: "stopped", URI: "", HasURI: true, TransportStatus: "ERROR_OCCURRED",
		DurationMS: 100000, ObservedAt: now.Add(time.Second),
	})
	if result.commandQueued {
		t.Fatal("renderer error was treated as natural completion")
	}
	state := service.mustState(t)
	if state.State != StateError || state.Error == "" || state.CurrentEntryID != queue.Entries[0].ID {
		t.Fatalf("renderer error did not preserve interrupted cursor: %#v", state)
	}
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Entries[0].Status != EntryPending || finished.Entries[1].Status != EntryPending {
		t.Fatalf("renderer error advanced queue: %#v", finished.Entries)
	}
}

func TestPlaybackStartInterruptsSlowObservationSchedule(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	service.pollInterval = 300 * time.Second
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	select {
	case <-service.observeWake:
	default:
		t.Fatal("successful playback start did not interrupt the configured 300-second observation wait")
	}
}

func TestStartupStoppedObservationRetainsGrantUntilPlaybackConfirmed(t *testing.T) {
	t.Run("fresh start", func(t *testing.T) {
		service, _, devices := newPlayerTestService(t)
		service.pollInterval = 300 * time.Second
		ctx := context.Background()
		now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
		service.now = func() time.Time { return now }
		queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		binding, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}

		now = now.Add(time.Second)
		devices.observation = output.Observation{
			State: "stopped", URI: binding.currentURI, HasURI: true,
			TransportStatus: "OK", ObservedAt: now,
		}
		service.observeSelected(ctx)
		retained, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state := service.mustState(t)
		pending, err := service.Queue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if state.State != StateStarting && state.State != StateUnavailable {
			t.Fatalf("startup stop was published as a confirmed state: %#v", state)
		}
		if retained.playID != binding.playID || retained.currentURI != binding.currentURI ||
			state.CurrentEntryID != queue.Entries[0].ID || pending.Entries[0].Status != EntryPlaying {
			t.Fatalf("startup stop discarded the owned playback intent: state=%#v queue=%#v", state, pending)
		}
		media := service.media.(*fakeMedia)
		media.mu.Lock()
		revoked := append([]string(nil), media.revoked...)
		media.mu.Unlock()
		if len(revoked) != 0 {
			t.Fatalf("startup stop revoked the media grant: %v", revoked)
		}

		now = now.Add(time.Second)
		devices.observation = output.Observation{
			State: "playing", URI: binding.currentURI, HasURI: true, ObservedAt: now,
		}
		service.observeSelected(ctx)
		confirmed, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state = service.mustState(t)
		devices.mu.Lock()
		calls := append([]string(nil), devices.calls...)
		devices.mu.Unlock()
		if state.State != StatePlaying || confirmed.playID != binding.playID || confirmed.currentURI != binding.currentURI {
			t.Fatalf("playing observation did not confirm the retained binding: %#v", state)
		}
		if strings.Join(calls, ",") != "set_uri,play" {
			t.Fatalf("startup confirmation replayed the renderer command: %v", calls)
		}
		if delay := service.nextObservationDelay(ctx); delay != service.pollInterval {
			t.Fatalf("playing observation did not clear startup evidence: %s", delay)
		}
	})

	t.Run("paused resume", func(t *testing.T) {
		service, _, devices := newPlayerTestService(t)
		service.pollInterval = 300 * time.Second
		ctx := context.Background()
		now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
		service.now = func() time.Time { return now }
		if _, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		binding, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		devices.observation = output.Observation{
			State: "paused", URI: binding.currentURI, HasURI: true, ObservedAt: now,
		}
		service.observeSelected(ctx)
		if delay := service.nextObservationDelay(ctx); delay != service.pollInterval {
			t.Fatalf("paused observation did not confirm startup: %s", delay)
		}
		now = now.Add(time.Minute)
		if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)

		now = now.Add(time.Second)
		devices.observation = output.Observation{
			State: "stopped", URI: binding.currentURI, HasURI: true,
			TransportStatus: "OK", ObservedAt: now,
		}
		service.observeSelected(ctx)
		resumed, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		state := service.mustState(t)
		media := service.media.(*fakeMedia)
		media.mu.Lock()
		revoked := append([]string(nil), media.revoked...)
		media.mu.Unlock()
		if (state.State != StateStarting && state.State != StateUnavailable) ||
			resumed.playID != binding.playID || resumed.currentURI != binding.currentURI || len(revoked) != 0 {
			t.Fatalf("stopped status discarded a genuinely resumed binding: state=%#v revoked=%v", state, revoked)
		}
		devices.mu.Lock()
		calls := append([]string(nil), devices.calls...)
		devices.mu.Unlock()
		if strings.Join(calls, ",") != "set_uri,play,play" {
			t.Fatalf("paused resume sent an unexpected renderer command: %v", calls)
		}
	})
}

func TestStartupDeadlineIsFixedAndPreservesQueueForExplicitReplay(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	service.pollInterval = 300 * time.Second
	ctx := context.Background()
	startedAt := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	now := startedAt
	service.now = func() time.Time { return now }
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if delay := service.nextObservationDelay(ctx); delay <= 0 || delay > commandTimeout {
		t.Fatalf("300-second polling missed the startup confirmation deadline: %s", delay)
	}

	for _, elapsed := range []time.Duration{time.Second, 3 * time.Second} {
		now = startedAt.Add(elapsed)
		devices.observation = output.Observation{
			State: "stopped", URI: binding.currentURI, HasURI: true,
			TransportStatus: "OK", ObservedAt: now,
		}
		service.observeSelected(ctx)
	}
	now = startedAt.Add(5 * time.Second)
	if _, err := service.Command(ctx, Command{Action: "pause"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	now = startedAt.Add(9 * time.Second)
	if _, err := service.Command(ctx, Command{Action: "seek", PositionMS: 1000}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	now = startedAt.Add(12 * time.Second)
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	now = startedAt.Add(13 * time.Second)
	devices.observation = output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		TransportStatus: "OK", ObservedAt: now,
	}
	service.observeSelected(ctx)
	afterCommands, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	media := service.media.(*fakeMedia)
	media.mu.Lock()
	revoked := append([]string(nil), media.revoked...)
	media.mu.Unlock()
	if afterCommands.playID != binding.playID || afterCommands.currentURI != binding.currentURI || len(revoked) != 0 {
		t.Fatalf("pause, seek, or no-op play discarded startup grace: state=%#v revoked=%v", afterCommands, revoked)
	}
	now = startedAt.Add(15 * time.Second)
	devices.observation = output.Observation{
		State: "transitioning", URI: binding.currentURI, HasURI: true, ObservedAt: now,
	}
	service.observeSelected(ctx)
	if delay := service.nextObservationDelay(ctx); delay <= 0 || delay > 5*time.Second {
		t.Fatalf("transitioning observation lost or refreshed the startup deadline: %s", delay)
	}
	now = startedAt.Add(19 * time.Second)
	devices.fail["observe"] = output.NewActionError(output.ErrorTimeout, "Observe", 0, context.DeadlineExceeded)
	service.observeSelected(ctx)
	uncertain, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	media.mu.Lock()
	revoked = append(revoked[:0], media.revoked...)
	media.mu.Unlock()
	if uncertain.playID != binding.playID || uncertain.currentURI != binding.currentURI || len(revoked) != 0 {
		t.Fatalf("status-query failure discarded startup grace: state=%#v revoked=%v", uncertain, revoked)
	}
	if delay := service.nextObservationDelay(ctx); delay <= 0 || delay > time.Second {
		t.Fatalf("query failure lost or refreshed the startup deadline: %s", delay)
	}

	now = startedAt.Add(commandTimeout + time.Second)
	delete(devices.fail, "observe")
	devices.observation = output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		TransportStatus: "OK", ObservedAt: now,
	}
	service.observeSelected(ctx)
	expired, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state := service.mustState(t)
	preserved, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	media.mu.Lock()
	revoked = append(revoked[:0], media.revoked...)
	media.mu.Unlock()
	devices.mu.Lock()
	calls := append([]string(nil), devices.calls...)
	devices.mu.Unlock()
	if state.State != StateStopped || state.CurrentEntryID != queue.Entries[0].ID ||
		state.PendingCommand != "" || state.Error == "" || expired.playID != "" || expired.currentURI != "" {
		t.Fatalf("expired startup did not require explicit replay: %#v", state)
	}
	if preserved.Entries[0].ID != queue.Entries[0].ID || preserved.Entries[0].Status != EntryPending ||
		preserved.Entries[1].ID != queue.Entries[1].ID || preserved.Entries[1].Status != EntryPending {
		t.Fatalf("expired startup changed the pending queue: %#v", preserved.Entries)
	}
	if len(revoked) != 1 || revoked[0] != binding.playID {
		t.Fatalf("expired startup did not revoke exactly its media grant: %v", revoked)
	}
	if strings.Join(calls, ",") != "set_uri,play,pause,seek:1000" {
		t.Fatalf("startup evidence caused an unexpected renderer action: %v", calls)
	}
	if delay := service.nextObservationDelay(ctx); delay != service.pollInterval {
		t.Fatalf("expired startup left a busy observation loop: %s", delay)
	}

	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	replayed, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	resumedQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.mu.Lock()
	calls = append([]string(nil), devices.calls...)
	devices.mu.Unlock()
	if replayed.playID == "" || replayed.playID == binding.playID || replayed.currentEntryID != queue.Entries[0].ID ||
		resumedQueue.Entries[0].Status != EntryPlaying || resumedQueue.Entries[1].Status != EntryPending {
		t.Fatalf("explicit replay did not restore the preserved cursor: state=%#v queue=%#v", replayed, resumedQueue)
	}
	if strings.Join(calls, ",") != "set_uri,play,pause,seek:1000,set_uri,play" {
		t.Fatalf("expired startup replayed without an explicit fresh start: %v", calls)
	}
}

func TestStartupGraceDoesNotDelayDefinitiveObservations(t *testing.T) {
	tests := []struct {
		name        string
		observation func(storedState, time.Time) output.Observation
		wantState   string
	}{
		{
			name: "renderer error",
			observation: func(binding storedState, now time.Time) output.Observation {
				return output.Observation{
					State: "stopped", URI: binding.currentURI, HasURI: true,
					TransportStatus: "ERROR_OCCURRED", ObservedAt: now,
				}
			},
			wantState: StateError,
		},
		{
			name: "foreign URI",
			observation: func(_ storedState, now time.Time) output.Observation {
				return output.Observation{
					State: "playing", URI: "http://10.0.0.2/media/foreign", HasURI: true, ObservedAt: now,
				}
			},
			wantState: StateUnavailable,
		},
		{
			name: "cleared URI",
			observation: func(_ storedState, now time.Time) output.Observation {
				return output.Observation{State: "stopped", URI: "", HasURI: true, ObservedAt: now}
			},
			wantState: StateStopped,
		},
		{
			name: "explicit cancellation",
			observation: func(binding storedState, now time.Time) output.Observation {
				return output.Observation{
					State: "stopped", URI: binding.currentURI, HasURI: true,
					CompletionKnown: true, Completed: false, ObservedAt: now,
				}
			},
			wantState: StateStopped,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _, devices := newPlayerTestService(t)
			ctx := context.Background()
			now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
			service.now = func() time.Time { return now }
			queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
				t.Fatal(err)
			}
			runAcceptedCommand(t, service)
			binding, err := service.loadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Second)
			devices.observation = test.observation(binding, now)
			service.observeSelected(ctx)

			interrupted, err := service.loadState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			state := service.mustState(t)
			preserved, err := service.Queue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			media := service.media.(*fakeMedia)
			media.mu.Lock()
			revoked := append([]string(nil), media.revoked...)
			media.mu.Unlock()
			if state.State != test.wantState || state.CurrentEntryID != queue.Entries[0].ID ||
				state.PendingCommand != "" || interrupted.playID != "" || interrupted.currentURI != "" {
				t.Fatalf("definitive observation was deferred by startup grace: %#v", state)
			}
			if preserved.Entries[0].Status != EntryPending || preserved.Entries[1].Status != EntryPending {
				t.Fatalf("definitive observation consumed the queue: %#v", preserved.Entries)
			}
			if len(revoked) != 1 || revoked[0] != binding.playID {
				t.Fatalf("definitive observation retained its media grant: %v", revoked)
			}
		})
	}

	t.Run("explicit completion", func(t *testing.T) {
		service, _, devices := newPlayerTestService(t)
		ctx := context.Background()
		now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
		service.now = func() time.Time { return now }
		queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		binding, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
		devices.observation = output.Observation{
			State: "stopped", URI: binding.currentURI, HasURI: true,
			CompletionKnown: true, Completed: true, ObservedAt: now,
		}
		service.observeSelected(ctx)
		state := service.mustState(t)
		advanced, err := service.Queue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		media := service.media.(*fakeMedia)
		media.mu.Lock()
		revoked := append([]string(nil), media.revoked...)
		media.mu.Unlock()
		if state.State != StateStarting || state.CurrentEntryID != queue.Entries[1].ID || state.PendingCommand == "" ||
			advanced.Entries[0].Status != EntryCompleted || advanced.Entries[1].Status != EntryPending {
			t.Fatalf("explicit completion was deferred by startup grace: state=%#v queue=%#v", state, advanced)
		}
		if len(revoked) != 1 || revoked[0] != binding.playID {
			t.Fatalf("explicit completion retained its media grant: %v", revoked)
		}
	})

	t.Run("explicit stop", func(t *testing.T) {
		service, _, _ := newPlayerTestService(t)
		ctx := context.Background()
		now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
		service.now = func() time.Time { return now }
		queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		binding, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Second)
		if _, err := service.Command(ctx, Command{Action: "stop"}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		state := service.mustState(t)
		preserved, err := service.Queue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		media := service.media.(*fakeMedia)
		media.mu.Lock()
		revoked := append([]string(nil), media.revoked...)
		media.mu.Unlock()
		if state.State != StateStopped || state.CurrentEntryID != queue.Entries[0].ID ||
			state.PendingCommand != "" || preserved.Entries[0].Status != EntryPending {
			t.Fatalf("explicit stop was deferred by startup grace: state=%#v queue=%#v", state, preserved)
		}
		if len(revoked) != 1 || revoked[0] != binding.playID {
			t.Fatalf("explicit stop retained its media grant: %v", revoked)
		}
	})
}

func TestNewPlaybackDoesNotReusePriorTrackEOFEvidence(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := context.Background()
	now := time.Date(2026, time.September, 13, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	first, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	service.applyObservation(ctx, output.Observation{
		State: "playing", URI: first.currentURI, HasURI: true,
		PositionMS: 99000, DurationMS: 100000, HasPosition: true, ObservedAt: now,
	})
	if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	second, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.currentEntryID != queue.Entries[1].ID || second.lastObservedState != "" || second.lastObservedHasPosition {
		t.Fatalf("new binding retained prior EOF evidence: %#v", second)
	}
	now = now.Add(time.Second)
	stopped := service.applyObservation(ctx, output.Observation{
		State: "stopped", TransportStatus: "OK", URI: second.currentURI, HasURI: true,
		DurationMS: 90000, ObservedAt: now,
	})
	if stopped.commandQueued {
		t.Fatal("ambiguous startup stop reused prior EOF evidence to queue automatic playback")
	}
	retained, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if retained.playID != second.playID || retained.currentURI != second.currentURI ||
		(retained.state != StateStarting && retained.state != StateUnavailable) {
		t.Fatalf("new binding was not retained during its startup grace: %#v", retained)
	}

	now = now.Add(commandTimeout)
	stopped = service.applyObservation(ctx, output.Observation{
		State: "stopped", TransportStatus: "OK", URI: second.currentURI, HasURI: true,
		DurationMS: 90000, ObservedAt: now,
	})
	if stopped.commandQueued {
		t.Fatal("ambiguous stop of the new entry queued automatic playback")
	}
	state := service.mustState(t)
	if state.State != StateStopped || state.CurrentEntryID != queue.Entries[1].ID || state.PendingCommand != "" || state.Error == "" {
		t.Fatalf("expired ambiguous stop did not preserve the new entry for explicit replay: %#v", state)
	}
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Entries[0].Status != EntryCompleted || finished.Entries[1].Status != EntryPending {
		t.Fatalf("ambiguous stop completed or replayed the new entry: %#v", finished.Entries)
	}
}

func TestTerminalMediaFailureAdvancesExactlyOnceAndRetainsFailedEntry(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
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
	failed := output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		PositionMS: 41119, DurationMS: 100000, HasPosition: true,
		ObservedAt: time.Now().UTC(), TransportStatus: "ERROR_OCCURRED",
		MediaFailed: true, PlayID: binding.playID, CommandSequence: 2,
	}
	devices.observation = failed
	service.observeSelected(ctx)
	service.observeSelected(ctx)
	var advances, naturalEnds int
	if err := db.QueryRow("SELECT COUNT(*) FROM player_commands WHERE action='error_next'").Scan(&advances); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM player_natural_ends").Scan(&naturalEnds); err != nil {
		t.Fatal(err)
	}
	if advances != 1 || naturalEnds != 0 {
		t.Fatalf("media failure progression advances=%d natural_ends=%d", advances, naturalEnds)
	}
	pending, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Entries[0].Status != EntryError || pending.Entries[1].Status != EntryPending {
		t.Fatalf("media failure queue=%#v", pending.Entries)
	}
	media := service.media.(*fakeMedia)
	media.mu.Lock()
	revoked := append([]string(nil), media.revoked...)
	media.mu.Unlock()
	if len(revoked) != 1 || revoked[0] != binding.playID {
		t.Fatalf("failed media revoke=%v", revoked)
	}
	runAcceptedCommand(t, service)
	started := service.mustState(t)
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if started.CurrentEntryID != queue.Entries[1].ID || started.State != StateStarting ||
		finished.Entries[0].Status != EntryError || finished.Entries[1].Status != EntryPlaying {
		t.Fatalf("media failure successor state=%#v queue=%#v", started, finished.Entries)
	}
}

func TestConsecutiveStartupMediaFailuresContinueToSafeSuccessor(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b", "a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	devices.fail["set_uri"] = output.NewActionError(output.ErrorMedia, "SetURI", 0, errors.New("terminal media engine failure"))
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	runAcceptedCommand(t, service)
	delete(devices.fail, "set_uri")
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.State != StateStarting || state.CurrentEntryID != queue.Entries[2].ID ||
		finished.Entries[0].Status != EntryError || finished.Entries[1].Status != EntryError ||
		finished.Entries[2].Status != EntryPlaying {
		t.Fatalf("consecutive media failure state=%#v queue=%#v", state, finished.Entries)
	}
	var advances, naturalEnds int
	if err := db.QueryRow("SELECT COUNT(*) FROM player_commands WHERE action='error_next'").Scan(&advances); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM player_natural_ends").Scan(&naturalEnds); err != nil {
		t.Fatal(err)
	}
	if advances != 2 || naturalEnds != 0 {
		t.Fatalf("startup media failure progression advances=%d natural_ends=%d", advances, naturalEnds)
	}
}

func TestPreviousStartupMediaFailureReturnsToNextStoredEntry(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[1].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	devices.fail["set_uri"] = output.NewActionError(output.ErrorMedia, "SetURI", 0, errors.New("terminal media engine failure"))
	if _, err := service.Command(ctx, Command{Action: "previous"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	if state.State != StateStarting || state.CurrentEntryID != queue.Entries[1].ID || state.PendingCommand != "next" {
		t.Fatalf("previous failed media did not schedule its successor: %#v", state)
	}
	delete(devices.fail, "set_uri")
	runAcceptedCommand(t, service)
	state = service.mustState(t)
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.CurrentEntryID != queue.Entries[1].ID || state.State != StateStarting ||
		finished.Entries[0].Status != EntryError || finished.Entries[1].Status != EntryPlaying {
		t.Fatalf("previous failed media state=%#v queue=%#v", state, finished.Entries)
	}
}

func TestLastEntryMediaFailureStopsWithError(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.observation = output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		ObservedAt: time.Now().UTC(), TransportStatus: "ERROR_OCCURRED",
		MediaFailed: true, PlayID: binding.playID,
	}
	service.observeSelected(ctx)
	state := service.mustState(t)
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var advances, naturalEnds int
	if err := db.QueryRow("SELECT COUNT(*) FROM player_commands WHERE action='error_next'").Scan(&advances); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM player_natural_ends").Scan(&naturalEnds); err != nil {
		t.Fatal(err)
	}
	if state.State != StateError || state.PendingCommand != "" || state.Error == "" ||
		state.CurrentEntryID != queue.Entries[0].ID || finished.Entries[0].Status != EntryError ||
		advances != 0 || naturalEnds != 0 {
		t.Fatalf("last media failure state=%#v queue=%#v advances=%d natural_ends=%d", state, finished.Entries, advances, naturalEnds)
	}
}

func TestExplicitStopSupersedesQueuedMediaFailureAdvance(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := t.Context()
	_, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failed := output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		ObservedAt: time.Now().UTC(), TransportStatus: "ERROR_OCCURRED",
		MediaFailed: true, PlayID: binding.playID,
	}
	devices.observation = failed
	service.observeSelected(ctx)
	if _, err := service.Command(ctx, Command{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	service.observeSelected(ctx)
	state := service.mustState(t)
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var advanceStatus string
	if err := db.QueryRow("SELECT status FROM player_commands WHERE action='error_next'").Scan(&advanceStatus); err != nil {
		t.Fatal(err)
	}
	if state.State != StateStopped || state.PendingCommand != "" ||
		finished.Entries[0].Status != EntryError || finished.Entries[1].Status != EntryPending ||
		advanceStatus != "failed" {
		t.Fatalf("stop supersession state=%#v queue=%#v advance=%q", state, finished.Entries, advanceStatus)
	}
}

func TestUnknownSuccessorStartDoesNotSkipAgain(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b", "a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.observation = output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		ObservedAt: time.Now().UTC(), TransportStatus: "ERROR_OCCURRED",
		MediaFailed: true, PlayID: binding.playID,
	}
	service.observeSelected(ctx)
	devices.fail["set_uri"] = output.NewActionError(output.ErrorTransport, "SetURI", 0, errors.New("connection lost"))
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var advances int
	if err := db.QueryRow("SELECT COUNT(*) FROM player_commands WHERE action='error_next'").Scan(&advances); err != nil {
		t.Fatal(err)
	}
	if state.State != StateUnavailable || !strings.Contains(strings.ToLower(state.Error), "unknown") ||
		state.CurrentEntryID != queue.Entries[1].ID || advances != 1 ||
		finished.Entries[0].Status != EntryError || finished.Entries[1].Status != EntryPending ||
		finished.Entries[2].Status != EntryPending {
		t.Fatalf("unknown successor start state=%#v queue=%#v advances=%d", state, finished.Entries, advances)
	}
}

func TestPausedMediaFailureDoesNotAdvance(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := t.Context()
	_, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	binding, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.observation = output.Observation{
		State: "paused", URI: binding.currentURI, HasURI: true,
		ObservedAt: time.Now().UTC(), TransportStatus: "OK", PlayID: binding.playID,
	}
	service.observeSelected(ctx)
	devices.observation = output.Observation{
		State: "stopped", URI: binding.currentURI, HasURI: true,
		ObservedAt: time.Now().UTC(), TransportStatus: "ERROR_OCCURRED",
		MediaFailed: true, PlayID: binding.playID,
	}
	service.observeSelected(ctx)
	state := service.mustState(t)
	finished, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var advances int
	if err := db.QueryRow("SELECT COUNT(*) FROM player_commands WHERE action='error_next'").Scan(&advances); err != nil {
		t.Fatal(err)
	}
	if state.State != StateError || state.PendingCommand != "" || advances != 0 ||
		finished.Entries[0].Status != EntryPending || finished.Entries[1].Status != EntryPending {
		t.Fatalf("paused media failure state=%#v queue=%#v advances=%d", state, finished.Entries, advances)
	}
}

func (s *Service) mustState(t *testing.T) State {
	t.Helper()
	state, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func faultCode(err error) string {
	var public *fault.Error
	if errors.As(err, &public) {
		return public.Code
	}
	return ""
}

func TestStopForRestartRejectsUnconfirmedRendererStop(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := context.Background()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	devices.fail["stop"] = output.NewActionError(output.ErrorFault, "Stop", 701, errors.New("renderer rejected stop"))
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = service.Run(runCtx)
	}()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
	err = service.StopForRestart(stopCtx)
	stopCancel()
	if err == nil || !strings.Contains(err.Error(), "Stop") || !strings.Contains(err.Error(), "701") {
		t.Fatalf("restart accepted an unconfirmed renderer stop: %v", err)
	}
	select {
	case <-done:
		t.Fatal("failed restart stop terminated the running player service")
	default:
	}
	cancel()
	<-done
}

func TestConfirmedStopSurvivesOutputLossWithoutAnotherObservation(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	if _, err := service.Command(ctx, Command{Action: "stop"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	if err := service.StopForRestart(ctx); err != nil {
		t.Fatal(err)
	}
	devices.device.Online = false
	service.observeSelected(ctx)
	state := service.mustState(t)
	if state.State != StateStopped || state.Error != "" || state.CurrentEntryID != queue.Entries[0].ID {
		t.Fatalf("confirmed stop became uncertain after output loss: %#v", state)
	}
	retained, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(retained.Entries) != 2 || retained.Entries[0].ID != queue.Entries[0].ID ||
		retained.Entries[0].Status != EntryPending || retained.Entries[1].ID != queue.Entries[1].ID ||
		retained.Entries[1].Status != EntryPending {
		t.Fatalf("confirmed stop changed the retained queue: %#v", retained)
	}
}

func TestNonBrowserRendererFailurePersistsSafeHistory(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	history, err := errorhistory.New(t.Context(), db, nil)
	if err != nil {
		t.Fatal(err)
	}
	service.history = history
	devices.mu.Lock()
	devices.device.Name = "Living room"
	devices.device.Protocol = output.ProtocolUPnP
	devices.fail["set_uri"] = output.NewActionError(output.ErrorFault, "SetURI", 714, errors.New("private endpoint detail"))
	devices.mu.Unlock()
	queue, err := service.MutateQueue(t.Context(), QueueMutation{Action: "append", TrackIDs: []string{"a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(t.Context(), Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	result, err := history.List(t.Context(), errorhistory.ListOptions{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Items[0].RendererName != "Living room" || result.Items[0].Protocol != output.ProtocolUPnP ||
		result.Items[0].TrackID != "a" || result.Items[0].Stage != "SetURI" || result.Items[0].Code != "fault:714" ||
		result.Items[0].Outcome != "failed" {
		t.Fatalf("renderer history=%#v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private endpoint detail") {
		t.Fatal("raw renderer error escaped into history")
	}
}

func TestIsPlaybackActiveUsesDurableActivityOnly(t *testing.T) {
	service, db, _ := newPlayerTestService(t)
	ctx := t.Context()
	setState := func(state, playID, uri string, resumeRequired int) {
		t.Helper()
		if _, err := db.ExecContext(ctx, "UPDATE player_state SET state=?,play_id=?,current_uri=?,resume_required=? WHERE singleton=1", state, playID, uri, resumeRequired); err != nil {
			t.Fatal(err)
		}
	}
	assertActive := func(want bool) {
		t.Helper()
		active, err := service.IsPlaybackActive(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if active != want {
			t.Fatalf("IsPlaybackActive=%t, want %t", active, want)
		}
	}
	setState(StatePaused, "play", "owned", 0)
	assertActive(false)
	setState(StateStarting, "play", "owned", 0)
	assertActive(true)
	setState(StatePlaying, "play", "owned", 0)
	assertActive(true)
	setState(StateUnavailable, "play", "owned", 0)
	assertActive(true)
	setState(StateUnavailable, "play", "owned", 1)
	assertActive(false)
	setState(StateStopped, "", "", 0)
	if _, err := db.ExecContext(ctx, `INSERT INTO player_commands(command_id,service_epoch,action,entry_id,position_ms,status,error,created_at,started_at,completed_at) VALUES('activity-command',?,'play','',0,'pending','',?,'','')`, service.epoch, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	assertActive(true)
	if _, err := db.ExecContext(ctx, "UPDATE player_commands SET status='succeeded' WHERE command_id='activity-command'"); err != nil {
		t.Fatal(err)
	}
	assertActive(false)
}

func TestPlaybackModesPersistWithoutTransportOrAutoplay(t *testing.T) {
	service, db, devices := newPlayerTestService(t)
	ctx := t.Context()
	before := service.mustState(t)
	devices.mu.Lock()
	beforeCalls := len(devices.calls)
	devices.mu.Unlock()

	shuffle := true
	repeatAll := RepeatAll
	updated, err := service.UpdateMode(ctx, ModeUpdate{Shuffle: &shuffle, RepeatMode: &repeatAll})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Shuffle || updated.RepeatMode != RepeatAll || updated.Revision != before.Revision+1 {
		t.Fatalf("combined mode update=%#v", updated)
	}
	repeatOne := RepeatOne
	updated, err = service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeatOne})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Shuffle || updated.RepeatMode != RepeatOne {
		t.Fatalf("partial mode update lost shuffle: %#v", updated)
	}
	selected, err := service.SelectOutput(ctx, "renderer")
	if err != nil {
		t.Fatal(err)
	}
	if !selected.Shuffle || selected.RepeatMode != RepeatOne {
		t.Fatalf("output selection lost playback modes: %#v", selected)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO player_commands(command_id,service_epoch,action,entry_id,position_ms,status,error,created_at,started_at,completed_at) VALUES('mode-active',?,'play','',0,'pending','',?,'','')`, service.epoch, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	repeatOff := RepeatOff
	if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeatOff}); faultCode(err) != "COMMAND_IN_PROGRESS" {
		t.Fatalf("mode update during command error=%v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE player_commands SET status='failed' WHERE command_id='mode-active'"); err != nil {
		t.Fatal(err)
	}

	recovered, err := newService(ctx, db, service.lib, devices, service.media, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	state, err := recovered.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.mu.Lock()
	afterCalls := len(devices.calls)
	devices.mu.Unlock()
	if !state.Shuffle || state.RepeatMode != RepeatOne || state.State != StateStopped || state.PendingCommand != "" {
		t.Fatalf("recovered mode state=%#v", state)
	}
	if afterCalls != beforeCalls {
		t.Fatalf("mode update or recovery issued transport calls: before=%d after=%d", beforeCalls, afterCalls)
	}
}

func TestPlaybackModeChangePreservesActiveBindingAndQueue(t *testing.T) {
	service, _, devices := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	beforeState, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	beforeQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.mu.Lock()
	beforeCalls := append([]string(nil), devices.calls...)
	devices.mu.Unlock()

	shuffle := true
	repeat := RepeatAll
	if _, err := service.UpdateMode(ctx, ModeUpdate{Shuffle: &shuffle, RepeatMode: &repeat}); err != nil {
		t.Fatal(err)
	}
	afterState, err := service.loadState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	afterQueue, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	devices.mu.Lock()
	afterCalls := append([]string(nil), devices.calls...)
	devices.mu.Unlock()
	if afterState.currentEntryID != beforeState.currentEntryID || afterState.playID != beforeState.playID ||
		afterState.currentURI != beforeState.currentURI || afterState.state != beforeState.state ||
		afterState.positionMS != beforeState.positionMS {
		t.Fatalf("mode change interrupted playback: before=%#v after=%#v", beforeState, afterState)
	}
	if !reflect.DeepEqual(afterQueue, beforeQueue) {
		t.Fatalf("mode change mutated queue: before=%#v after=%#v", beforeQueue, afterQueue)
	}
	if !reflect.DeepEqual(afterCalls, beforeCalls) {
		t.Fatalf("mode change sent transport calls: before=%v after=%v", beforeCalls, afterCalls)
	}
}

func TestShuffleTraversalUsesQueueEntryIdentityAndPlayNextPrecedence(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "a", "b"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	shuffle := true
	if _, err := service.UpdateMode(ctx, ModeUpdate{Shuffle: &shuffle}); err != nil {
		t.Fatal(err)
	}
	displayed, err := service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(displayed.Entries) != len(queue.Entries) {
		t.Fatalf("shuffle changed displayed queue length: before=%#v after=%#v", queue.Entries, displayed.Entries)
	}
	for index := range queue.Entries {
		if displayed.Entries[index].ID != queue.Entries[index].ID {
			t.Fatalf("shuffle rearranged displayed queue: before=%#v after=%#v", queue.Entries, displayed.Entries)
		}
	}
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	firstEntryID := service.mustState(t).CurrentEntryID

	queue, err = service.Queue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	queue, err = service.MutateQueue(ctx, QueueMutation{Action: "next", TrackIDs: []string{"b"}, Revision: queue.Revision})
	if err != nil {
		t.Fatal(err)
	}
	currentIndex := -1
	for index, entry := range queue.Entries {
		if entry.ID == firstEntryID {
			currentIndex = index
			break
		}
	}
	if currentIndex < 0 || currentIndex+1 >= len(queue.Entries) {
		t.Fatalf("current queue entry missing after play-next: %#v", queue.Entries)
	}
	playNextID := queue.Entries[currentIndex+1].ID
	if _, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: queue.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	if current := service.mustState(t).CurrentEntryID; current != playNextID {
		t.Fatalf("shuffle next selected %q, want explicit play-next %q", current, playNextID)
	}
	if _, err := service.Command(ctx, Command{Action: "previous"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	if current := service.mustState(t).CurrentEntryID; current != firstEntryID {
		t.Fatalf("shuffle previous selected %q, want visited %q", current, firstEntryID)
	}
}

func TestShuffleTraversalVisitsEveryDuplicateEntryBeforeStopping(t *testing.T) {
	service, _, _ := newPlayerTestService(t)
	ctx := t.Context()
	queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "a", "b", "a"}, Revision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(queue.Entries) != 4 {
		t.Fatalf("queue lost duplicate track entries: %#v", queue.Entries)
	}
	remaining := make(map[string]bool, len(queue.Entries))
	for _, entry := range queue.Entries {
		remaining[entry.ID] = true
	}
	shuffle := true
	if _, err := service.UpdateMode(ctx, ModeUpdate{Shuffle: &shuffle}); err != nil {
		t.Fatal(err)
	}
	var lastEntryID string
	if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	for index := range queue.Entries {
		if index > 0 {
			if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
				t.Fatal(err)
			}
			runAcceptedCommand(t, service)
		}
		current := service.mustState(t).CurrentEntryID
		if !remaining[current] {
			t.Fatalf("shuffle step %d repeated or invented queue entry %q", index, current)
		}
		delete(remaining, current)
		lastEntryID = current
	}
	if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, service)
	state := service.mustState(t)
	if state.State != StateStopped || state.CurrentEntryID != lastEntryID {
		t.Fatalf("shuffle traversal end=%#v", state)
	}
}

func TestNaturalRepeatModesAndMediaFailureBoundary(t *testing.T) {
	t.Run("repeat one is natural completion only", func(t *testing.T) {
		service, _, _ := newPlayerTestService(t)
		ctx := t.Context()
		queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
		if err != nil {
			t.Fatal(err)
		}
		repeat := RepeatOne
		if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeat}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		stored, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		service.applyObservation(ctx, output.Observation{
			State: "stopped", URI: stored.currentURI, HasURI: true,
			CompletionKnown: true, Completed: true, ObservedAt: time.Now().UTC(),
		})
		runAcceptedCommand(t, service)
		if current := service.mustState(t).CurrentEntryID; current != queue.Entries[0].ID {
			t.Fatalf("repeat one advanced natural completion to %q", current)
		}
		if _, err := service.Command(ctx, Command{Action: "next"}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		if current := service.mustState(t).CurrentEntryID; current != queue.Entries[1].ID {
			t.Fatalf("repeat one intercepted explicit next: %q", current)
		}
	})

	t.Run("repeat all wraps a completed traversal", func(t *testing.T) {
		service, _, _ := newPlayerTestService(t)
		ctx := t.Context()
		queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
		if err != nil {
			t.Fatal(err)
		}
		repeat := RepeatAll
		if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeat}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[1].ID}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		stored, err := service.loadState(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result := service.applyObservation(ctx, output.Observation{
			State: "stopped", URI: stored.currentURI, HasURI: true,
			CompletionKnown: true, Completed: true, ObservedAt: time.Now().UTC(),
		})
		if !result.commandQueued {
			t.Fatal("repeat all did not queue the new traversal")
		}
		runAcceptedCommand(t, service)
		if current := service.mustState(t).CurrentEntryID; current != queue.Entries[0].ID {
			t.Fatalf("repeat all wrapped to %q, want %q", current, queue.Entries[0].ID)
		}
	})

	t.Run("unavailable entry advances within traversal", func(t *testing.T) {
		service, _, _ := newPlayerTestService(t)
		ctx := t.Context()
		queue, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0})
		if err != nil {
			t.Fatal(err)
		}
		lib := service.lib.(*fakeLibrary)
		unavailable := lib.tracks["a"]
		unavailable.Available = false
		lib.tracks["a"] = unavailable
		if _, err := service.Command(ctx, Command{Action: "play", EntryID: queue.Entries[0].ID}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		runAcceptedCommand(t, service)
		state := service.mustState(t)
		if state.CurrentEntryID != queue.Entries[1].ID || state.State != StateStarting || state.PendingCommand != "" {
			t.Fatalf("unavailable entry did not advance: %#v", state)
		}
		updated, err := service.Queue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Entries[0].Status != EntryError || updated.Entries[1].Status != EntryPlaying {
			t.Fatalf("unavailable entry traversal statuses=%#v", updated.Entries)
		}
	})

	t.Run("media failures never repeat wrap", func(t *testing.T) {
		service, _, _ := newPlayerTestService(t)
		ctx := t.Context()
		if _, err := service.MutateQueue(ctx, QueueMutation{Action: "append", TrackIDs: []string{"a", "b"}, Revision: 0}); err != nil {
			t.Fatal(err)
		}
		repeat := RepeatAll
		if _, err := service.UpdateMode(ctx, ModeUpdate{RepeatMode: &repeat}); err != nil {
			t.Fatal(err)
		}
		service.media = &failingPreparation{err: output.NewActionError(output.ErrorMedia, "Prepare", 0, errors.New("unavailable media"))}
		if _, err := service.Command(ctx, Command{Action: "play"}); err != nil {
			t.Fatal(err)
		}
		runAcceptedCommand(t, service)
		runAcceptedCommand(t, service)
		state := service.mustState(t)
		if state.State != StateError || state.PendingCommand != "" {
			t.Fatalf("all-error traversal did not terminate: %#v", state)
		}
		queue, err := service.Queue(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(queue.Entries) != 2 || queue.Entries[0].Status != EntryError || queue.Entries[1].Status != EntryError {
			t.Fatalf("all-error queue=%#v", queue.Entries)
		}
	})
}
