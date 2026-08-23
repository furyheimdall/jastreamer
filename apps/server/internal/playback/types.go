package playback

import "errors"

const CurrentSchemaVersion = 2

var (
	ErrClosed              = errors.New("playback: store closed")
	ErrIdempotencyConflict = errors.New("playback: idempotency key reused with different request")
	ErrRevisionConflict    = errors.New("playback: stale revision")
	ErrSchemaTooNew        = errors.New("playback: database schema is newer than this binary")
	ErrCorruptDatabase     = errors.New("playback: database integrity check failed")
	ErrUnsafeWAL           = errors.New("playback: SQLite WAL build lacks required fix")
	ErrInvalidObservation  = errors.New("playback: renderer observation is inconsistent")
	ErrInvalidTransition   = errors.New("playback: invalid transport transition")
	ErrBoundaryConflict    = errors.New("playback: boundary identity reused with different previous play")
	ErrNonLocalDatabase    = errors.New("playback: database path must be a local filesystem path")
	ErrInvalidRequest      = errors.New("playback: invalid request")
	ErrQueueLimit          = errors.New("playback: enqueue exceeds 10,000 entries")
	ErrAutomaticPreempted  = errors.New("playback: automatic preview was preempted by explicit queue")
	ErrAutomaticConflict   = errors.New("playback: automatic boundary reused with different input")
)

type (
	ZoneID       string
	TrackID      string
	QueueEntryID string
	SessionID    string
	PlayID       string
	BoundaryID   string
	Revision     int64
)

type Transport string

const (
	TransportIdle      Transport = "idle"
	TransportSelecting Transport = "selecting"
	TransportStarting  Transport = "starting"
	TransportPlaying   Transport = "playing"
	TransportPaused    Transport = "paused"
	TransportBlocked   Transport = "blocked"
	TransportSuspended Transport = "suspended"
)

type TransportEvent string

const (
	EventStart            TransportEvent = "start"
	EventReserve          TransportEvent = "reserve"
	EventBlock            TransportEvent = "block"
	EventExhaust          TransportEvent = "exhaust"
	EventConfirm          TransportEvent = "confirm"
	EventPause            TransportEvent = "pause"
	EventResume           TransportEvent = "resume"
	EventBoundary         TransportEvent = "boundary"
	EventStop             TransportEvent = "stop"
	EventDisconnect       TransportEvent = "disconnect"
	EventFailure          TransportEvent = "failure"
	EventExternalOverride TransportEvent = "external_override"
	EventRetry            TransportEvent = "retry"
	EventSkip             TransportEvent = "skip"
)

type QueueState string

const (
	QueuePending   QueueState = "pending"
	QueueReserved  QueueState = "reserved"
	QueuePlaying   QueueState = "playing"
	QueueCompleted QueueState = "completed"
	QueueBlocked   QueueState = "blocked"
	QueueRemoved   QueueState = "removed"
)

type DecisionKind string

const (
	DecisionPlay  DecisionKind = "play"
	DecisionStop  DecisionKind = "stop"
	DecisionBlock DecisionKind = "block"
)

const (
	ReasonPlayExplicit  = "PLAY_EXPLICIT"
	ReasonPlayAutomatic = "PLAY_AUTOMATIC"
	ReasonBlockExplicit = "BLOCK_EXPLICIT"
	ReasonQueueEmpty    = "STOP_QUEUE_EMPTY"
)

type JournalMode string

const (
	JournalRollback JournalMode = "rollback"
	JournalWAL      JournalMode = "wal"
)

type Config struct {
	Path            string
	MigrationPath   string
	BackupDirectory string
	SupportedSchema int
	JournalMode     JournalMode
}

type QueueTrack struct {
	ID        TrackID
	Available bool
}

type EnqueueRequest struct {
	ZoneID           ZoneID
	IdempotencyKey   string
	ExpectedRevision Revision
	Tracks           []QueueTrack
}

type EnqueueResult struct {
	Revision Revision
	EntryIDs []QueueEntryID
}

type AvailabilityRequest struct {
	ZoneID           ZoneID
	TrackID          TrackID
	Available        bool
	ExpectedRevision Revision
}

type AutomaticPreviewRequest struct {
	ZoneID           ZoneID
	Boundary         Boundary
	TrackID          TrackID
	ExpectedRevision Revision
}

type Boundary struct {
	ID             BoundaryID
	PreviousPlayID PlayID
}

type Decision struct {
	ID           string
	Kind         DecisionKind
	Reason       string
	PlayID       PlayID
	QueueEntryID QueueEntryID
	TrackID      TrackID
	Revision     Revision
}

type QueueEntry struct {
	ID       QueueEntryID
	TrackID  TrackID
	State    QueueState
	Position int64
}

type ZoneSnapshot struct {
	ZoneID      ZoneID
	Revision    Revision
	Transport   Transport
	SessionID   SessionID
	SessionSeed string
	CurrentPlay PlayID
	Queue       []QueueEntry
}

type OutboxCommand struct {
	ID     string
	PlayID PlayID
	Type   string
	State  string
}

type RendererObservation struct {
	OutcomeKnown bool
	Playing      bool
	PlayID       PlayID
}

type ReconcileResult struct {
	Transport Transport
	PlayID    PlayID
}

type migrationHook func(version int) error

type RestoreRequest struct {
	BackupPath      string
	TargetPath      string
	SupportedSchema int
}

type decisionKey struct {
	zoneID       ZoneID
	sessionID    SessionID
	boundaryID   BoundaryID
	previousPlay PlayID
}
type playCompletion struct {
	zoneID   ZoneID
	playID   PlayID
	revision Revision
}
type sessionEnd struct {
	sessionID SessionID
	revision  Revision
	reason    string
}
type queueTransition struct {
	entryID  QueueEntryID
	state    QueueState
	playID   PlayID
	revision Revision
}
type reservation struct {
	zoneID    ZoneID
	sessionID SessionID
	boundary  Boundary
	decision  Decision
	revision  Revision
}
type decisionRecord struct {
	zoneID         ZoneID
	sessionID      SessionID
	boundaryID     BoundaryID
	previousPlayID PlayID
	sequence       int64
	decision       Decision
}
type zoneDecisionUpdate struct {
	zoneID    ZoneID
	sessionID SessionID
	seed      string
	playID    PlayID
	revision  Revision
	sequence  int64
	transport Transport
}
type migrationRun struct {
	db     *sqliteDB
	config Config
	hook   migrationHook
}
