package ranking

import (
	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/curation/candidates"
)

const (
	AlgorithmVersion      = "policy-v1"
	DefaultArtistGap      = 4
	DefaultAlbumGap       = 10
	MaxPasses             = 4
	MetadataGenreWeight   = 50
	MetadataStyleWeight   = 30
	MetadataMoodTagWeight = 20
	AcousticWeight        = 55
	MetadataWeight        = 45
	SeedWeight            = 60
	CurrentWeight         = 40
	ScoreBandWidth        = 250
)

type BasisPoints int

type AcousticSimilarity struct {
	Available bool
	Score     BasisPoints
}

type RankedCandidate struct {
	Candidate       candidates.Candidate
	SeedAcoustic    AcousticSimilarity
	CurrentAcoustic AcousticSimilarity
}

type Policy struct {
	ArtistGap int
	AlbumGap  int
}

func DefaultPolicy() Policy {
	return Policy{ArtistGap: DefaultArtistGap, AlbumGap: DefaultAlbumGap}
}

type StartedTrack struct {
	Track     candidates.Track
	Generated bool
}

type Request struct {
	Candidates       []RankedCandidate
	Seed             candidates.Track
	Current          candidates.Track
	SessionSeed      string
	DecisionSequence uint64
	Policy           Policy
	History          []StartedTrack
	Seen             map[candidates.RecordingKey]struct{}
}

type StopReason string

const StopSimilarExhausted StopReason = "STOP_SIMILAR_EXHAUSTED"

type ScoringPolicyTrace struct {
	MetadataGenreWeight   int `json:"metadata_genre_weight"`
	MetadataStyleWeight   int `json:"metadata_style_weight"`
	MetadataMoodTagWeight int `json:"metadata_mood_tag_weight"`
	AcousticWeight        int `json:"acoustic_weight"`
	MetadataWeight        int `json:"metadata_weight"`
	SeedWeight            int `json:"seed_weight"`
	CurrentWeight         int `json:"current_weight"`
	ScoreBandWidth        int `json:"score_band_width"`
}

type Explanation struct {
	TrackID              catalog.TrackID    `json:"track_id"`
	RecordingKey         string             `json:"recording_key"`
	AlgorithmVersion     string             `json:"algorithm_version"`
	RelaxationPass       int                `json:"relaxation_pass"`
	EffectiveArtistGap   int                `json:"effective_artist_gap"`
	EffectiveAlbumGap    int                `json:"effective_album_gap"`
	Tier                 candidates.Tier    `json:"tier"`
	SeedMetadataScore    BasisPoints        `json:"seed_metadata_score"`
	CurrentMetadataScore BasisPoints        `json:"current_metadata_score"`
	SeedSimilarity       BasisPoints        `json:"seed_similarity"`
	CurrentSimilarity    BasisPoints        `json:"current_similarity"`
	RelatedScore         BasisPoints        `json:"related_score"`
	ScoreBand            int                `json:"score_band"`
	GeneratedArtistCount int                `json:"generated_artist_count"`
	GeneratedAlbumCount  int                `json:"generated_album_count"`
	TiePrefix            string             `json:"tie_prefix"`
	ScoringPolicy        ScoringPolicyTrace `json:"scoring_policy"`
}

type Decision struct {
	Candidate   candidates.Candidate `json:"-"`
	Explanation Explanation          `json:"explanation"`
}

type Result struct {
	Decision       *Decision
	StopReason     StopReason
	PassesExamined int
}
