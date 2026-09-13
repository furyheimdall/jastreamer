package cast

import (
	"testing"
	"time"
)

func TestObservationCompletionRequiresOwnedFinishedStatus(t *testing.T) {
	duration := 120.0
	position := 120.0
	status := mediaStatus{
		MediaSessionID: 7, PlayerState: "IDLE", IdleReason: "FINISHED",
		CurrentTime: &position, Media: mediaDescription{ContentID: "http://127.0.0.1/media/play", Duration: &duration},
	}
	owned := observationFromStatus(status, true, time.Unix(100, 0))
	if !owned.CompletionKnown || !owned.Completed || owned.State != "stopped" || !owned.HasURI {
		t.Fatalf("owned FINISHED observation = %+v", owned)
	}
	unowned := observationFromStatus(status, false, time.Unix(100, 0))
	if !unowned.CompletionKnown || unowned.Completed || unowned.TransportStatus != "ERROR_OCCURRED" {
		t.Fatalf("unowned FINISHED observation = %+v", unowned)
	}
}

func TestObservationDoesNotCompleteCancelledOrInterruptedMedia(t *testing.T) {
	for _, reason := range []string{"CANCELLED", "CANCELED", "INTERRUPTED", "ERROR"} {
		t.Run(reason, func(t *testing.T) {
			status := mediaStatus{
				MediaSessionID: 7, PlayerState: "IDLE", IdleReason: reason,
				Media: mediaDescription{ContentID: "http://127.0.0.1/media/play"},
			}
			observation := observationFromStatus(status, true, time.Unix(100, 0))
			if !observation.CompletionKnown || observation.Completed || observation.TransportStatus != "ERROR_OCCURRED" {
				t.Fatalf("%s observation = %+v", reason, observation)
			}
		})
	}
}

func TestObservationTreatsUnknownIdleAsKnownNonCompletion(t *testing.T) {
	status := mediaStatus{
		MediaSessionID: 7, PlayerState: "IDLE",
		Media: mediaDescription{ContentID: "http://127.0.0.1/media/play"},
	}
	observation := observationFromStatus(status, true, time.Unix(100, 0))
	if !observation.CompletionKnown || observation.Completed || observation.TransportStatus != "ERROR_OCCURRED" {
		t.Fatalf("unknown IDLE observation = %+v", observation)
	}
}

func TestObservationMarksPlayingAsKnownNonCompletion(t *testing.T) {
	position := 10.0
	status := mediaStatus{
		MediaSessionID: 7, PlayerState: "PLAYING", CurrentTime: &position,
		Media: mediaDescription{ContentID: "http://127.0.0.1/media/play"},
	}
	observation := observationFromStatus(status, true, time.Unix(100, 0))
	if !observation.CompletionKnown || observation.Completed || observation.State != "playing" {
		t.Fatalf("playing observation = %+v", observation)
	}
}
