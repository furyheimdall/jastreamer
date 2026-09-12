package dlna

import (
	"errors"
	"time"
)

const (
	mediaRendererPrefix     = "urn:schemas-upnp-org:device:MediaRenderer:"
	mediaRendererTarget     = mediaRendererPrefix + "1"
	avTransportPrefix       = "urn:schemas-upnp-org:service:AVTransport:"
	connectionManagerPrefix = "urn:schemas-upnp-org:service:ConnectionManager:"
)

var (
	ErrInvalidConfig   = errors.New("dlna: invalid configuration")
	ErrInvalidResource = errors.New("dlna: invalid media resource")
	ErrNotFound        = errors.New("dlna: renderer not found")
	ErrOffline         = errors.New("dlna: renderer is offline")
	ErrUnsupported     = errors.New("dlna: action is not supported")
	ErrTimeout         = errors.New("dlna: renderer timed out")
	ErrUnavailable     = errors.New("dlna: renderer is unavailable")
	ErrInvalidResponse = errors.New("dlna: invalid renderer response")
)

type Config struct {
	Interfaces        []string
	DiscoveryInterval time.Duration
	Notify            func(string)
}
