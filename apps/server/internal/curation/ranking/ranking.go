package ranking

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/curation/candidates"
)

type scoredCandidate struct {
	input                                      RankedCandidate
	seedMetadata, currentMetadata              BasisPoints
	seedSimilarity, currentSimilarity, related BasisPoints
	generatedArtistCount, generatedAlbumCount  int
	tie                                        uint64
}

type cooldown struct{ artist, album int }

func Select(request Request) Result {
	eligible := make([]scoredCandidate, 0, len(request.Candidates))
	seedProfile := newMetadataProfile(request.Seed.Signals)
	currentProfile := newMetadataProfile(request.Current.Signals)
	for _, input := range request.Candidates {
		if _, seen := request.Seen[recordingKey(input.Candidate.Track.Catalog)]; seen {
			continue
		}
		candidateProfile := newMetadataProfile(input.Candidate.Track.Signals)
		seedMetadata, seedSimilarity := anchorSimilarity(seedProfile, candidateProfile, input.SeedAcoustic)
		currentMetadata, currentSimilarity := anchorSimilarity(currentProfile, candidateProfile, input.CurrentAcoustic)
		artistCount, albumCount := generatedCounts(request.History, input.Candidate.Track.Catalog)
		eligible = append(eligible, scoredCandidate{
			input: input, seedMetadata: seedMetadata, currentMetadata: currentMetadata,
			seedSimilarity: seedSimilarity, currentSimilarity: currentSimilarity,
			related:              RelatedScore(seedSimilarity, currentSimilarity),
			generatedArtistCount: artistCount, generatedAlbumCount: albumCount,
			tie: TieValue(request.SessionSeed, request.DecisionSequence, string(input.Candidate.Track.Catalog.TrackID)),
		})
	}
	passes := [...]cooldown{{request.Policy.ArtistGap, request.Policy.AlbumGap}, {request.Policy.ArtistGap, 1}, {1, 1}, {0, 0}}
	for index, pass := range passes {
		var selected *scoredCandidate
		for candidateIndex := range eligible {
			candidate := &eligible[candidateIndex]
			if !outsideCooldown(candidate.input.Candidate.Track.Catalog, request.History, pass) {
				continue
			}
			if selected == nil || compareScored(*candidate, *selected) < 0 {
				selected = candidate
			}
		}
		if selected != nil {
			return Result{Decision: &Decision{Candidate: selected.input.Candidate, Explanation: explain(*selected, index+1, pass)}, PassesExamined: index + 1}
		}
	}
	return Result{StopReason: StopSimilarExhausted, PassesExamined: MaxPasses}
}

func compareScored(left, right scoredCandidate) int {
	if value := cmp.Compare(tierOrder(left.input.Candidate.Tier), tierOrder(right.input.Candidate.Tier)); value != 0 {
		return value
	}
	if value := cmp.Compare(int(right.related)/ScoreBandWidth, int(left.related)/ScoreBandWidth); value != 0 {
		return value
	}
	if value := cmp.Compare(left.generatedArtistCount, right.generatedArtistCount); value != 0 {
		return value
	}
	if value := cmp.Compare(left.generatedAlbumCount, right.generatedAlbumCount); value != 0 {
		return value
	}
	if value := cmp.Compare(right.related, left.related); value != 0 {
		return value
	}
	if value := cmp.Compare(left.tie, right.tie); value != 0 {
		return value
	}
	return strings.Compare(string(left.input.Candidate.Track.Catalog.TrackID), string(right.input.Candidate.Track.Catalog.TrackID))
}

func outsideCooldown(track catalog.Track, history []StartedTrack, gap cooldown) bool {
	artist := normalizedArtist(track.Metadata.Artist)
	for offset := 1; offset <= gap.artist && offset <= len(history); offset++ {
		if artist != "" && artist == normalizedArtist(history[len(history)-offset].Track.Catalog.Metadata.Artist) {
			return false
		}
	}
	if track.AlbumID == "" {
		return true
	}
	for offset := 1; offset <= gap.album && offset <= len(history); offset++ {
		if track.AlbumID == history[len(history)-offset].Track.Catalog.AlbumID {
			return false
		}
	}
	return true
}

func generatedCounts(history []StartedTrack, track catalog.Track) (int, int) {
	artist := normalizedArtist(track.Metadata.Artist)
	artistCount, albumCount := 0, 0
	for _, started := range history {
		if !started.Generated {
			continue
		}
		if artist != "" && artist == normalizedArtist(started.Track.Catalog.Metadata.Artist) {
			artistCount++
		}
		if track.AlbumID != "" && track.AlbumID == started.Track.Catalog.AlbumID {
			albumCount++
		}
	}
	return artistCount, albumCount
}

func explain(candidate scoredCandidate, passNumber int, pass cooldown) Explanation {
	track := candidate.input.Candidate.Track.Catalog
	return Explanation{
		TrackID: track.TrackID, RecordingKey: string(recordingKey(track)), AlgorithmVersion: AlgorithmVersion,
		RelaxationPass: passNumber, EffectiveArtistGap: pass.artist, EffectiveAlbumGap: pass.album,
		Tier: candidate.input.Candidate.Tier, SeedMetadataScore: candidate.seedMetadata,
		CurrentMetadataScore: candidate.currentMetadata, SeedSimilarity: candidate.seedSimilarity,
		CurrentSimilarity: candidate.currentSimilarity, RelatedScore: candidate.related,
		ScoreBand: int(candidate.related) / ScoreBandWidth, GeneratedArtistCount: candidate.generatedArtistCount,
		GeneratedAlbumCount: candidate.generatedAlbumCount, TiePrefix: fmt.Sprintf("0x%016x", candidate.tie),
		ScoringPolicy: ScoringPolicyTrace{
			MetadataGenreWeight: MetadataGenreWeight, MetadataStyleWeight: MetadataStyleWeight,
			MetadataMoodTagWeight: MetadataMoodTagWeight, AcousticWeight: AcousticWeight,
			MetadataWeight: MetadataWeight, SeedWeight: SeedWeight,
			CurrentWeight: CurrentWeight, ScoreBandWidth: ScoreBandWidth,
		},
	}
}

func recordingKey(track catalog.Track) candidates.RecordingKey {
	if track.RecordingID != "" {
		return candidates.RecordingKey("recording:" + string(track.RecordingID))
	}
	if track.AudioFingerprint != "" {
		return candidates.RecordingKey("fingerprint:" + track.AudioFingerprint)
	}
	if track.Fingerprint != "" {
		return candidates.RecordingKey("fingerprint:" + track.Fingerprint)
	}
	return candidates.RecordingKey("track:" + string(track.TrackID))
}

func normalizedArtist(value string) string {
	value = strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
	if value == "various artists" {
		return ""
	}
	return value
}

func tierOrder(tier candidates.Tier) int {
	switch tier {
	case candidates.TierMetadata:
		return 0
	case candidates.TierAcoustic:
		return 1
	case candidates.TierSameArtist:
		return 2
	default:
		return 3
	}
}
