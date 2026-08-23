package candidates

import (
	"slices"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/analysis"
	"github.com/jakestreamer/jstreamer-server/internal/catalog"
)

func TestCandidateRetrievalPageSizeInvariant(t *testing.T) {
	// Given
	seed := track("seed", trackMetadata{"seed-recording", "seed-fingerprint", "The Artist"}, signals{genres: []string{" Dream  Pop "}, acoustic: []byte{10, 10}})
	current := seed
	tracks := []Track{
		track("unrelated", trackMetadata{"r-unrelated", "f-unrelated", "Someone Else"}, signals{}),
		track("same-artist", trackMetadata{"r-artist", "f-artist", "the artist"}, signals{}),
		track("acoustic-far", trackMetadata{"r-far", "f-far", "Other"}, signals{acoustic: []byte{30, 30}}),
		track("metadata", trackMetadata{"r-metadata", "f-metadata", "Other"}, signals{genres: []string{"dream pop"}}),
		track("acoustic-near", trackMetadata{"r-near", "f-near", "Other"}, signals{acoustic: []byte{11, 10}}),
	}

	// When
	small := Retrieve(Request{Index: NewIndex(0, tracks), Seed: seed, Current: current, PageSize: 1})
	slices.Reverse(tracks)
	large := Retrieve(Request{Index: NewIndex(0, tracks), Seed: seed, Current: current, PageSize: 100})

	// Then
	want := []catalog.TrackID{"acoustic-near", "metadata", "acoustic-far", "same-artist"}
	assertCandidateIDs(t, small.Candidates, want)
	assertCandidateIDs(t, large.Candidates, want)
}

func TestCandidateRetrievalDegradesThroughAvailableSignals(t *testing.T) {
	tests := []struct {
		name      string
		seed      Track
		candidate Track
		wantTier  Tier
	}{
		{
			name:      "metadata remains when acoustic is unavailable",
			seed:      track("seed", trackMetadata{"seed", "seed-fp", "Seed Artist"}, signals{styles: []string{"ambient"}}),
			candidate: track("metadata", trackMetadata{"metadata", "metadata-fp", "Other"}, signals{styles: []string{" AMBIENT "}}),
			wantTier:  TierMetadata,
		},
		{
			name:      "acoustic remains when metadata is unavailable",
			seed:      track("seed", trackMetadata{"seed", "seed-fp", "Seed Artist"}, signals{acoustic: []byte{5, 8}}),
			candidate: track("acoustic", trackMetadata{"acoustic", "acoustic-fp", "Other"}, signals{acoustic: []byte{6, 8}}),
			wantTier:  TierAcoustic,
		},
		{
			name:      "same artist remains when metadata and acoustic are unavailable",
			seed:      track("seed", trackMetadata{"seed", "seed-fp", "Seed Artist"}, signals{}),
			candidate: track("same-artist", trackMetadata{"same-artist", "same-fp", " seed   artist "}, signals{}),
			wantTier:  TierSameArtist,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			request := Request{Index: NewIndex(0, []Track{tt.candidate}), Seed: tt.seed, Current: tt.seed, PageSize: 10}

			// When
			got := Retrieve(request)

			// Then
			if len(got.Candidates) != 1 || got.Candidates[0].Tier != tt.wantTier {
				t.Fatalf("candidates = %+v, want one %s candidate", got.Candidates, tt.wantTier)
			}
		})
	}
}

func TestCandidateRetrievalUsesRecordingIdentityFallbacks(t *testing.T) {
	// Given
	seed := track("seed", trackMetadata{"seed-recording", "seed-fp", "Seed"}, signals{genres: []string{"rock"}})
	tracks := []Track{
		track("embedded-seen", trackMetadata{"shared-recording", "one-fp", "Other"}, signals{genres: []string{"rock"}}),
		track("embedded-duplicate", trackMetadata{"shared-recording", "two-fp", "Other"}, signals{genres: []string{"rock"}}),
		track("fingerprint-seen", trackMetadata{"", "shared-fingerprint", "Other"}, signals{genres: []string{"rock"}}),
		track("track-id-seen", trackMetadata{"", "", "Other"}, signals{genres: []string{"rock"}}),
		track("eligible", trackMetadata{"eligible-recording", "eligible-fp", "Other"}, signals{genres: []string{"rock"}}),
	}
	seen := map[RecordingKey]struct{}{
		"recording:shared-recording":     {},
		"fingerprint:shared-fingerprint": {},
		"track:track-id-seen":            {},
	}

	// When
	got := Retrieve(Request{Index: NewIndex(0, tracks), Seed: seed, Current: seed, Seen: seen, PageSize: 2})

	// Then
	assertCandidateIDs(t, got.Candidates, []catalog.TrackID{"eligible"})
}

func TestAllRelatedSeenReturnsFiniteEmpty(t *testing.T) {
	// Given
	seed := track("seed", trackMetadata{"seed-recording", "seed-fp", "Artist"}, signals{genres: []string{"rock"}})
	tracks := []Track{
		track("one", trackMetadata{"one-recording", "one-fp", "Other"}, signals{genres: []string{"rock"}}),
		track("two", trackMetadata{"two-recording", "two-fp", "Other"}, signals{genres: []string{"rock"}}),
		track("three", trackMetadata{"three-recording", "three-fp", "Artist"}, signals{}),
	}
	seen := map[RecordingKey]struct{}{
		"recording:one-recording":   {},
		"recording:two-recording":   {},
		"recording:three-recording": {},
	}

	// When
	got := Retrieve(Request{Index: NewIndex(0, tracks), Seed: seed, Current: seed, Seen: seen, PageSize: 1})

	// Then
	if len(got.Candidates) != 0 {
		t.Fatalf("candidates = %+v, want finite empty result", got.Candidates)
	}
	if got.PagesRead != 3 {
		t.Fatalf("pages read = %d, want exactly 3", got.PagesRead)
	}
}

func TestCandidateRetrievalExcludesUnavailableCurrentAndPolicyBlockedTracks(t *testing.T) {
	// Given
	seed := track("seed", trackMetadata{"seed-recording", "seed-fp", "Artist"}, signals{genres: []string{"rock"}})
	current := track("current", trackMetadata{"current-recording", "current-fp", "Artist"}, signals{genres: []string{"rock"}})
	unavailable := track("unavailable", trackMetadata{"unavailable-recording", "unavailable-fp", "Other"}, signals{genres: []string{"rock"}})
	unavailable.Catalog.Available = false
	tracks := []Track{
		current,
		track("current-copy", trackMetadata{"current-recording", "other-fp", "Other"}, signals{genres: []string{"rock"}}),
		unavailable,
		track("policy", trackMetadata{"policy-recording", "policy-fp", "Other"}, signals{genres: []string{"rock"}}),
		track("blacklist", trackMetadata{"blacklist-recording", "blacklist-fp", "Other"}, signals{genres: []string{"rock"}}),
		track("eligible", trackMetadata{"eligible-recording", "eligible-fp", "Other"}, signals{genres: []string{"rock"}}),
	}

	// When
	got := Retrieve(Request{
		Index: NewIndex(0, tracks), Seed: seed, Current: current, PageSize: 2,
		PolicyExcluded: map[catalog.TrackID]struct{}{"policy": {}},
		StartBlacklist: map[catalog.TrackID]struct{}{"blacklist": {}},
	})

	// Then
	assertCandidateIDs(t, got.Candidates, []catalog.TrackID{"eligible"})
}

func TestCandidateRetrievalStopsWithoutRandomFallback(t *testing.T) {
	// Given
	seed := track("seed", trackMetadata{"seed-recording", "seed-fp", "Various Artists"}, signals{})
	tracks := []Track{
		seed,
		track("compilation", trackMetadata{"compilation-recording", "compilation-fp", " various  artists "}, signals{}),
		track("unrelated", trackMetadata{"unrelated-recording", "unrelated-fp", "Other"}, signals{}),
	}

	// When
	got := Retrieve(Request{Index: NewIndex(0, tracks), Seed: seed, Current: seed, PageSize: 1})

	// Then
	if len(got.Candidates) != 0 {
		t.Fatalf("candidates = %+v, want no synthetic-artist or random fallback", got.Candidates)
	}
	if got.PagesRead != len(tracks) {
		t.Fatalf("pages read = %d, want true exhaustion at %d", got.PagesRead, len(tracks))
	}
}

type signals struct {
	genres, styles, moods, localTags []string
	acoustic                         []byte
}

func TestCandidateRetrievalExcludesSeedRecordingWithoutSeenHint(t *testing.T) {
	seed := track("seed", trackMetadata{"seed-recording", "seed-file", "Seed Artist"}, signals{
		genres: []string{"ambient"},
	})
	current := track("current", trackMetadata{"current-recording", "current-file", "Current Artist"}, signals{
		genres: []string{"ambient"},
	})
	duplicateSeed := track("duplicate-seed", trackMetadata{"seed-recording", "duplicate-file", "Other"}, signals{
		genres: []string{"ambient"},
	})
	eligible := track("eligible", trackMetadata{"eligible-recording", "eligible-file", "Other"}, signals{
		genres: []string{"ambient"},
	})
	got := Retrieve(Request{Index: NewIndex(0, []Track{duplicateSeed, eligible}), Seed: seed, Current: current, PageSize: 1})
	assertCandidateIDs(t, got.Candidates, []catalog.TrackID{"eligible"})
}

func TestCandidateRetrievalIdentityFallsBackToAudioFingerprint(t *testing.T) {
	current := track("current", trackMetadata{"", "current-tag-fingerprint", "Artist"}, signals{
		genres: []string{"ambient"},
	})
	current.Catalog.AudioFingerprint = "same-audio"
	sameRecording := track("retagged-copy", trackMetadata{"", "different-tag-fingerprint", "Other"}, signals{
		genres: []string{"ambient"},
	})
	sameRecording.Catalog.AudioFingerprint = "same-audio"
	got := Retrieve(Request{Index: NewIndex(0, []Track{sameRecording}), Seed: current, Current: current, PageSize: 1})
	if len(got.Candidates) != 0 {
		t.Fatalf("same audio identity returned %+v", got.Candidates)
	}
}

func TestCandidateRetrievalNormalizesUnicodeTags(t *testing.T) {
	seed := track("seed", trackMetadata{"seed-recording", "seed-file", "Artist"}, signals{
		genres: []string{"Caf\u00e9 Pop"},
	})
	candidate := track("candidate", trackMetadata{"candidate-recording", "candidate-file", "Other"}, signals{
		genres: []string{"Cafe\u0301 Pop"},
	})
	got := Retrieve(Request{Index: NewIndex(0, []Track{candidate}), Seed: seed, Current: seed, PageSize: 1})
	assertCandidateIDs(t, got.Candidates, []catalog.TrackID{"candidate"})
}

func TestCandidateRetrievalIsInsertionOrderInvariant(t *testing.T) {
	seed := track("seed", trackMetadata{"seed-recording", "seed-file", "Artist"}, signals{
		genres: []string{"ambient"}, acoustic: []byte{10, 10},
	})
	tracks := []Track{
		track("metadata-b", trackMetadata{"b", "b-file", "Other"}, signals{genres: []string{"ambient"}}),
		track("acoustic", trackMetadata{"a", "a-file", "Other"}, signals{acoustic: []byte{11, 11}}),
		track("metadata-a", trackMetadata{"c", "c-file", "Other"}, signals{genres: []string{"ambient"}}),
	}
	forward := Retrieve(Request{Index: NewIndex(0, tracks), Seed: seed, Current: seed, PageSize: 1})
	slices.Reverse(tracks)
	reverse := Retrieve(Request{Index: NewIndex(0, tracks), Seed: seed, Current: seed, PageSize: 100})
	assertCandidateIDs(t, forward.Candidates, []catalog.TrackID{"acoustic", "metadata-a", "metadata-b"})
	assertCandidateIDs(t, reverse.Candidates, []catalog.TrackID{"acoustic", "metadata-a", "metadata-b"})
}

func TestIndexSnapshotUsesPersistedTagsAndCompatibleAnalysis(t *testing.T) {
	current := analysis.CurrentProvenance()
	snapshot := catalog.EmptySnapshot()
	snapshot.Revision = 42
	snapshot.Tracks["compatible"] = catalog.Track{
		TrackID: "compatible", RecordingID: "compatible-recording", Available: true,
		Metadata:       catalog.Metadata{Genres: []string{"Ambient"}, Styles: []string{"Ethereal"}},
		AnalysisStatus: catalog.AnalysisComplete, AnalysisProvenance: current,
		AnalysisVector: string([]byte{1, 2, 3}),
	}
	stale := snapshot.Tracks["compatible"]
	stale.TrackID, stale.RecordingID = "stale", "stale-recording"
	stale.AnalysisProvenance.AnalyzerVersion = "old"
	snapshot.Tracks["stale"] = stale
	index := IndexSnapshot(snapshot)
	if index.Revision != 42 || len(index.Tracks) != 2 {
		t.Fatalf("index = %+v", index)
	}
	if !slices.Equal(index.Tracks[0].Signals.Genres, []string{"Ambient"}) ||
		!slices.Equal(index.Tracks[0].Signals.AcousticVector, []byte{1, 2, 3}) {
		t.Fatalf("compatible signals = %+v", index.Tracks[0].Signals)
	}
	if len(index.Tracks[1].Signals.AcousticVector) != 0 {
		t.Fatalf("stale vector was exposed: %v", index.Tracks[1].Signals.AcousticVector)
	}
}

type trackMetadata struct {
	recordingID, fingerprint, artist string
}

func track(id string, metadata trackMetadata, value signals) Track {
	return Track{
		Catalog: catalog.Track{
			TrackID:     catalog.TrackID(id),
			RecordingID: catalog.RecordingID(metadata.recordingID),
			Fingerprint: metadata.fingerprint,
			Format:      catalog.FormatFLAC,
			Metadata:    catalog.Metadata{Artist: metadata.artist},
			Available:   true,
		},
		Signals: Signals{
			Genres: value.genres, Styles: value.styles, Moods: value.moods,
			LocalTags: value.localTags, AcousticVector: value.acoustic,
		},
	}
}

func assertCandidateIDs(t *testing.T, got []Candidate, want []catalog.TrackID) {
	t.Helper()
	ids := make([]catalog.TrackID, len(got))
	for index := range got {
		ids[index] = got[index].Track.Catalog.TrackID
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("candidate IDs = %v, want %v", ids, want)
	}
}
