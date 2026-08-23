package decision

import (
	"strconv"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/curation/candidates"
	"github.com/jakestreamer/jstreamer-server/internal/curation/ranking"
)

func testBoundary(id string) Boundary {
	return Boundary{ID: BoundaryID(id)}
}

func testExplicit(id, track string, available bool) ExplicitEntry {
	return ExplicitEntry{
		ID:        QueueEntryID(id),
		TrackID:   catalog.TrackID(track),
		Available: available,
	}
}

func testCurationTrack(id string) candidates.Track {
	return candidates.Track{
		Catalog: catalog.Track{
			TrackID:     catalog.TrackID(id),
			RecordingID: catalog.RecordingID("recording-" + id),
			AlbumID:     catalog.AlbumID("album-" + id),
			Format:      catalog.FormatFLAC,
			Metadata:    catalog.Metadata{Artist: "artist-" + id},
			Available:   true,
		},
		Signals: candidates.Signals{Genres: []string{"ambient"}},
	}
}

func testSimilarSnapshot(candidateIDs ...string) Snapshot {
	seed := testCurationTrack("seed")
	tracks := []candidates.Track{seed}
	for _, id := range candidateIDs {
		tracks = append(tracks, testCurationTrack(id))
	}
	return Snapshot{
		Policy: PolicySimilar,
		Similar: SimilarSnapshot{
			Index:            candidates.NewIndex(1, tracks),
			Seed:             seed,
			Current:          seed,
			PageSize:         2,
			RankingPolicy:    ranking.DefaultPolicy(),
			SessionSeed:      "session-seed",
			DecisionSequence: 7,
		},
	}
}

func testAlbumSnapshot(trackCount int) Snapshot {
	catalogSnapshot := catalog.EmptySnapshot()
	for number := 1; number <= trackCount; number++ {
		id := catalog.TrackID("album-track-" + strconv.Itoa(number))
		metadata := catalog.Metadata{Disc: 1, Track: number}
		catalogSnapshot.Tracks[id] = catalog.Track{
			TrackID:     id,
			RecordingID: catalog.RecordingID("recording-" + string(id)),
			AlbumID:     "album-a",
			Available:   true,
			Order:       catalog.NewOrderKey(metadata, string(id), id),
		}
	}
	anchor := catalogSnapshot.Tracks["album-track-1"]
	return Snapshot{
		Policy:  PolicyAlbum,
		Catalog: catalogSnapshot,
		Album: AlbumSnapshot{
			AlbumID: "album-a",
			Anchor:  anchor.Order,
			Started: map[catalog.RecordingID]bool{anchor.RecordingID: true},
		},
	}
}

func requirePlay(t *testing.T, outcome Outcome) Play {
	t.Helper()
	play, ok := outcome.(Play)
	if !ok {
		t.Fatalf("outcome = %#v, want Play", outcome)
	}
	return play
}

func requireStop(t *testing.T, outcome Outcome) Stop {
	t.Helper()
	stop, ok := outcome.(Stop)
	if !ok {
		t.Fatalf("outcome = %#v, want Stop", outcome)
	}
	return stop
}
