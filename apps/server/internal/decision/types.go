package decision

import (
	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/curation/candidates"
	"github.com/jastreamer/jastreamer-server/internal/curation/ranking"
)

const MaxGeneratedAttempts = 3

type (
	BoundaryID   string
	QueueEntryID string
	Policy       string
	Reason       string
	Source       string
	Kind         string
)

const (
	PolicyStop    Policy = "stop"
	PolicyAlbum   Policy = "album"
	PolicySimilar Policy = "similar"
)

const (
	ReasonPlayExplicit         Reason = "PLAY_EXPLICIT"
	ReasonPlayAlbum            Reason = "PLAY_ALBUM"
	ReasonPlaySimilar          Reason = "PLAY_SIMILAR"
	ReasonBlockExplicit        Reason = "BLOCK_EXPLICIT"
	ReasonStopModeOff          Reason = "STOP_MODE_OFF"
	ReasonStopNoAlbum          Reason = "STOP_NO_ALBUM"
	ReasonStopAlbumComplete    Reason = "STOP_ALBUM_COMPLETE"
	ReasonStopSimilarExhausted Reason = "STOP_SIMILAR_EXHAUSTED"
	ReasonStopSimilarNoSignal  Reason = "STOP_SIMILAR_NO_SIGNAL"
	ReasonStopAutoFailureLimit Reason = "STOP_AUTO_FAILURE_LIMIT"
)

const (
	SourceExplicit Source = "explicit"
	SourceAlbum    Source = "album"
	SourceSimilar  Source = "similar"
)

const (
	KindPlay  Kind = "play"
	KindStop  Kind = "stop"
	KindBlock Kind = "block"
)

type Boundary struct {
	ID BoundaryID
}

type ExplicitEntry struct {
	ID        QueueEntryID
	TrackID   catalog.TrackID
	Available bool
}

type AlbumSnapshot struct {
	AlbumID catalog.AlbumID
	Anchor  catalog.OrderKey
	Started map[catalog.RecordingID]bool
}

type AcousticScores struct {
	Seed    ranking.AcousticSimilarity
	Current ranking.AcousticSimilarity
}

type SimilarSnapshot struct {
	Index            candidates.Index
	Seed             candidates.Track
	Current          candidates.Track
	Seen             map[candidates.RecordingKey]struct{}
	PolicyExcluded   map[catalog.TrackID]struct{}
	Acoustic         map[catalog.TrackID]AcousticScores
	PageSize         int
	SuppressSamePath bool
	RankingPolicy    ranking.Policy
	History          []ranking.StartedTrack
	SessionSeed      string
	DecisionSequence uint64
}

type Snapshot struct {
	Policy          Policy
	Explicit        []ExplicitEntry
	Catalog         catalog.Snapshot
	Album           AlbumSnapshot
	Similar         SimilarSnapshot
	FailedGenerated []catalog.TrackID
}

type Outcome interface {
	decisionOutcome()
	Kind() Kind
}

type Play struct {
	BoundaryID   BoundaryID
	TrackID      catalog.TrackID
	RecordingID  catalog.RecordingID
	RecordingKey candidates.RecordingKey
	AlbumID      catalog.AlbumID
	Order        catalog.OrderKey
	QueueEntryID QueueEntryID
	Source       Source
	Reason       Reason
	Explanation  ranking.Explanation
}

func (Play) decisionOutcome() {}
func (Play) Kind() Kind       { return KindPlay }

type Stop struct {
	BoundaryID BoundaryID
	Reason     Reason
}

func (Stop) decisionOutcome() {}
func (Stop) Kind() Kind       { return KindStop }

type Block struct {
	BoundaryID   BoundaryID
	TrackID      catalog.TrackID
	QueueEntryID QueueEntryID
	Reason       Reason
}

func (Block) decisionOutcome() {}
func (Block) Kind() Kind       { return KindBlock }
