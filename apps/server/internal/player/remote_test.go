package player

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func TestRemoteControlRejectsStaleOwnershipWithoutChangingPlayback(t *testing.T) {
	for _, kind := range []string{"device", "play", "generation", "missing guard"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := newPlayerTestService(t)
			st := startPlayCountTrack(t, s, "a")
			before := s.mustState(t)
			intent := output.RemoteControl{DeviceID: st.rendererID, PlayID: st.playID, Action: "next", Current: func() bool { return true }}
			switch kind {
			case "device":
				intent.DeviceID = "another-renderer"
			case "play":
				intent.PlayID = "previous-play"
			case "generation":
				intent.Current = func() bool { return false }
			case "missing guard":
				intent.Current = nil
			}
			if err := s.RemoteCommand(context.Background(), intent); err == nil {
				t.Fatal("stale receiver control was accepted")
			}
			after := s.mustState(t)
			if after.Revision != before.Revision || after.CurrentEntryID != before.CurrentEntryID || after.PendingCommand != "" {
				t.Fatalf("stale control changed playback: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestRemoteControlUsesServerQueueAndRejectsConcurrentCommands(t *testing.T) {
	s, _, _ := newPlayerTestService(t)
	st := startPlayCountTrack(t, s, "a")
	ctx := context.Background()
	observePlayCountPosition(s, st, time.Now(), 0, st.durationMS)
	intent := output.RemoteControl{DeviceID: st.rendererID, PlayID: st.playID, Action: "pause", Current: func() bool { return true }}
	if err := s.RemoteCommand(ctx, intent); err != nil {
		t.Fatal(err)
	}
	intent.Action = "next"
	if err := s.RemoteCommand(ctx, intent); err == nil {
		t.Fatal("receiver bypassed the active command fence")
	}
	runAcceptedCommand(t, s)
	if state := s.mustState(t); state.State != StatePlaying {
		t.Fatalf("command acknowledgement replaced the renderer observation: %+v", state)
	}
	s.applyObservation(ctx, output.Observation{
		State: "PAUSED_PLAYBACK", URI: st.currentURI, HasURI: true, PlayID: st.playID,
		PositionMS: 0, DurationMS: st.durationMS, HasPosition: true, ObservedAt: time.Now(),
	})
	paused := s.mustState(t)
	intent.Action = "pause"
	if err := s.RemoteCommand(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if after := s.mustState(t); after.Revision != paused.Revision || after.PendingCommand != "" {
		t.Fatalf("duplicate state request created a feedback command: %+v", after)
	}
	intent.Action = "playpause"
	if err := s.RemoteCommand(ctx, intent); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, s)
	if after := s.mustState(t); after.State != StateStarting || after.CurrentEntryID != st.currentEntryID {
		t.Fatalf("device toggle did not request the Server-owned entry while awaiting observation: %+v", after)
	}
}

func TestRemoteRelativeSeekClampsWithoutIntegerOverflow(t *testing.T) {
	for _, test := range []struct {
		name        string
		delta, want int64
	}{{"forward", 1000, 4000}, {"minimum", math.MinInt64, 0}, {"maximum", math.MaxInt64, 100000}} {
		t.Run(test.name, func(t *testing.T) {
			s, _, _ := newPlayerTestService(t)
			st := startPlayCountTrack(t, s, "a")
			observePlayCountPosition(s, st, time.Now(), 3000, 100000)
			if err := s.RemoteCommand(context.Background(), output.RemoteControl{
				DeviceID: st.rendererID, PlayID: st.playID, Action: "seek", PositionMS: test.delta,
				Relative: true, Current: func() bool { return true },
			}); err != nil {
				t.Fatal(err)
			}
			command, found, err := s.claimCommand(context.Background())
			if err != nil || !found || command.positionMS != test.want {
				t.Fatalf("relative seek intent: found=%v position=%d want=%d err=%v", found, command.positionMS, test.want, err)
			}
			s.executeSafely(context.Background(), command)
			if after := s.mustState(t); after.PositionMS != 3000 || after.CurrentEntryID != st.currentEntryID {
				t.Fatalf("seek acknowledgement overwrote the last renderer observation: %+v", after)
			}
		})
	}
}

func TestRemoteStopInvalidatesOldPlayButAllowsExplicitStoppedPlay(t *testing.T) {
	s, _, _ := newPlayerTestService(t)
	st := startPlayCountTrack(t, s, "a")
	ctx := context.Background()
	intent := output.RemoteControl{DeviceID: st.rendererID, PlayID: st.playID, Action: "stop", Current: func() bool { return true }}
	if err := s.RemoteCommand(ctx, intent); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, s)
	intent.Action = "play"
	if err := s.RemoteCommand(ctx, intent); err == nil {
		t.Fatal("pre-Stop event resumed playback")
	}
	if state := s.mustState(t); state.State != StateStopped || state.PendingCommand != "" {
		t.Fatalf("stale play changed stopped state: %+v", state)
	}
	intent.PlayID = ""
	if err := s.RemoteCommand(ctx, intent); err != nil {
		t.Fatal(err)
	}
	runAcceptedCommand(t, s)
	if state := s.mustState(t); state.State != StateStarting || state.CurrentEntryID != st.currentEntryID {
		t.Fatalf("explicit stopped-device Play did not enter observation-backed startup: %+v", state)
	}
}
