package album

import (
	"sort"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

type StopReason string

const (
	Continue      StopReason = "CONTINUE"
	NoAlbum       StopReason = "STOP_NO_ALBUM"
	AlbumComplete StopReason = "STOP_ALBUM_COMPLETE"
)

type Result struct {
	Track  catalog.Track
	Reason StopReason
}

type Request struct {
	Snapshot catalog.Snapshot
	AlbumID  catalog.AlbumID
	Anchor   catalog.OrderKey
	Started  map[catalog.RecordingID]bool
}

func Select(request Request) Result {
	if request.AlbumID == "" {
		return Result{Reason: NoAlbum}
	}
	anchorTrack := catalog.Track{TrackID: request.Anchor.TrackID, Order: request.Anchor}
	foundAlbum := false
	candidates := make([]catalog.Track, 0)
	for _, track := range request.Snapshot.Tracks {
		if track.AlbumID == request.AlbumID {
			foundAlbum = true
		}
		if track.AlbumID != request.AlbumID ||
			!track.Available ||
			catalog.CompareTrackOrder(track, anchorTrack) <= 0 ||
			request.Started[track.RecordingID] {
			continue
		}
		candidates = append(candidates, track)
	}
	sort.Slice(candidates, func(left, right int) bool { return catalog.CompareTrackOrder(candidates[left], candidates[right]) < 0 })
	if !foundAlbum {
		return Result{Reason: NoAlbum}
	}
	if len(candidates) == 0 {
		return Result{Reason: AlbumComplete}
	}
	return Result{Track: candidates[0], Reason: Continue}
}
