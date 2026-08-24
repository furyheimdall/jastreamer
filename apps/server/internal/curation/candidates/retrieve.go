package candidates

import (
	"slices"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func Retrieve(request Request) Result {
	result := Result{RevisionMatched: request.CatalogRevision == request.Index.Revision}
	if !result.RevisionMatched {
		return result
	}
	pageSize := max(1, request.PageSize)
	tracks := NewIndex(request.Index.Revision, request.Index.Tracks).Tracks
	currentKey, seedKey := recordingKey(request.Current.Catalog), recordingKey(request.Seed.Catalog)
	byRecording := make(map[RecordingKey]Candidate)
	for offset := 0; offset < len(tracks); offset += pageSize {
		result.PagesRead++
		end := min(offset+pageSize, len(tracks))
		for _, track := range tracks[offset:end] {
			if !staticEligible(track) {
				continue
			}
			result.TotalEligible++
			key := recordingKey(track.Catalog)
			if baseExcluded(track, request, key, currentKey, seedKey) {
				continue
			}
			candidate, related := scoreCandidate(track, request.Seed, request.Current)
			if related {
				result.RelatedEligible++
			}
			if dynamicallyExcluded(track.Catalog.TrackID, key, request) {
				continue
			}
			result.FilteredEligible++
			if !related {
				continue
			}
			result.ScoredEligible++
			previous, exists := byRecording[key]
			if !exists || compareCandidates(candidate, previous) < 0 {
				byRecording[key] = candidate
			}
		}
	}
	result.Candidates = make([]Candidate, 0, len(byRecording))
	for _, candidate := range byRecording {
		result.Candidates = append(result.Candidates, candidate)
	}
	slices.SortFunc(result.Candidates, compareCandidates)
	return result
}

func staticEligible(track Track) bool {
	if !track.Catalog.Available || !supportedFormat(track.Catalog.Format) {
		return false
	}
	switch track.Catalog.AnalysisStatus {
	case "", catalog.AnalysisQueued, catalog.AnalysisRunning:
		return true
	case catalog.AnalysisComplete:
		return track.Catalog.AnalysisProvenance == currentProvenance()
	case catalog.AnalysisFailed:
		return false
	default:
		return false
	}
}

func baseExcluded(track Track, request Request, key, currentKey, seedKey RecordingKey) bool {
	if track.Catalog.TrackID == request.Current.Catalog.TrackID || key == currentKey || key == seedKey {
		return true
	}
	if !request.SuppressSamePath {
		return false
	}
	path := normalizedPath(track.Catalog.RelativePath)
	return path != "" && (path == normalizedPath(request.Current.Catalog.RelativePath) || path == normalizedPath(request.Seed.Catalog.RelativePath))
}

func dynamicallyExcluded(id catalog.TrackID, key RecordingKey, request Request) bool {
	_, seen := request.Seen[key]
	_, policyExcluded := request.PolicyExcluded[id]
	_, blacklisted := request.StartBlacklist[id]
	return seen || policyExcluded || blacklisted
}

func compareCandidates(left, right Candidate) int {
	if left.CompositeDistance < right.CompositeDistance {
		return -1
	}
	if left.CompositeDistance > right.CompositeDistance {
		return 1
	}
	return strings.Compare(string(left.Track.Catalog.TrackID), string(right.Track.Catalog.TrackID))
}
