package ranking

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"os"
	"slices"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/curation/candidates"
)

type canonicalFixture struct {
	Seed, Current, Candidate candidates.Signals
	Related                  struct{ SeedScore, CurrentScore BasisPoints }
	Tie                      struct {
		SessionSeed      string
		DecisionSequence uint64
		TrackID          string
	}
	Expected struct {
		MetadataScore BasisPoints
		RelatedScore  BasisPoints
		TiePrefix     string
	}
}

func TestCanonicalFixtureDerivesIntegerScoresAndStableTie(t *testing.T) {
	// Given
	fixture := readCanonicalFixture(t)
	request := Request{
		Candidates:  []RankedCandidate{rankedTrack(fixture.Tie.TrackID, "recording-c", "artist-c", "album-c", fixture.Candidate, candidates.TierMetadata)},
		Seed:        track("seed", "seed-recording", "seed-artist", "seed-album", fixture.Seed),
		Current:     track("current", "current-recording", "current-artist", "current-album", fixture.Current),
		SessionSeed: fixture.Tie.SessionSeed, DecisionSequence: fixture.Tie.DecisionSequence, Policy: DefaultPolicy(),
	}

	// When
	result := Select(request)

	// Then
	if result.Decision == nil {
		t.Fatalf("decision = nil, stop = %s", result.StopReason)
	}
	explanation := result.Decision.Explanation
	if explanation.CurrentMetadataScore != fixture.Expected.MetadataScore || explanation.RelatedScore != fixture.Expected.RelatedScore || explanation.TiePrefix != fixture.Expected.TiePrefix {
		t.Fatalf("explanation = %+v, want metadata=%d related=%d tie=%s", explanation, fixture.Expected.MetadataScore, fixture.Expected.RelatedScore, fixture.Expected.TiePrefix)
	}
	assertGoldenExplanation(t, explanation)
}

func TestSelectUsesFirstNonemptyBoundedRelaxationPass(t *testing.T) {
	// Given
	signal := candidates.Signals{Genres: []string{"ambient"}}
	candidate := rankedTrack("candidate", "candidate-recording", "artist-a", "album-a", signal, candidates.TierMetadata)
	history := []StartedTrack{
		{Track: track("old-album", "old-album-recording", "other", "album-a", signal)},
		{Track: track("old-artist", "old-artist-recording", "artist-a", "other-album", signal)},
		{Track: track("recent", "recent-recording", "other", "recent-album", signal)},
	}
	request := Request{Candidates: []RankedCandidate{candidate}, Seed: candidate.Candidate.Track, Current: candidate.Candidate.Track, SessionSeed: "session", Policy: DefaultPolicy(), History: history}

	// When
	result := Select(request)

	// Then
	if result.Decision == nil || result.PassesExamined != 3 || result.Decision.Explanation.RelaxationPass != 3 {
		t.Fatalf("result = %+v, want selection on pass 3", result)
	}
}

func TestSelectNeverReplaysSessionSeenRecordingAndStopsAfterFourPasses(t *testing.T) {
	// Given
	signal := candidates.Signals{Genres: []string{"rock"}}
	candidate := rankedTrack("copy", "seen-recording", "artist", "album", signal, candidates.TierMetadata)
	seen := map[candidates.RecordingKey]struct{}{"recording:seen-recording": {}}
	request := Request{Candidates: []RankedCandidate{candidate}, Seed: candidate.Candidate.Track, Current: candidate.Candidate.Track, Policy: DefaultPolicy(), Seen: seen}

	// When
	result := Select(request)

	// Then
	if result.Decision != nil || result.StopReason != StopSimilarExhausted || result.PassesExamined != MaxPasses {
		t.Fatalf("result = %+v, want finite exhaustion", result)
	}
	if len(seen) != 1 {
		t.Fatalf("seen was mutated: %v", seen)
	}
}

func TestSelectIsByteIdenticalAcrossRandomInsertionOrders(t *testing.T) {
	// Given
	signal := candidates.Signals{Genres: []string{"rock"}}
	base := []RankedCandidate{
		rankedTrack("track-a", "recording-a", "artist-a", "album-a", signal, candidates.TierMetadata),
		rankedTrack("track-b", "recording-b", "artist-b", "album-b", signal, candidates.TierMetadata),
		rankedTrack("track-c", "recording-c", "artist-c", "album-c", signal, candidates.TierMetadata),
	}
	request := Request{Seed: base[0].Candidate.Track, Current: base[0].Candidate.Track, SessionSeed: "stable", DecisionSequence: 9, Policy: DefaultPolicy()}
	var expected []byte
	random := rand.New(rand.NewSource(11))

	for iteration := range 100 {
		// When
		request.Candidates = slices.Clone(base)
		random.Shuffle(len(request.Candidates), func(left, right int) {
			request.Candidates[left], request.Candidates[right] = request.Candidates[right], request.Candidates[left]
		})
		result := Select(request)
		got, err := json.Marshal(result.Decision.Explanation)
		if err != nil {
			t.Fatal(err)
		}

		// Then
		if iteration == 0 {
			expected = got
		} else if !slices.Equal(got, expected) {
			t.Fatalf("iteration %d explanation = %s, want %s", iteration, got, expected)
		}
	}
}

func readCanonicalFixture(t *testing.T) canonicalFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/canonical.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture canonicalFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func assertGoldenExplanation(t *testing.T, explanation Explanation) {
	t.Helper()
	got, err := json.Marshal(explanation)
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/canonical-decision.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	want = bytes.TrimSuffix(want, []byte{'\n'})
	if !slices.Equal(got, want) {
		t.Fatalf("explanation = %s, want %s", got, want)
	}
}

func rankedTrack(id, recording, artist, album string, signals candidates.Signals, tier candidates.Tier) RankedCandidate {
	return RankedCandidate{Candidate: candidates.Candidate{Track: track(id, recording, artist, album, signals), Tier: tier}}
}

func track(id, recording, artist, album string, signals candidates.Signals) candidates.Track {
	return candidates.Track{Catalog: catalog.Track{TrackID: catalog.TrackID(id), RecordingID: catalog.RecordingID(recording), AlbumID: catalog.AlbumID(album), Metadata: catalog.Metadata{Artist: artist}, Available: true}, Signals: signals}
}
