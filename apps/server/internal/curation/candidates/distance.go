package candidates

import "github.com/jastreamer/jastreamer-server/internal/catalog"

type bonuses struct{ genre, artist, album uint64 }

func scoreCandidate(track, seed, current Track) (Candidate, bool) {
	distance, tier, scoreBonuses, related := scoreAgainstAnchors(track, seed, current)
	if !related {
		return Candidate{}, false
	}
	acousticDistance, _ := nearestAcousticDistance(track, seed, current)
	return Candidate{
		Track: track, Tier: tier, AcousticDistance: acousticDistance,
		CompositeDistance: distance, GenreBonus: scoreBonuses.genre,
		ArtistBonus: scoreBonuses.artist, AlbumBonus: scoreBonuses.album,
	}, true
}

func scoreAgainstAnchors(candidate Track, anchors ...Track) (uint64, Tier, bonuses, bool) {
	bestDistance := CompositeDistanceLimit + 1
	var bestBonuses bonuses
	tier := TierSameArtist
	related := false
	for _, anchor := range anchors {
		metadataRelated := sharesMetadata(candidate.Signals, anchor.Signals)
		_, acousticAvailable := acousticDistance(candidate, anchor)
		artistRelated := sharesPrimaryArtist(candidate.Catalog.Metadata.Artist, anchor.Catalog.Metadata.Artist)
		if !metadataRelated && !acousticAvailable && !artistRelated {
			continue
		}
		related = true
		if metadataRelated {
			tier = TierMetadata
		} else if acousticAvailable && tier != TierMetadata {
			tier = TierAcoustic
		}
		base, available := featureDistance(candidate, anchor)
		if !available {
			base = CompositeDistanceLimit
		}
		value := bonusesFor(candidate.Catalog, anchor.Catalog, candidate.Signals, anchor.Signals)
		bonus := min(GenreBonusLimit+ArtistBonusLimit+AlbumBonusLimit, value.genre+value.artist+value.album)
		distance := base - min(base, bonus)
		if distance < bestDistance {
			bestDistance, bestBonuses = distance, value
		}
	}
	return bestDistance, tier, bestBonuses, related
}

func featureDistance(candidate, anchor Track) (uint64, bool) {
	metadata, hasMetadata := metadataDistance(candidate.Signals, anchor.Signals)
	acoustic, hasAcoustic := acousticDistance(candidate, anchor)
	weighted, weight := uint64(0), uint64(0)
	if hasAcoustic {
		weighted, weight = weighted+55*acoustic, weight+55
	}
	if hasMetadata {
		weighted, weight = weighted+45*metadata, weight+45
	}
	if weight == 0 {
		return 0, false
	}
	return weighted / weight, true
}

func metadataDistance(left, right Signals) (uint64, bool) {
	weights := [...]uint64{40, 25, 20, 15}
	leftValues := [...][]string{left.Genres, left.Styles, left.Moods, left.LocalTags}
	rightValues := [...][]string{right.Genres, right.Styles, right.Moods, right.LocalTags}
	weighted, weight := uint64(0), uint64(0)
	for index := range weights {
		leftSet, rightSet := normalizedSet(leftValues[index]), normalizedSet(rightValues[index])
		if len(leftSet) == 0 || len(rightSet) == 0 {
			continue
		}
		weighted += weights[index] * (CompositeDistanceLimit - jaccard(leftSet, rightSet))
		weight += weights[index]
	}
	if weight == 0 {
		return 0, false
	}
	return weighted / weight, true
}

func acousticDistance(left, right Track) (uint64, bool) {
	leftVector, rightVector := compatibleVector(left), compatibleVector(right)
	if len(leftVector) == 0 || len(leftVector) != len(rightVector) {
		return 0, false
	}
	var sum uint64
	for index, value := range leftVector {
		difference := int64(value) - int64(rightVector[index])
		sum += uint64(difference * difference)
	}
	return sum * CompositeDistanceLimit / (uint64(len(leftVector)) * 255 * 255), true
}

func compatibleVector(track Track) []byte {
	if track.Catalog.AnalysisStatus == "" {
		return track.Signals.AcousticVector
	}
	if track.Catalog.AnalysisStatus != catalog.AnalysisComplete || track.Catalog.AnalysisProvenance != currentProvenance() {
		return nil
	}
	return track.Signals.AcousticVector
}

func nearestAcousticDistance(candidate Track, anchors ...Track) (uint64, bool) {
	var nearest uint64
	found := false
	for _, anchor := range anchors {
		left, right := compatibleVector(candidate), compatibleVector(anchor)
		if len(left) == 0 || len(left) != len(right) {
			continue
		}
		var distance uint64
		for index, value := range left {
			difference := int64(value) - int64(right[index])
			distance += uint64(difference * difference)
		}
		if !found || distance < nearest {
			nearest, found = distance, true
		}
	}
	return nearest, found
}
