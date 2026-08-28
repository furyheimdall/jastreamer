package playback

import (
	"encoding/json"
	"errors"
	"time"
)

const MaxRendererCommandAttempts = 8

var (
	ErrSensitivePayload        = errors.New("playback: command payload contains a persisted secret")
	ErrCommandDeliveryConflict = errors.New("playback: command delivery identity conflict")
	ErrCommandResultConflict   = errors.New("playback: command result conflict")
	ErrPlaybackEventConflict   = errors.New("playback: playback event identity conflict")
	ErrStaleRendererEpoch      = errors.New("playback: stale renderer session epoch")
	ErrCommandSequenceGap      = errors.New("playback: renderer command sequence gap")
	ErrNoRendererCommand       = errors.New("playback: no renderer command is ready")
	ErrCommandExpired          = errors.New("playback: renderer command deadline expired")
	ErrCommandRetryExhausted   = errors.New("playback: renderer command retry budget exhausted")
)

type playbackClock struct{}

func (playbackClock) Now() time.Time { return time.Now().UTC() }

type SessionEpoch string

type CommandAckStatus string

const (
	CommandAckReceived  CommandAckStatus = "received"
	CommandAckDuplicate CommandAckStatus = "duplicate"
	CommandAckRejected  CommandAckStatus = "rejected"
)

type PlaybackEventKind string

const (
	PlaybackEventPlaying PlaybackEventKind = "playing"
	PlaybackEventPaused  PlaybackEventKind = "paused"
	PlaybackEventEnded   PlaybackEventKind = "ended"
	PlaybackEventFailed  PlaybackEventKind = "failed"
)

type RendererSessionRequest struct {
	RendererID         RendererID
	LastServerSequence CommandSequence
	ConnectedAt        time.Time
}

type RendererSessionState struct {
	RendererID         RendererID
	Epoch              SessionEpoch
	Generation         int64
	NextSequence       CommandSequence
	LastServerSequence CommandSequence
	ConnectedAt        time.Time
}

type RendererSessionClose struct {
	RendererID     RendererID
	Epoch          SessionEpoch
	DisconnectedAt time.Time
}

type RendererSessionObservation struct {
	RendererID    RendererID
	Epoch         SessionEpoch
	ProtocolMajor int
	Capabilities  []string
	ObservedAt    time.Time
}

type RendererCommandRequest struct {
	RendererID  RendererID
	Epoch       SessionEpoch
	AttemptedAt time.Time
	Deadline    time.Time
}

type RendererCommandAcknowledgement struct {
	RendererID RendererID
	Epoch      SessionEpoch
	CommandID  string
	Sequence   CommandSequence
	Status     CommandAckStatus
	Error      json.RawMessage
	RecordedAt time.Time
}

type RendererTerminalResult struct {
	RendererID    RendererID
	Epoch         SessionEpoch
	CommandID     string
	ResultID      string
	Status        string
	ObservedState string
	PositionMS    *int64
	Payload       json.RawMessage
	ErrorCode     string
	ErrorDetail   string
	Historical    bool
	RecordedAt    time.Time
}

type RendererResultAcknowledgement struct {
	RendererID RendererID
	Epoch      SessionEpoch
	ResultID   string
	RecordedAt time.Time
}

type RendererPlaybackEvent struct {
	RendererID RendererID
	Epoch      SessionEpoch
	EventID    string
	PlayID     PlayID
	Kind       PlaybackEventKind
	PositionMS *int64
	ObservedAt time.Time
}

type RendererSessionTruth struct {
	RendererID      RendererID
	ZoneID          ZoneID
	ConnectionState string
	Epoch           SessionEpoch
	IntentSessionID SessionID
	IntentPlayID    PlayID
	IntentTransport Transport
	ObservedPlayID  PlayID
	ObservedState   string
	ObservedAt      time.Time
}
