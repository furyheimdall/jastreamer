package ranking

import (
	"cmp"
	"slices"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/curation/candidates"
)

func TestAnchorSimilarityRenormalizesAvailableSignals(t *testing.T) {
	tests := []struct {
		name     string
		anchor   candidates.Signals
		value    candidates.Signals
		acoustic AcousticSimilarity
		want     BasisPoints
	}{
		{"metadata only", candidates.Signals{Genres: []string{"rock"}}, candidates.Signals{Genres: []string{"rock"}}, AcousticSimilarity{}, 10000},
		{"acoustic only", candidates.Signals{}, candidates.Signals{}, AcousticSimilarity{Available: true, Score: 8000}, 8000},
		{"both available", candidates.Signals{Genres: []string{"rock"}}, candidates.Signals{Genres: []string{"rock"}}, AcousticSimilarity{Available: true, Score: 6000}, 7800},
		{"candidate missing acoustic", candidates.Signals{Genres: []string{"rock"}}, candidates.Signals{Genres: []string{"rock"}}, AcousticSimilarity{}, 10000},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			anchor := newMetadataProfile(test.anchor)
			candidate := newMetadataProfile(test.value)

			// When
			_, got := anchorSimilarity(anchor, candidate, test.acoustic)

			// Then
			if got != test.want {
				t.Fatalf("similarity = %d, want %d", got, test.want)
			}
		})
	}
}

func TestMetadataScoreRenormalizesOnlyComparableGroups(t *testing.T) {
	anchor := candidates.Signals{Genres: []string{"rock"}, Styles: []string{"dream pop"}}
	candidate := candidates.Signals{Genres: []string{"rock"}}
	if got := MetadataScore(anchor, candidate); got != 10000 {
		t.Fatalf("metadata score = %d, want 10000", got)
	}
}

func TestRelatedScoreClampsBoundaryInputs(t *testing.T) {
	if got := RelatedScore(-100, 20000); got != 4000 {
		t.Fatalf("clamped related score = %d, want 4000", got)
	}
}

func TestSelectedExplanationReconstructsClampedTotalScore(t *testing.T) {
	value := rankedTrack(
		"candidate", "candidate-recording", "artist", "album",
		candidates.Signals{}, candidates.TierAcoustic,
	)
	value.SeedAcoustic = AcousticSimilarity{Available: true, Score: -100}
	value.CurrentAcoustic = AcousticSimilarity{Available: true, Score: 20000}
	result := Select(Request{
		Candidates: []RankedCandidate{value}, Seed: value.Candidate.Track,
		Current: value.Candidate.Track, Policy: DefaultPolicy(),
	})
	if result.Decision == nil {
		t.Fatal("decision = nil")
	}
	explanation := result.Decision.Explanation
	if explanation.SeedSimilarity != 0 || explanation.CurrentSimilarity != 10000 {
		t.Fatalf("clamped similarities = %d/%d", explanation.SeedSimilarity, explanation.CurrentSimilarity)
	}
	if got := RelatedScore(explanation.SeedSimilarity, explanation.CurrentSimilarity); got != explanation.RelatedScore {
		t.Fatalf("reconstructed score = %d, stored = %d", got, explanation.RelatedScore)
	}
	if explanation.ScoreBand != int(explanation.RelatedScore)/explanation.ScoringPolicy.ScoreBandWidth {
		t.Fatalf("score band = %d, score = %d", explanation.ScoreBand, explanation.RelatedScore)
	}
	policy := explanation.ScoringPolicy
	if policy.MetadataGenreWeight != 50 || policy.MetadataStyleWeight != 30 ||
		policy.MetadataMoodTagWeight != 20 || policy.AcousticWeight != 55 ||
		policy.MetadataWeight != 45 || policy.SeedWeight != 60 || policy.CurrentWeight != 40 {
		t.Fatalf("scoring policy trace = %+v", policy)
	}
}

func TestCompareScoredDefinesTotalRankingOrder(t *testing.T) {
	// Given
	values := []scoredCandidate{
		scoreFixture("track-final-b", candidates.TierMetadata, 700, 0, 0, 9),
		scoreFixture("track-tier", candidates.TierAcoustic, 10000, 0, 0, 1),
		scoreFixture("track-band", candidates.TierMetadata, 249, 0, 0, 1),
		scoreFixture("track-artist-count", candidates.TierMetadata, 700, 1, 0, 1),
		scoreFixture("track-album-count", candidates.TierMetadata, 700, 0, 1, 1),
		scoreFixture("track-exact", candidates.TierMetadata, 699, 0, 0, 1),
		scoreFixture("track-tie", candidates.TierMetadata, 700, 0, 0, 10),
		scoreFixture("track-final-a", candidates.TierMetadata, 700, 0, 0, 9),
	}

	// When
	slices.SortFunc(values, compareScored)

	// Then
	want := []string{"track-final-a", "track-final-b", "track-tie", "track-exact", "track-album-count", "track-artist-count", "track-band", "track-tier"}
	for index, value := range values {
		got := string(value.input.Candidate.Track.Catalog.TrackID)
		if got != want[index] {
			t.Fatalf("rank %d = %s, want %s", index, got, want[index])
		}
		for otherIndex, other := range values {
			if sign(compareScored(value, other)) != -sign(compareScored(other, value)) {
				t.Fatalf("comparison is not antisymmetric at %d,%d", index, otherIndex)
			}
		}
	}
}

func TestSelectUsesFourthPassWhenOnlyRecentArtistRemains(t *testing.T) {
	// Given
	signal := candidates.Signals{Genres: []string{"rock"}}
	candidate := rankedTrack("related", "related-recording", "artist", "album-new", signal, candidates.TierMetadata)
	history := []StartedTrack{{Track: track("current", "current-recording", "artist", "album-current", signal)}}
	request := Request{Candidates: []RankedCandidate{candidate}, Seed: candidate.Candidate.Track, Current: history[0].Track, Policy: DefaultPolicy(), History: history}

	// When
	result := Select(request)

	// Then
	if result.Decision == nil || result.PassesExamined != MaxPasses || result.Decision.Explanation.EffectiveArtistGap != 0 {
		t.Fatalf("result = %+v, want artist relaxation on pass 4", result)
	}
}

func scoreFixture(id string, tier candidates.Tier, related BasisPoints, artistCount, albumCount int, tie uint64) scoredCandidate {
	value := rankedTrack(id, "recording-"+id, "artist-"+id, "album-"+id, candidates.Signals{}, tier)
	return scoredCandidate{input: value, related: related, generatedArtistCount: artistCount, generatedAlbumCount: albumCount, tie: tie}
}

func sign(value int) int { return cmp.Compare(value, 0) }
