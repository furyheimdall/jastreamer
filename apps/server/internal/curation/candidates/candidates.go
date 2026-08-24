package candidates

import "github.com/jastreamer/jastreamer-server/internal/catalog"

type RecordingKey string
type Tier string

const (
	TierMetadata   Tier = "metadata"
	TierAcoustic   Tier = "acoustic"
	TierSameArtist Tier = "same_artist"

	GenreBonusLimit        uint64 = 500
	ArtistBonusLimit       uint64 = 300
	AlbumBonusLimit        uint64 = 200
	CompositeDistanceLimit uint64 = 10000
)

type Signals struct {
	Genres         []string
	Styles         []string
	Moods          []string
	LocalTags      []string
	AcousticVector []byte
}

type Track struct {
	Catalog catalog.Track
	Signals Signals
}

type Index struct {
	Revision uint64
	Tracks   []Track
}

type Candidate struct {
	Track             Track
	Tier              Tier
	AcousticDistance  uint64
	CompositeDistance uint64
	GenreBonus        uint64
	ArtistBonus       uint64
	AlbumBonus        uint64
}

type Request struct {
	Index            Index
	CatalogRevision  uint64
	Seed             Track
	Current          Track
	Seen             map[RecordingKey]struct{}
	PolicyExcluded   map[catalog.TrackID]struct{}
	StartBlacklist   map[catalog.TrackID]struct{}
	SuppressSamePath bool
	PageSize         int
}

type Result struct {
	Candidates []Candidate
	PagesRead  int
	// TotalEligible counts available, supported, non-failed index tracks.
	TotalEligible int
	// FilteredEligible counts tracks remaining after identity, path, session, and policy filters.
	FilteredEligible int
	// ScoredEligible counts related tracks with an exact composite distance before recording deduplication.
	ScoredEligible int
	// RelatedEligible includes related tracks hidden by session and policy filters.
	RelatedEligible int
	RevisionMatched bool
}
