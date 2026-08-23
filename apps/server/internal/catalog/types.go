package catalog

import (
	"time"

	"github.com/jakestreamer/jstreamer-server/internal/analysis"
)

type (
	FileID      string
	TrackID     string
	RecordingID string
	AlbumID     string
)

type Format string

const (
	FormatFLAC      Format = "flac"
	FormatMP3       Format = "mp3"
	FormatOggVorbis Format = "ogg-vorbis"
	FormatOpus      Format = "opus"
	FormatPCMWAV    Format = "pcm-wav"
)

type AnalysisStatus string

const (
	AnalysisQueued   AnalysisStatus = "queued"
	AnalysisRunning  AnalysisStatus = "running"
	AnalysisComplete AnalysisStatus = "complete"
	AnalysisFailed   AnalysisStatus = "failed"
)

type Metadata struct {
	Title       string
	Album       string
	AlbumArtist string
	Artist      string
	RecordingID string
	ReleaseID   string
	Disc        int
	Track       int
}

type OrderedNumber struct {
	Known bool
	Value int
}

type OrderKey struct {
	Disc        OrderedNumber
	Track       OrderedNumber
	NaturalPath string
	TrackID     TrackID
}

type Track struct {
	FileID              FileID
	TrackID             TrackID
	RecordingID         RecordingID
	AlbumID             AlbumID
	RelativePath        string
	Format              Format
	Fingerprint         string
	AudioFingerprint    string
	FileVersion         FileVersion
	Metadata            Metadata
	Order               OrderKey
	Available           bool
	Generation          uint64
	AnalysisStatus      AnalysisStatus
	AnalysisFingerprint string
	AnalysisProvenance  analysis.Provenance
	AnalysisFailure     string
	AnalysisVector      string
}

type Snapshot struct {
	Generation uint64
	Revision   uint64
	Tracks     map[TrackID]Track
}

func EmptySnapshot() Snapshot { return Snapshot{Tracks: make(map[TrackID]Track)} }

type AnalysisJob struct {
	TrackID      TrackID
	Fingerprint  string
	RelativePath string
	Status       AnalysisStatus
}

type IssueCode string

const (
	IssueOutsideRoot       IssueCode = "outside_root"
	IssueMalformed         IssueCode = "malformed"
	IssuePermission        IssueCode = "permission_denied"
	IssueChangedDuringRead IssueCode = "changed_during_read"
	IssueRead              IssueCode = "read_error"
)

type Issue struct {
	Path string
	Code IssueCode
	Err  error
}
type ScanResult struct {
	Snapshot     Snapshot
	AnalysisJobs []AnalysisJob
	Issues       []Issue
	Complete     bool
}
type FileVersion struct {
	Size     int64
	Modified time.Time
}

type MediaSnapshot struct {
	Format             Format
	Metadata           Metadata
	ContentFingerprint string
	AudioFingerprint   string
}

type SnapshotReader interface {
	ReadStable(path string) (MediaSnapshot, FileVersion, FileVersion, error)
}
