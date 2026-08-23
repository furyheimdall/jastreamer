package candidates

import (
	"slices"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
)

func TestIndexSnapshotIsSortedAndDetachedFromCatalog(t *testing.T) {
	snapshot := catalog.EmptySnapshot()
	snapshot.Revision = 17
	for _, id := range []catalog.TrackID{"z", "a", "m"} {
		snapshot.Tracks[id] = catalog.Track{
			TrackID: id, RecordingID: catalog.RecordingID(id + "-recording"), Available: true,
			Metadata: catalog.Metadata{Genres: []string{"Ambient"}},
		}
	}
	index := IndexSnapshot(snapshot)
	if !slices.Equal(candidateTrackIDs(index.Tracks), []catalog.TrackID{"a", "m", "z"}) {
		t.Fatalf("index order = %v", candidateTrackIDs(index.Tracks))
	}
	track := snapshot.Tracks["a"]
	track.Metadata.Genres[0] = "mutated"
	snapshot.Tracks["a"] = track
	if index.Revision != 17 || !slices.Equal(index.Tracks[0].Signals.Genres, []string{"Ambient"}) {
		t.Fatalf("index aliases catalog: %+v", index)
	}
}

func candidateTrackIDs(values []Track) []catalog.TrackID {
	ids := make([]catalog.TrackID, len(values))
	for index := range values {
		ids[index] = values[index].Catalog.TrackID
	}
	return ids
}
