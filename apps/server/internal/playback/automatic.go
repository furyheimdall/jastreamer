package playback

type automaticPreview struct {
	zoneID       ZoneID
	boundaryID   BoundaryID
	previousPlay PlayID
	sessionID    SessionID
	trackID      TrackID
	source       string
	reason       string
	state        string
	created      Revision
	terminal     Revision
	playID       PlayID
}
