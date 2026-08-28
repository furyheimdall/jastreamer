package playback

import (
	"context"
	"time"
)

type MediaRepresentation string

const (
	MediaOriginal MediaRepresentation = "original"
	MediaL16      MediaRepresentation = "l16"
)

type MediaResource struct {
	URL            string
	MimeType       string
	TrackID        TrackID
	Title          string
	Representation MediaRepresentation
}

// K17PlaybackAdapter is the deliberately narrow control surface supported by
// the allowlisted FiiO K17 adapter. It is not a generic UPnP renderer API.
type K17PlaybackAdapter interface {
	RendererID() RendererID
	ZoneID() ZoneID
	SetAVTransportURI(context.Context, MediaResource) error
	Play(context.Context) error
	Pause(context.Context) error
	Stop(context.Context) error
	Seek(context.Context, time.Duration) error
}
