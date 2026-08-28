package playback

import (
	"encoding/json"
	"time"
)

type (
	RendererID       string
	CatalogRootID    string
	CatalogScanJobID string
	AssignmentID     string
	SigningKeyID     string
	EventEpoch       int64
	CommandSequence  int64
)

type RendererKind string

const (
	RendererKindK17  RendererKind = "k17"
	RendererKindJake RendererKind = "jake"
)

type RendererState string

const (
	RendererUnavailable  RendererState = "unavailable"
	RendererAvailable    RendererState = "available"
	RendererConnected    RendererState = "connected"
	RendererIncompatible RendererState = "incompatible"
	RendererRevoked      RendererState = "revoked"
)

type ScanJobStatus string

const (
	ScanJobQueued    ScanJobStatus = "queued"
	ScanJobRunning   ScanJobStatus = "running"
	ScanJobComplete  ScanJobStatus = "complete"
	ScanJobFailed    ScanJobStatus = "failed"
	ScanJobCancelled ScanJobStatus = "cancelled"
)

type FFmpegStatus string

const (
	FFmpegUnconfigured FFmpegStatus = "unconfigured"
	FFmpegAvailable    FFmpegStatus = "available"
	FFmpegUnavailable  FFmpegStatus = "unavailable"
	FFmpegIncompatible FFmpegStatus = "incompatible"
)

type SettingsState struct {
	SchemaVersion int
	Revision      Revision
	Document      json.RawMessage
	UpdatedAt     time.Time
}

type CatalogRoot struct {
	ID            CatalogRootID
	DisplayName   string
	CanonicalPath string
	Enabled       bool
	Revision      Revision
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type CatalogScanJob struct {
	ID                CatalogScanJobID
	RootID            CatalogRootID
	RequestedRevision Revision
	Status            ScanJobStatus
	RequestedAt       time.Time
	StartedAt         time.Time
	FinishedAt        time.Time
	CatalogRevision   Revision
	ErrorCode         string
	ErrorDetail       string
}

type Renderer struct {
	ID                  RendererID
	Kind                RendererKind
	DisplayName         string
	State               RendererState
	ProtocolMajor       int
	FirmwareVersion     string
	EndpointFingerprint string
	Revision            Revision
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type RendererCapability struct {
	RendererID       RendererID
	Name             string
	Value            string
	ObservedRevision Revision
	ObservedAt       time.Time
}

type ZoneAssignment struct {
	ID                 AssignmentID
	ZoneID             ZoneID
	RendererID         RendererID
	AssignedRevision   Revision
	AssignedAt         time.Time
	UnassignedRevision Revision
	UnassignedAt       time.Time
}

type EncryptedMediaSigningKey struct {
	ID            SigningKeyID
	Digest        string
	Ciphertext    []byte
	Nonce         []byte
	WrappingKeyID string
	CreatedAt     time.Time
	RetiredAt     time.Time
}

type EventEpochState struct {
	Epoch     EventEpoch
	Revision  Revision
	UpdatedAt time.Time
}

type CommandReceiptState string

const (
	CommandReceiptPending  CommandReceiptState = "pending"
	CommandReceiptReceived CommandReceiptState = "received"
	CommandReceiptTerminal CommandReceiptState = "terminal"
)

type CommandDelivery struct {
	CommandID  string
	RendererID RendererID
	Sequence   CommandSequence
	Payload    json.RawMessage
	CreatedAt  time.Time
}

type CommandAttempt struct {
	CommandID   string
	AttemptedAt time.Time
	ErrorCode   string
	ErrorDetail string
}

type CommandReceipt struct {
	CommandID  string
	ReceivedAt time.Time
}

type DurableCommand struct {
	ID              string
	ZoneID          ZoneID
	SessionID       SessionID
	PlayID          PlayID
	TrackID         TrackID
	RendererID      RendererID
	Sequence        CommandSequence
	Type            string
	Payload         json.RawMessage
	Deadline        time.Time
	ReceiptState    CommandReceiptState
	AckStatus       CommandAckStatus
	Attempts        int
	MaxAttempts     int
	LastErrorCode   string
	LastErrorDetail string
	Result          json.RawMessage
	CreatedRevision Revision
	CreatedAt       time.Time
	LastAttemptAt   time.Time
	NextAttemptAt   time.Time
	ReceivedAt      time.Time
	TerminalAt      time.Time
	SupersededAt    time.Time
	ResultAckAt     time.Time
}

type CommandResult struct {
	CommandID   string
	RendererID  RendererID
	Sequence    CommandSequence
	Outcome     string
	Result      json.RawMessage
	ErrorCode   string
	ErrorDetail string
	RecordedAt  time.Time
}

type FFmpegProbe struct {
	ConfiguredPath        string
	ExecutableFingerprint string
	Status                FFmpegStatus
	Version               string
	Codecs                []string
	ErrorCode             string
	ErrorDetail           string
	Revision              Revision
	ProbedAt              time.Time
}
