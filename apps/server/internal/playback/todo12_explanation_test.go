package playback_test

import (
	"context"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/curation/ranking"
	"github.com/jastreamer/jastreamer-server/internal/decision"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func TestSimilarDecisionExplanationSurvivesRestartAndRecomputesExactly(t *testing.T) {
	// Given
	ctx := context.Background()
	config := todo12Config(t)
	store := openTodo12Store(t, config)
	playing := startTodo12Session(t, store, "zone-explanation")
	request := playback.NextRequest{
		ZoneID:   "zone-explanation",
		Boundary: playback.Boundary{ID: "automatic-explanation", PreviousPlayID: playing.CurrentPlay},
		Snapshot: todo12SimilarSnapshot("generated"),
	}
	committed, err := store.CommitNext(ctx, request)
	if err != nil {
		t.Fatalf("commit similar decision: %v", err)
	}
	if committed.Source != string(decision.SourceSimilar) || committed.Explanation.RecordingKey == "" {
		t.Fatalf("committed explanation = %+v", committed)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before restart: %v", err)
	}
	restarted := openTodo12Store(t, config)

	// When
	replayed, err := restarted.CommitNext(ctx, request)
	// Then
	if err != nil {
		t.Fatalf("replay similar decision: %v", err)
	}
	if replayed.ID != committed.ID || replayed.RecordingKey != committed.RecordingKey ||
		replayed.Explanation != committed.Explanation {
		t.Fatalf("committed=%+v replayed=%+v", committed, replayed)
	}
	explanation := replayed.Explanation
	trace := explanation.ScoringPolicy
	related := ranking.BasisPoints(
		(trace.SeedWeight*int(explanation.SeedSimilarity) +
			trace.CurrentWeight*int(explanation.CurrentSimilarity)) / 100,
	)
	if explanation.RelatedScore != related || explanation.ScoreBand != int(related)/trace.ScoreBandWidth ||
		replayed.RecordingKey != explanation.RecordingKey {
		t.Fatalf("persisted explanation does not recompute: %+v", explanation)
	}
}
