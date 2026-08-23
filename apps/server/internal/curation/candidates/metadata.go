package candidates

import (
	"github.com/jakestreamer/jstreamer-server/internal/analysis"
	"github.com/jakestreamer/jstreamer-server/internal/catalog"
)

func currentProvenance() analysis.Provenance { return analysis.CurrentProvenance() }

func sharesMetadata(left, right Signals) bool {
	return overlaps(left.Genres, right.Genres) || overlaps(left.Styles, right.Styles) ||
		overlaps(left.Moods, right.Moods) || overlaps(left.LocalTags, right.LocalTags)
}

func overlaps(left, right []string) bool {
	values := normalizedSet(left)
	for _, value := range right {
		if _, exists := values[normalize(value)]; exists {
			return true
		}
	}
	return false
}

func normalizedSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		if normalized := normalize(value); normalized != "" {
			result[normalized] = struct{}{}
		}
	}
	return result
}

func jaccard(left, right map[string]struct{}) uint64 {
	intersection, union := 0, len(left)
	for value := range right {
		if _, exists := left[value]; exists {
			intersection++
		} else {
			union++
		}
	}
	return CompositeDistanceLimit * uint64(intersection) / uint64(union)
}

func bonusesFor(candidate, anchor catalog.Track, candidateSignals, anchorSignals Signals) bonuses {
	value := bonuses{}
	if overlaps(candidateSignals.Genres, anchorSignals.Genres) {
		value.genre = GenreBonusLimit
	}
	if sharesPrimaryArtist(candidate.Metadata.Artist, anchor.Metadata.Artist) {
		value.artist = ArtistBonusLimit
	}
	if candidate.AlbumID != "" && candidate.AlbumID == anchor.AlbumID {
		value.album = AlbumBonusLimit
	}
	return value
}
