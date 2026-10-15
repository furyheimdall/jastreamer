package player

import (
	"errors"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

type signalMedia struct {
	*fakeMedia
	playID string
	path   output.SignalPath
}

// Retain the evidence on Revoke deliberately: Player must independently fence
// stale provider evidence with its own transport state and play identity.
func (media *signalMedia) SignalPath(_ string, playID string) (output.SignalPath, bool) {
	return media.path, playID == media.playID
}

func TestSnapshotSignalPathRequiresHealthyCurrentPlayback(t *testing.T) {
	for _, change := range []string{
		"playing", "paused", "pending", "stopped", "starting", "error", "error message", "warning", "offline", "missing output",
		"different renderer", "different track", "different play", "missing play", "recovered intent", "unknown precision", "unknown scope", "invalid PCM width",
	} {
		t.Run(change, func(t *testing.T) {
			service, db, devices := newPlayerTestService(t)
			st := startPlayCountTrack(t, service, "a")
			media := &signalMedia{fakeMedia: service.media.(*fakeMedia), playID: st.playID, path: output.SignalPath{
				Scope: "server", Mode: "original", RendererID: st.rendererID, TrackID: "a", SampleRate: 44100, ValidBits: 16, Channels: 2,
			}}
			service.media = media
			if _, err := db.ExecContext(t.Context(), "UPDATE player_state SET state='playing' WHERE singleton=1"); err != nil {
				t.Fatal(err)
			}
			query := ""
			switch change {
			case "paused":
				query = "UPDATE player_state SET state='paused' WHERE singleton=1"
			case "pending":
				if _, err := service.Command(t.Context(), Command{Action: "pause"}); err != nil {
					t.Fatal(err)
				}
			case "stopped", "starting", "error":
				if _, err := db.ExecContext(t.Context(), "UPDATE player_state SET state=? WHERE singleton=1", change); err != nil {
					t.Fatal(err)
				}
			case "error message":
				query = "UPDATE player_state SET error='playback failed' WHERE singleton=1"
			case "warning":
				service.observationFailure = observationFailure{rendererID: st.rendererID, playID: st.playID, count: observationWarningThreshold, warning: StatusWarning{ID: 1, Message: "output status unknown"}}
			case "offline":
				devices.device.Online = false
			case "missing output":
				devices.device.ID = "other"
			case "different renderer":
				media.path.RendererID = "other"
			case "different track":
				media.path.TrackID = "b"
			case "different play":
				media.playID = "obsolete-play"
			case "missing play":
				query = "UPDATE player_state SET play_id='' WHERE singleton=1"
			case "recovered intent":
				query = "UPDATE player_state SET resume_required=1 WHERE singleton=1"
			case "unknown precision":
				media.path.ValidBits = 0
			case "unknown scope":
				media.path.Scope = "receiver"
			case "invalid PCM width":
				media.path.Mode = "pcm"
				media.path.ContainerBits = 8
			}
			if query != "" {
				if _, err := db.ExecContext(t.Context(), query); err != nil {
					t.Fatal(err)
				}
			}
			state, err := service.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			want := change == "playing" || change == "paused"
			if (state.SignalPath != nil) != want {
				t.Fatalf("signal path = %+v for state %+v", state.SignalPath, state)
			}
		})
	}
}

func TestSignalPathDoesNotSurviveStopFailureOutputChangeOrRestart(t *testing.T) {
	for _, transition := range []string{"stop", "pause failure", "output change", "restart"} {
		t.Run(transition, func(t *testing.T) {
			service, db, devices := newPlayerTestService(t)
			st := startPlayCountTrack(t, service, "a")
			media := &signalMedia{fakeMedia: service.media.(*fakeMedia), playID: st.playID, path: output.SignalPath{
				Scope: "server", Mode: "original", RendererID: st.rendererID, TrackID: "a", SampleRate: 44100, ValidBits: 16, Channels: 2,
			}}
			service.media = media
			observePlayCountPosition(service, st, time.Now().UTC(), 1000, 100000)
			before, err := service.Snapshot(t.Context())
			if err != nil || before.SignalPath == nil {
				t.Fatalf("missing initial live proof: %+v err=%v", before, err)
			}
			if transition == "restart" {
				service, err = newService(t.Context(), db, service.lib, devices, media, time.Second, nil)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				action := "stop"
				if transition == "pause failure" {
					action = "pause"
					devices.fail["pause"] = output.NewActionError(output.ErrorTransport, "Pause", 0, errors.New("unreachable"))
				}
				if _, err := service.Command(t.Context(), Command{Action: action}); err != nil {
					t.Fatal(err)
				}
				runAcceptedCommand(t, service)
				if transition == "output change" {
					devices.device.ID = "new-renderer"
					if _, err := service.SelectOutput(t.Context(), "new-renderer"); err != nil {
						t.Fatal(err)
					}
				}
			}
			after, err := service.Snapshot(t.Context())
			if err != nil || after.SignalPath != nil {
				t.Fatalf("stale proof survived %s: %+v err=%v", transition, after, err)
			}
		})
	}
}
