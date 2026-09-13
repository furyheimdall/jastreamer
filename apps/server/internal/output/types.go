package output

import (
	"context"
	"fmt"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
)

const (
	ProtocolUPnP    = "upnp"
	ProtocolAirPlay = "airplay"
)

type Resource struct {
	URL        string `json:"url"`
	Mime       string `json:"mime"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	ArtworkURL string `json:"artwork_url"`
	DurationMS int64  `json:"duration_ms"`
	Size       int64  `json:"size"`
	Seekable   bool   `json:"seekable"`
	TrackID    string `json:"-"`
	PlayID     string `json:"-"`
}

type Capabilities struct {
	Play  bool `json:"play"`
	Pause bool `json:"pause"`
	Stop  bool `json:"stop"`
	Seek  bool `json:"seek"`
}

type Device struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Address      string `json:"address"`
	// LocalAddress is the server interface used to reach this output, when known.
	LocalAddress     string       `json:"-"`
	Online           bool         `json:"online"`
	LastSeen         string       `json:"last_seen"`
	Capabilities     Capabilities `json:"capabilities"`
	ProtocolInfo     []string     `json:"protocol_info"`
	Protocol         string       `json:"protocol"`
	PairingRequired  bool         `json:"pairing_required"`
	PasswordRequired bool         `json:"password_required"`
}

type Observation struct {
	State           string    `json:"state"`
	PositionMS      int64     `json:"position_ms"`
	DurationMS      int64     `json:"duration_ms"`
	URI             string    `json:"uri"`
	HasURI          bool      `json:"has_uri"`
	HasPosition     bool      `json:"has_position"`
	ObservedAt      time.Time `json:"observed_at"`
	TransportStatus string    `json:"transport_status"`
}

type ErrorKind string

const (
	ErrorUnsupported ErrorKind = "unsupported"
	ErrorTimeout     ErrorKind = "timeout"
	ErrorCancelled   ErrorKind = "cancelled"
	ErrorTransport   ErrorKind = "transport"
	ErrorResponse    ErrorKind = "response"
	ErrorFault       ErrorKind = "fault"
)

// ActionError classifies an output failure without exposing endpoint or media URLs.
type ActionError struct {
	Kind   ErrorKind
	Action string
	Code   int
	cause  error
}

func NewActionError(kind ErrorKind, action string, code int, cause error) *ActionError {
	return &ActionError{Kind: kind, Action: action, Code: code, cause: cause}
}

func (err *ActionError) Error() string {
	if err.Code != 0 {
		return fmt.Sprintf("output: %s failed (%s, code %d)", err.Action, err.Kind, err.Code)
	}
	return fmt.Sprintf("output: %s failed (%s)", err.Action, err.Kind)
}

func (err *ActionError) Unwrap() error { return err.cause }

type Controller interface {
	Run(context.Context) error
	Refresh(context.Context) error
	Devices() []Device
	Device(string) (Device, bool)
	SetURI(context.Context, string, Resource) error
	Play(context.Context, string) error
	Pause(context.Context, string) error
	Stop(context.Context, string) error
	Seek(context.Context, string, int64) error
	Observe(context.Context, string) (Observation, error)
}

type Media interface {
	Prepare(context.Context, Device, library.Track, string) (Resource, error)
	Revoke(string)
}

type PairingRequest struct {
	PIN      string `json:"pin"`
	Password string `json:"password"`
}

type PairingStatus struct {
	Required bool   `json:"required"`
	Prompt   string `json:"prompt"`
}

type Pairer interface {
	Pair(context.Context, string, PairingRequest) (PairingStatus, error)
}

type Backend struct {
	Protocol   string
	Controller Controller
	Media      Media
	Pairer     Pairer
}
