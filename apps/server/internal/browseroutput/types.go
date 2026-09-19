package browseroutput

import (
	"errors"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

var (
	ErrInvalidRequest = errors.New("browser output: invalid request")
	ErrNotFound       = errors.New("browser output: registration not found")
	ErrUnauthorized   = errors.New("browser output: owner credential rejected")
	ErrCapacity       = errors.New("browser output: registration capacity reached")
	ErrStaleReport    = errors.New("browser output: stale report")
	ErrConflict       = errors.New("browser output: command conflict")
)

type Registration struct {
	ID              string        `json:"registration_id"`
	OwnerToken      string        `json:"owner_token,omitempty"`
	LeaseExpiresAt  time.Time     `json:"lease_expires_at"`
	LeaseDurationMS int64         `json:"lease_duration_ms"`
	PollAfterMS     int64         `json:"poll_after_ms"`
	Device          output.Device `json:"device"`
}

type Lease struct {
	LeaseExpiresAt       time.Time `json:"lease_expires_at"`
	LeaseDurationMS      int64     `json:"lease_duration_ms"`
	CancelBeforeSequence uint64    `json:"cancel_before_sequence"`
}

type Command struct {
	Sequence   uint64           `json:"sequence"`
	Action     string           `json:"action"`
	PlayID     string           `json:"play_id,omitempty"`
	PositionMS int64            `json:"position_ms,omitempty"`
	Resource   *output.Resource `json:"resource,omitempty"`
}

type CommandPoll struct {
	LeaseExpiresAt       time.Time `json:"lease_expires_at"`
	LeaseDurationMS      int64     `json:"lease_duration_ms"`
	CancelBeforeSequence uint64    `json:"cancel_before_sequence"`
	PollAfterMS          int64     `json:"poll_after_ms"`
	Command              *Command  `json:"command,omitempty"`
}

type ObservationReport struct {
	Event       string `json:"event"`
	PlayID      string `json:"play_id"`
	State       string `json:"state,omitempty"`
	PositionMS  int64  `json:"position_ms"`
	DurationMS  int64  `json:"duration_ms"`
	HasPosition bool   `json:"has_position"`
}

type Report struct {
	Sequence    uint64             `json:"sequence"`
	Result      string             `json:"result,omitempty"`
	ErrorCode   string             `json:"error_code,omitempty"`
	Observation *ObservationReport `json:"observation,omitempty"`
}
