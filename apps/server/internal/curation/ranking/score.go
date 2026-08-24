package ranking

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/curation/candidates"
)

type metadataProfile struct {
	dimensions [3]map[string]struct{}
}

func MetadataScore(anchor, candidate candidates.Signals) BasisPoints {
	score, _ := newMetadataProfile(anchor).score(newMetadataProfile(candidate))
	return score
}

func RelatedScore(seed, current BasisPoints) BasisPoints {
	seed, current = clampScore(seed), clampScore(current)
	return BasisPoints((SeedWeight*int(seed) + CurrentWeight*int(current)) / 100)
}

func TieValue(sessionSeed string, decisionSequence uint64, trackID string) uint64 {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d|%s", AlgorithmVersion, sessionSeed, decisionSequence, trackID))
	return binary.BigEndian.Uint64(sum[:8])
}

func newMetadataProfile(value candidates.Signals) metadataProfile {
	return metadataProfile{dimensions: [3]map[string]struct{}{
		normalizedSet(value.Genres),
		normalizedSet(value.Styles),
		normalizedSet(append(value.Moods, value.LocalTags...)),
	}}
}

func (profile metadataProfile) score(candidate metadataProfile) (BasisPoints, bool) {
	weights := [...]int{MetadataGenreWeight, MetadataStyleWeight, MetadataMoodTagWeight}
	weightedScore := 0
	availableWeight := 0
	for index, anchorSet := range profile.dimensions {
		if len(anchorSet) == 0 || len(candidate.dimensions[index]) == 0 {
			continue
		}
		availableWeight += weights[index]
		weightedScore += weights[index] * int(jaccard(anchorSet, candidate.dimensions[index]))
	}
	if availableWeight == 0 {
		return 0, false
	}
	return BasisPoints(weightedScore / availableWeight), true
}

func anchorSimilarity(profile, candidate metadataProfile, acoustic AcousticSimilarity) (BasisPoints, BasisPoints) {
	metadata, metadataAvailable := profile.score(candidate)
	weightedScore := 0
	availableWeight := 0
	if acoustic.Available {
		weightedScore += AcousticWeight * int(clampScore(acoustic.Score))
		availableWeight += AcousticWeight
	}
	if metadataAvailable {
		weightedScore += MetadataWeight * int(metadata)
		availableWeight += MetadataWeight
	}
	if availableWeight == 0 {
		return metadata, 0
	}
	return metadata, BasisPoints(weightedScore / availableWeight)
}

func clampScore(value BasisPoints) BasisPoints {
	return BasisPoints(min(10000, max(0, int(value))))
}

func normalizedSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
		if normalized != "" {
			set[normalized] = struct{}{}
		}
	}
	return set
}

func jaccard(left, right map[string]struct{}) BasisPoints {
	intersection := 0
	union := len(left)
	for value := range right {
		if _, exists := left[value]; exists {
			intersection++
			continue
		}
		union++
	}
	return BasisPoints(10000 * intersection / union)
}
