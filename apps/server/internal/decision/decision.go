package decision

import (
	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/continuation/album"
	"github.com/jastreamer/jastreamer-server/internal/curation/candidates"
	"github.com/jastreamer/jastreamer-server/internal/curation/ranking"
)

func DecideNext(snapshot Snapshot, boundary Boundary) Outcome {
	if len(snapshot.Explicit) > 0 {
		head := snapshot.Explicit[0]
		if !head.Available {
			return Block{
				BoundaryID: boundary.ID, TrackID: head.TrackID,
				QueueEntryID: head.ID, Reason: ReasonBlockExplicit,
			}
		}
		track := snapshot.Catalog.Tracks[head.TrackID]
		if track.TrackID == "" {
			track.TrackID = head.TrackID
		}
		return Play{
			BoundaryID: boundary.ID, TrackID: head.TrackID,
			RecordingID: track.RecordingID, RecordingKey: candidates.RecordingKeyFor(track),
			AlbumID: track.AlbumID, Order: track.Order,
			QueueEntryID: head.ID, Source: SourceExplicit, Reason: ReasonPlayExplicit,
		}
	}
	failed := make(map[catalog.TrackID]struct{}, len(snapshot.FailedGenerated))
	for _, trackID := range snapshot.FailedGenerated {
		failed[trackID] = struct{}{}
	}
	if len(failed) >= MaxGeneratedAttempts {
		return Stop{BoundaryID: boundary.ID, Reason: ReasonStopAutoFailureLimit}
	}
	switch snapshot.Policy {
	case PolicyAlbum:
		return decideAlbum(snapshot, boundary)
	case PolicySimilar:
		return decideSimilar(snapshot, boundary)
	case PolicyStop:
		return Stop{BoundaryID: boundary.ID, Reason: ReasonStopModeOff}
	default:
		return Stop{BoundaryID: boundary.ID, Reason: ReasonStopModeOff}
	}
}

func decideAlbum(snapshot Snapshot, boundary Boundary) Outcome {
	failed := make(map[catalog.TrackID]struct{}, len(snapshot.FailedGenerated))
	for _, trackID := range snapshot.FailedGenerated {
		failed[trackID] = struct{}{}
	}
	anchor := snapshot.Album.Anchor
	for range len(failed) + 1 {
		result := album.Select(album.Request{
			Snapshot: snapshot.Catalog, AlbumID: snapshot.Album.AlbumID,
			Anchor: anchor, Started: snapshot.Album.Started,
		})
		switch result.Reason {
		case album.NoAlbum:
			return Stop{BoundaryID: boundary.ID, Reason: ReasonStopNoAlbum}
		case album.AlbumComplete:
			return Stop{BoundaryID: boundary.ID, Reason: ReasonStopAlbumComplete}
		case album.Continue:
			if _, rejected := failed[result.Track.TrackID]; !rejected {
				return Play{
					BoundaryID: boundary.ID, TrackID: result.Track.TrackID,
					RecordingID:  result.Track.RecordingID,
					RecordingKey: candidates.RecordingKeyFor(result.Track),
					AlbumID:      result.Track.AlbumID, Order: result.Track.Order,
					Source: SourceAlbum, Reason: ReasonPlayAlbum,
				}
			}
			anchor = result.Track.Order
		default:
			return Stop{BoundaryID: boundary.ID, Reason: ReasonStopAlbumComplete}
		}
	}
	return Stop{BoundaryID: boundary.ID, Reason: ReasonStopAlbumComplete}
}

func decideSimilar(snapshot Snapshot, boundary Boundary) Outcome {
	blacklist := make(map[catalog.TrackID]struct{}, len(snapshot.FailedGenerated))
	for _, trackID := range snapshot.FailedGenerated {
		blacklist[trackID] = struct{}{}
	}
	request := candidates.Request{
		Index: snapshot.Similar.Index, CatalogRevision: snapshot.Similar.Index.Revision,
		Seed: snapshot.Similar.Seed, Current: snapshot.Similar.Current,
		Seen: snapshot.Similar.Seen, PolicyExcluded: snapshot.Similar.PolicyExcluded,
		StartBlacklist: blacklist, SuppressSamePath: snapshot.Similar.SuppressSamePath,
		PageSize: snapshot.Similar.PageSize,
	}
	retrieved := candidates.Retrieve(request)
	if len(retrieved.Candidates) == 0 {
		if retrieved.RelatedEligible == 0 {
			return Stop{BoundaryID: boundary.ID, Reason: ReasonStopSimilarNoSignal}
		}
		return Stop{BoundaryID: boundary.ID, Reason: ReasonStopSimilarExhausted}
	}
	ranked := make([]ranking.RankedCandidate, len(retrieved.Candidates))
	for index, candidate := range retrieved.Candidates {
		scores := snapshot.Similar.Acoustic[candidate.Track.Catalog.TrackID]
		ranked[index] = ranking.RankedCandidate{
			Candidate: candidate, SeedAcoustic: scores.Seed, CurrentAcoustic: scores.Current,
		}
	}
	result := ranking.Select(ranking.Request{
		Candidates: ranked, Seed: snapshot.Similar.Seed, Current: snapshot.Similar.Current,
		SessionSeed:      snapshot.Similar.SessionSeed,
		DecisionSequence: snapshot.Similar.DecisionSequence,
		Policy:           snapshot.Similar.RankingPolicy, History: snapshot.Similar.History,
		Seen: snapshot.Similar.Seen,
	})
	if result.Decision == nil {
		return Stop{BoundaryID: boundary.ID, Reason: ReasonStopSimilarExhausted}
	}
	track := result.Decision.Candidate.Track.Catalog
	return Play{
		BoundaryID:   boundary.ID,
		TrackID:      track.TrackID,
		RecordingID:  track.RecordingID,
		RecordingKey: candidates.RecordingKeyFor(track),
		AlbumID:      track.AlbumID,
		Order:        track.Order,
		Source:       SourceSimilar,
		Reason:       ReasonPlaySimilar,
		Explanation:  result.Decision.Explanation,
	}
}
