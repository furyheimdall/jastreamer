package ranking

import (
	"fmt"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/curation/candidates"
)

func BenchmarkSelect100000(b *testing.B) {
	signal := candidates.Signals{Genres: []string{"rock"}}
	values := make([]RankedCandidate, 100000)
	for index := range values {
		id := fmt.Sprintf("track-%06d", index)
		values[index] = rankedTrack(id, "recording-"+id, "artist", "album", signal, candidates.TierMetadata)
	}
	request := Request{Candidates: values, Seed: values[0].Candidate.Track, Current: values[0].Candidate.Track, SessionSeed: "benchmark", Policy: Policy{}}
	b.ResetTimer()
	for b.Loop() {
		Select(request)
	}
}
