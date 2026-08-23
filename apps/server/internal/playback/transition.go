package playback

import "fmt"

func Transition(from Transport, event TransportEvent) (Transport, error) {
	var to Transport
	valid := false
	switch event {
	case EventStart:
		to, valid = TransportSelecting, from == TransportIdle
	case EventReserve:
		to, valid = TransportStarting, from == TransportSelecting
	case EventBlock:
		to, valid = TransportBlocked, from == TransportSelecting
	case EventExhaust:
		to, valid = TransportIdle, from == TransportSelecting
	case EventConfirm:
		to = TransportPlaying
		valid = from == TransportStarting || from == TransportSuspended
	case EventPause:
		to, valid = TransportPaused, from == TransportPlaying
	case EventResume:
		to, valid = TransportPlaying, from == TransportPaused
	case EventBoundary:
		to, valid = TransportSelecting, from == TransportPlaying
	case EventStop:
		to = TransportIdle
		valid = from != TransportIdle
	case EventDisconnect:
		to = TransportSuspended
		valid = from == TransportSelecting || from == TransportStarting || from == TransportPlaying || from == TransportPaused || from == TransportBlocked
	case EventFailure, EventExternalOverride:
		to = TransportSuspended
		valid = from == TransportStarting || from == TransportPlaying || from == TransportPaused
	case EventRetry:
		to, valid = TransportSelecting, from == TransportBlocked
	case EventSkip:
		to, valid = TransportSelecting, from == TransportBlocked
	default:
		return "", fmt.Errorf("event %q: %w", event, ErrInvalidTransition)
	}
	if !valid {
		return "", fmt.Errorf("%s + %s: %w", from, event, ErrInvalidTransition)
	}
	return to, nil
}
