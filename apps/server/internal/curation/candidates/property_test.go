package candidates

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
)

func TestCandidateRetrievalMatchesAcousticBruteForce(t *testing.T) {
	random := rand.New(rand.NewSource(20260823))
	for iteration := range 200 {
		seedVector := []byte{byte(random.Intn(256)), byte(random.Intn(256)), byte(random.Intn(256))}
		seed := track("seed", trackMetadata{"seed-r", "seed-f", "seed"}, signals{acoustic: seedVector})
		values := make([]Track, 80)
		type expected struct {
			id       catalog.TrackID
			distance uint64
		}
		want := make([]expected, len(values))
		for index := range values {
			id := catalog.TrackID(fmt.Sprintf("track-%03d", index))
			vector := []byte{byte(random.Intn(256)), byte(random.Intn(256)), byte(random.Intn(256))}
			values[index] = track(string(id), trackMetadata{string(id) + "-r", string(id) + "-f", "other"}, signals{acoustic: vector})
			var sum uint64
			for dimension, value := range vector {
				difference := int64(value) - int64(seedVector[dimension])
				sum += uint64(difference * difference)
			}
			want[index] = expected{id, sum * CompositeDistanceLimit / (uint64(len(vector)) * 255 * 255)}
		}
		sort.Slice(want, func(left, right int) bool {
			if want[left].distance != want[right].distance {
				return want[left].distance < want[right].distance
			}
			return want[left].id < want[right].id
		})
		random.Shuffle(len(values), func(left, right int) { values[left], values[right] = values[right], values[left] })
		got := Retrieve(Request{Index: NewIndex(9, values), CatalogRevision: 9, Seed: seed, Current: seed, PageSize: 1 + random.Intn(100)})
		if len(got.Candidates) != len(want) {
			t.Fatalf("iteration %d: candidates = %d, want %d", iteration, len(got.Candidates), len(want))
		}
		for index := range want {
			if got.Candidates[index].Track.Catalog.TrackID != want[index].id || got.Candidates[index].CompositeDistance != want[index].distance {
				t.Fatalf("iteration %d index %d: got %s/%d want %s/%d", iteration, index, got.Candidates[index].Track.Catalog.TrackID, got.Candidates[index].CompositeDistance, want[index].id, want[index].distance)
			}
		}
	}
}
