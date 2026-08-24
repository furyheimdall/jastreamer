package playback

import (
	"github.com/jastreamer/jastreamer-server/internal/decision"
)

type commitStage string

const commitStageAfterDecision commitStage = "after_decision"

type NextRequest struct {
	ZoneID   ZoneID
	Boundary Boundary
	Snapshot decision.Snapshot
}

type StartFailureRequest struct {
	ZoneID     ZoneID
	BoundaryID BoundaryID
	PlayID     PlayID
	Snapshot   decision.Snapshot
}

type ContinuationPolicy struct {
	Mode            decision.Policy
	SessionOverride decision.Policy
	ArtistGap       int
	AlbumGap        int
	Revision        int64
}

type boundaryKey struct {
	zoneID     ZoneID
	sessionID  SessionID
	boundaryID BoundaryID
}

type PolicyUpdate struct {
	ZoneID           ZoneID
	ExpectedRevision int64
	Mode             decision.Policy
	SessionOverride  decision.Policy
	ArtistGap        int
	AlbumGap         int
}

func (policy ContinuationPolicy) effectiveMode() decision.Policy {
	if policy.SessionOverride != "" {
		return policy.SessionOverride
	}
	return policy.Mode
}
