package candidates

import (
	"slices"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/analysis"
	"github.com/jakestreamer/jstreamer-server/internal/catalog"
)

func TestCandidateRetrievalCountsFiniteExhaustion(t *testing.T) {
	seed := track("seed", trackMetadata{"seed-r", "seed-f", "artist"}, signals{genres: []string{"rock"}})
	t.Run("one track no signal", func(t *testing.T) {
		got := Retrieve(Request{Index: NewIndex(7, []Track{seed}), CatalogRevision: 7, Seed: seed, Current: seed, PageSize: 1})
		if len(got.Candidates) != 0 || got.TotalEligible != 1 || got.FilteredEligible != 0 || got.ScoredEligible != 0 || got.RelatedEligible != 0 || got.PagesRead != 1 {
			t.Fatalf("result = %+v", got)
		}
	})
	t.Run("all related seen", func(t *testing.T) {
		values := []Track{
			track("a", trackMetadata{"a-r", "a-f", "other"}, signals{genres: []string{"rock"}}),
			track("b", trackMetadata{"b-r", "b-f", "other"}, signals{genres: []string{"rock"}}),
			track("c", trackMetadata{"c-r", "c-f", "artist"}, signals{}),
		}
		seen := map[RecordingKey]struct{}{"recording:a-r": {}, "recording:b-r": {}, "recording:c-r": {}}
		got := Retrieve(Request{Index: NewIndex(8, values), CatalogRevision: 8, Seed: seed, Current: seed, Seen: seen, PageSize: 1})
		if len(got.Candidates) != 0 || got.TotalEligible != 3 || got.FilteredEligible != 0 || got.ScoredEligible != 0 || got.RelatedEligible != 3 || got.PagesRead != 3 {
			t.Fatalf("result = %+v", got)
		}
	})
}

func TestCandidateRetrievalHardFiltersAndSamePath(t *testing.T) {
	seed := track("seed", trackMetadata{"seed-r", "seed-f", "artist"}, signals{genres: []string{"rock"}})
	seed.Catalog.RelativePath = "Album/Seed.flac"
	current := track("current", trackMetadata{"current-r", "current-f", "artist"}, signals{genres: []string{"rock"}})
	values := []Track{
		track("same-path", trackMetadata{"path-r", "path-f", "other"}, signals{genres: []string{"rock"}}),
		track("unsupported", trackMetadata{"unsupported-r", "unsupported-f", "other"}, signals{genres: []string{"rock"}}),
		track("failed", trackMetadata{"failed-r", "failed-f", "other"}, signals{genres: []string{"rock"}}),
		track("eligible", trackMetadata{"eligible-r", "eligible-f", "other"}, signals{genres: []string{"rock"}}),
	}
	values[0].Catalog.RelativePath = "album\\seed.flac"
	values[1].Catalog.Format = "aac"
	values[2].Catalog.AnalysisStatus = catalog.AnalysisFailed
	got := Retrieve(Request{Index: NewIndex(1, values), CatalogRevision: 1, Seed: seed, Current: current, SuppressSamePath: true, PageSize: 2})
	assertCandidateIDs(t, got.Candidates, []catalog.TrackID{"eligible"})
	if got.TotalEligible != 2 || got.FilteredEligible != 1 || got.ScoredEligible != 1 || got.PagesRead != 2 {
		t.Fatalf("counts = %+v", got)
	}
}

func TestCandidateRetrievalRejectsRevisionAndIncompatibleAcoustics(t *testing.T) {
	seed := track("seed", trackMetadata{"seed-r", "seed-f", "seed"}, signals{
		genres: []string{"ambient"}, acoustic: []byte{1, 2, 3},
	})
	stale := track("stale", trackMetadata{"stale-r", "stale-f", "other"}, signals{
		genres: []string{"ambient"}, acoustic: []byte{1, 2, 4},
	})
	stale.Catalog.AnalysisStatus = catalog.AnalysisComplete
	stale.Catalog.AnalysisProvenance = analysis.CurrentProvenance()
	stale.Catalog.AnalysisProvenance.AnalyzerVersion = "old"
	index := NewIndex(5, []Track{stale})
	if got := Retrieve(Request{Index: index, CatalogRevision: 4, Seed: seed, Current: seed}); got.RevisionMatched || len(got.Candidates) != 0 || got.PagesRead != 0 {
		t.Fatalf("revision mismatch = %+v", got)
	}
	if got := Retrieve(Request{Index: index, CatalogRevision: 5, Seed: seed, Current: seed}); len(got.Candidates) != 0 || got.ScoredEligible != 0 {
		t.Fatalf("stale acoustic result = %+v", got)
	}
}

func TestCandidateRetrievalRejectsUnknownMediaFormat(t *testing.T) {
	seed := track("seed", trackMetadata{"seed-r", "seed-f", "seed"}, signals{genres: []string{"ambient"}})
	unsupported := track("unsupported", trackMetadata{"candidate-r", "candidate-f", "other"}, signals{genres: []string{"ambient"}})
	unsupported.Catalog.Format = ""
	got := Retrieve(Request{Index: NewIndex(1, []Track{unsupported}), CatalogRevision: 1, Seed: seed, Current: seed})
	if got.TotalEligible != 0 || len(got.Candidates) != 0 {
		t.Fatalf("unsupported format result = %+v", got)
	}
}

func TestCompositeDistanceRenormalizesAndBoundsBonuses(t *testing.T) {
	seed := track("seed", trackMetadata{"seed-r", "seed-f", "artist"}, signals{genres: []string{"rock"}, acoustic: []byte{10}})
	seed.Catalog.AlbumID = "album"
	metadataOnly := track("metadata", trackMetadata{"metadata-r", "metadata-f", "other"}, signals{genres: []string{"rock"}})
	allBonuses := track("bonuses", trackMetadata{"bonus-r", "bonus-f", "artist"}, signals{genres: []string{"rock"}, acoustic: []byte{10}})
	allBonuses.Catalog.AlbumID = "album"
	got := Retrieve(Request{Index: NewIndex(1, []Track{metadataOnly, allBonuses}), CatalogRevision: 1, Seed: seed, Current: seed})
	if !slices.Equal(candidateIDs(got.Candidates), []catalog.TrackID{"bonuses", "metadata"}) {
		t.Fatalf("ordered IDs = %v", candidateIDs(got.Candidates))
	}
	if got.Candidates[0].GenreBonus != GenreBonusLimit || got.Candidates[0].ArtistBonus != ArtistBonusLimit || got.Candidates[0].AlbumBonus != AlbumBonusLimit || got.Candidates[0].CompositeDistance != 0 {
		t.Fatalf("bounded bonuses = %+v", got.Candidates[0])
	}
	if got.Candidates[1].CompositeDistance != 0 {
		t.Fatalf("metadata-only distance = %d, want renormalized zero", got.Candidates[1].CompositeDistance)
	}
}

func candidateIDs(values []Candidate) []catalog.TrackID {
	ids := make([]catalog.TrackID, len(values))
	for index := range values {
		ids[index] = values[index].Track.Catalog.TrackID
	}
	return ids
}
