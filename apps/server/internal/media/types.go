package media

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

var (
	ErrInvalidConfig     = errors.New("media: invalid configuration")
	ErrNoRepresentation  = errors.New("media: no compatible representation")
	ErrInvalidCapability = errors.New("media: invalid capability")
	ErrExpiredCapability = errors.New("media: capability expired")
	ErrWrongRenderer     = errors.New("media: capability belongs to another renderer")
	ErrWrongAudience     = errors.New("media: capability has the wrong audience")
	ErrUnauthorizedPlay  = errors.New("media: play identity is not active")
	ErrTrackUnavailable  = errors.New("media: track is unavailable")
	ErrStaleFile         = errors.New("media: file identity changed")
	ErrUnsafePath        = errors.New("media: unsafe catalog path")
	ErrRangeUnsupported  = errors.New("media: range is unsupported for this representation")
)

type Representation string

type Audience string

const (
	Original Representation = "original"
	L16      Representation = "l16"

	AudienceCustomRenderer Audience = "custom_renderer"
	AudienceK17Capability  Audience = "k17_capability"
)

type Grant struct {
	Audience       Audience
	RendererID     playback.RendererID
	ZoneID         playback.ZoneID
	PlayID         playback.PlayID
	TrackID        catalog.TrackID
	Representation Representation
	FileSize       int64
	ModifiedNS     int64
}

type Claims struct {
	KeyID          string              `json:"kid"`
	Audience       Audience            `json:"audience"`
	RendererID     playback.RendererID `json:"renderer"`
	ZoneID         playback.ZoneID     `json:"zone"`
	PlayID         playback.PlayID     `json:"play"`
	TrackID        catalog.TrackID     `json:"track"`
	Representation Representation      `json:"representation"`
	FileSize       int64               `json:"size"`
	ModifiedNS     int64               `json:"modified_ns"`
	ExpiresAt      int64               `json:"expires_at"`
}

type Clock interface{ Now() time.Time }

type Authorizer interface {
	AuthorizeMedia(context.Context, playback.MediaAuthorization) error
}

type TransformProvider interface {
	Open(context.Context, io.Reader, catalog.Format) (io.ReadCloser, error)
}
