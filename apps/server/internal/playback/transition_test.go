package playback

import (
	"errors"
	"testing"
)

func Test_Transition_covers_every_transport_edge(t *testing.T) {
	tests := []struct {
		name  string
		from  Transport
		event TransportEvent
		want  Transport
	}{
		{"idle starts selection", TransportIdle, EventStart, TransportSelecting},
		{"selection reserves", TransportSelecting, EventReserve, TransportStarting},
		{"selection blocks", TransportSelecting, EventBlock, TransportBlocked},
		{"selection stops", TransportSelecting, EventExhaust, TransportIdle},
		{"start confirms", TransportStarting, EventConfirm, TransportPlaying},
		{"start ambiguity suspends", TransportStarting, EventDisconnect, TransportSuspended},
		{"play pauses", TransportPlaying, EventPause, TransportPaused},
		{"play advances", TransportPlaying, EventBoundary, TransportSelecting},
		{"play stops", TransportPlaying, EventStop, TransportIdle},
		{"play error suspends", TransportPlaying, EventFailure, TransportSuspended},
		{"pause resumes", TransportPaused, EventResume, TransportPlaying},
		{"pause stops", TransportPaused, EventStop, TransportIdle},
		{"pause disconnects", TransportPaused, EventDisconnect, TransportSuspended},
		{"block retries", TransportBlocked, EventRetry, TransportSelecting},
		{"block skips explicitly", TransportBlocked, EventSkip, TransportSelecting},
		{"block stops", TransportBlocked, EventStop, TransportIdle},
		{"suspended reconciles", TransportSuspended, EventConfirm, TransportPlaying},
		{"suspended stops", TransportSuspended, EventStop, TransportIdle},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			got, err := Transition(test.from, test.event)

			// Then
			if err != nil || got != test.want {
				t.Fatalf("transition = %s, %v; want %s", got, err, test.want)
			}
		})
	}
}

func Test_Transition_rejects_automatic_advance_after_nonboundary_events(t *testing.T) {
	tests := []struct {
		event TransportEvent
		want  Transport
		err   bool
	}{
		{EventPause, "", true},
		{EventBoundary, "", true},
		{EventStop, TransportIdle, false},
		{EventDisconnect, TransportSuspended, false},
		{EventFailure, TransportSuspended, false},
		{EventExternalOverride, TransportSuspended, false},
	}
	for _, test := range tests {
		t.Run(string(test.event), func(t *testing.T) {
			// When
			got, err := Transition(TransportPaused, test.event)

			// Then
			if test.err {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if err != nil || got != test.want || got == TransportSelecting {
				t.Fatalf("transition = %s, %v; want %s without advance", got, err, test.want)
			}
		})
	}
}

func TestTransitionMatrixIsExhaustive(t *testing.T) {
	transports := []Transport{
		TransportIdle, TransportSelecting, TransportStarting, TransportPlaying,
		TransportPaused, TransportBlocked, TransportSuspended,
	}
	events := []TransportEvent{
		EventStart, EventReserve, EventBlock, EventExhaust, EventConfirm,
		EventPause, EventResume, EventBoundary, EventStop, EventDisconnect,
		EventFailure, EventExternalOverride, EventRetry, EventSkip,
	}
	type edge struct {
		from  Transport
		event TransportEvent
	}
	valid := map[edge]Transport{
		{TransportIdle, EventStart}:                TransportSelecting,
		{TransportSelecting, EventReserve}:         TransportStarting,
		{TransportSelecting, EventBlock}:           TransportBlocked,
		{TransportSelecting, EventExhaust}:         TransportIdle,
		{TransportSelecting, EventStop}:            TransportIdle,
		{TransportSelecting, EventDisconnect}:      TransportSuspended,
		{TransportStarting, EventConfirm}:          TransportPlaying,
		{TransportStarting, EventStop}:             TransportIdle,
		{TransportStarting, EventDisconnect}:       TransportSuspended,
		{TransportStarting, EventFailure}:          TransportSuspended,
		{TransportStarting, EventExternalOverride}: TransportSuspended,
		{TransportPlaying, EventPause}:             TransportPaused,
		{TransportPlaying, EventBoundary}:          TransportSelecting,
		{TransportPlaying, EventStop}:              TransportIdle,
		{TransportPlaying, EventDisconnect}:        TransportSuspended,
		{TransportPlaying, EventFailure}:           TransportSuspended,
		{TransportPlaying, EventExternalOverride}:  TransportSuspended,
		{TransportPaused, EventResume}:             TransportPlaying,
		{TransportPaused, EventStop}:               TransportIdle,
		{TransportPaused, EventDisconnect}:         TransportSuspended,
		{TransportPaused, EventFailure}:            TransportSuspended,
		{TransportPaused, EventExternalOverride}:   TransportSuspended,
		{TransportBlocked, EventRetry}:             TransportSelecting,
		{TransportBlocked, EventSkip}:              TransportSelecting,
		{TransportBlocked, EventStop}:              TransportIdle,
		{TransportBlocked, EventDisconnect}:        TransportSuspended,
		{TransportSuspended, EventConfirm}:         TransportPlaying,
		{TransportSuspended, EventStop}:            TransportIdle,
	}
	for _, from := range transports {
		for _, event := range events {
			want, ok := valid[edge{from, event}]
			got, err := Transition(from, event)
			if ok {
				if err != nil || got != want {
					t.Errorf("%s + %s = %s, %v; want %s", from, event, got, err, want)
				}
			} else if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("%s + %s error = %v, want invalid transition", from, event, err)
			}
		}
	}
}
