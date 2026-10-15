package player

import (
	"context"
	"fmt"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

// RemoteCommand admits a physical receiver's intent through the same durable
// command queue as Control. A receiver never owns a second queue or play state.
func (s *Service) RemoteCommand(ctx context.Context, control output.RemoteControl) error {
	s.opMu.Lock()
	accepted, err := s.acceptRemoteLocked(ctx, control)
	s.opMu.Unlock()
	if err != nil {
		return err
	}
	if accepted {
		s.notify("player")
		s.signalWorker()
	}
	return nil
}

func (s *Service) acceptRemoteLocked(ctx context.Context, control output.RemoteControl) (bool, error) {
	st, err := s.loadState(ctx)
	if err != nil {
		return false, err
	}
	if control.DeviceID == "" || control.Current == nil || st.rendererID != control.DeviceID || st.playID != control.PlayID || !control.Current() {
		return false, fault.New(409, "STALE_OUTPUT_CONTROL", "The receiver no longer owns this playback connection.")
	}
	command := Command{Action: control.Action, PositionMS: control.PositionMS}
	if command.Action == "playpause" {
		if st.state == StatePlaying || st.state == StateStarting {
			command.Action = "pause"
		} else {
			command.Action = "play"
		}
	}
	if control.Relative {
		if command.Action != "seek" || st.durationMS <= 0 {
			return false, fault.New(409, "SEEK_UNAVAILABLE", "The current stream cannot accept a relative seek.")
		}
		position := max(int64(0), min(st.positionMS, st.durationMS))
		// Compare before addition so even an untrusted extreme delta cannot wrap.
		if command.PositionMS < -position {
			command.PositionMS = 0
		} else if command.PositionMS > st.durationMS-position {
			command.PositionMS = st.durationMS
		} else {
			command.PositionMS += position
		}
	}
	if err := validCommand(command); err != nil {
		return false, err
	}
	// Status publication is not an intent. Repeated absolute state requests
	// cannot feed a state echo back into another transport operation.
	if (command.Action == "play" && st.state == StatePlaying) ||
		(command.Action == "pause" && st.state == StatePaused) ||
		(command.Action == "stop" && st.state == StateStopped) {
		return false, nil
	}
	id, err := randomID("command")
	if err != nil {
		return false, fmt.Errorf("player: create receiver command identity: %w", err)
	}
	if err := s.acceptCommandLocked(ctx, id, s.now().UTC().Format(time.RFC3339Nano), command); err != nil {
		return false, err
	}
	return true, nil
}
