package playback_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/curation/candidates"
	"github.com/jastreamer/jastreamer-server/internal/curation/ranking"
	"github.com/jastreamer/jastreamer-server/internal/decision"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func TestSimilarSessionSeenPersistsEveryRecordingKeyFallbackAcrossRestart(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		tracks func() []candidates.Track
	}{
		{"recording id", "recording:", recordingIdentityTracks},
		{"audio fingerprint", "fingerprint:", audioFingerprintIdentityTracks},
		{"file fingerprint", "fingerprint:", fileFingerprintIdentityTracks},
		{"track id", "track:", trackIdentityTracks},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			ctx := context.Background()
			config := todo12Config(t)
			store := openTodo12Store(t, config)
			playing := startTodo12Session(t, store, "zone-seen")
			seed := todo12Track("seed")
			tracks := append([]candidates.Track{seed}, test.tracks()...)
			snapshot := decision.Snapshot{Similar: decision.SimilarSnapshot{
				Index: candidates.NewIndex(1, tracks), Seed: seed, Current: seed,
				PageSize: 1, RankingPolicy: ranking.DefaultPolicy(), SessionSeed: "seen-session",
			}}
			first, err := store.CommitNext(ctx, playback.NextRequest{
				ZoneID:   "zone-seen",
				Boundary: playback.Boundary{ID: "seen-1", PreviousPlayID: playing.CurrentPlay},
				Snapshot: snapshot,
			})
			if err != nil {
				t.Fatalf("first similar decision: %v", err)
			}
			if _, err := store.ConfirmStart(ctx, "zone-seen", first.PlayID); err != nil {
				t.Fatalf("confirm first similar decision: %v", err)
			}
			if err := store.Close(); err != nil {
				t.Fatalf("close after first decision: %v", err)
			}
			store = openTodo12Store(t, config)

			// When
			second, err := store.CommitNext(ctx, playback.NextRequest{
				ZoneID:   "zone-seen",
				Boundary: playback.Boundary{ID: "seen-2", PreviousPlayID: first.PlayID},
				Snapshot: snapshot,
			})
			if err != nil {
				t.Fatalf("second similar decision: %v", err)
			}
			if _, err := store.ConfirmStart(ctx, "zone-seen", second.PlayID); err != nil {
				t.Fatalf("confirm second similar decision: %v", err)
			}
			third, err := store.CommitNext(ctx, playback.NextRequest{
				ZoneID:   "zone-seen",
				Boundary: playback.Boundary{ID: "seen-3", PreviousPlayID: second.PlayID},
				Snapshot: snapshot,
			})
			// Then
			if err != nil {
				t.Fatalf("exhausted similar decision: %v", err)
			}
			if first.RecordingKey == "" || second.RecordingKey == "" || first.RecordingKey == second.RecordingKey ||
				!strings.HasPrefix(first.RecordingKey, test.prefix) || !strings.HasPrefix(second.RecordingKey, test.prefix) {
				t.Fatalf("recording keys first=%q second=%q", first.RecordingKey, second.RecordingKey)
			}
			if third.Kind != playback.DecisionStop || third.Reason != string(decision.ReasonStopSimilarExhausted) {
				t.Fatalf("seen set was cleared: third=%+v", third)
			}
		})
	}
}

func recordingIdentityTracks() []candidates.Track {
	left, duplicate, other := todo12Track("a"), todo12Track("b"), todo12Track("c")
	left.Catalog.RecordingID, duplicate.Catalog.RecordingID = "shared", "shared"
	other.Catalog.RecordingID = "other"
	return []candidates.Track{left, duplicate, other}
}

func audioFingerprintIdentityTracks() []candidates.Track {
	left, duplicate, other := tracksWithoutEmbeddedRecording()
	left.Catalog.AudioFingerprint, duplicate.Catalog.AudioFingerprint = "shared-audio", "shared-audio"
	other.Catalog.AudioFingerprint = "other-audio"
	return []candidates.Track{left, duplicate, other}
}

func fileFingerprintIdentityTracks() []candidates.Track {
	left, duplicate, other := tracksWithoutEmbeddedRecording()
	left.Catalog.Fingerprint, duplicate.Catalog.Fingerprint = "shared-file", "shared-file"
	other.Catalog.Fingerprint = "other-file"
	return []candidates.Track{left, duplicate, other}
}

func trackIdentityTracks() []candidates.Track {
	left, _, other := tracksWithoutEmbeddedRecording()
	return []candidates.Track{left, other}
}

func tracksWithoutEmbeddedRecording() (candidates.Track, candidates.Track, candidates.Track) {
	left, duplicate, other := todo12Track("a"), todo12Track("b"), todo12Track("c")
	for _, track := range []*catalog.Track{&left.Catalog, &duplicate.Catalog, &other.Catalog} {
		track.RecordingID = ""
		track.AudioFingerprint = ""
		track.Fingerprint = ""
	}
	return left, duplicate, other
}
