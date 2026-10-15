package raat

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type Config struct {
	Interfaces        []string
	DiscoveryInterval time.Duration
	FFmpegPath        string
	Notify            func(string)
	OnControl         func(context.Context, output.RemoteControl) error
	State             func(context.Context) (TransportState, error)
}

type TransportState struct {
	RendererID, State, RepeatMode string
	PositionMS, DurationMS        int64
	Shuffle                       bool
	CanPlay, CanPause, CanSeek    bool
}

func New(config Config, catalog *library.Service) (*Manager, error) {
	if catalog == nil {
		return nil, errors.New("raat: library is required")
	}
	config.Interfaces = append([]string(nil), config.Interfaces...)
	if config.DiscoveryInterval == 0 {
		config.DiscoveryInterval = 30 * time.Second
	}
	if config.DiscoveryInterval < time.Second || config.DiscoveryInterval > 24*time.Hour {
		return nil, errors.New("raat: invalid discovery interval")
	}
	if _, err := resolveNetworks(config.Interfaces); err != nil {
		return nil, err
	}
	path := strings.TrimSpace(config.FFmpegPath)
	if path == "" {
		path = "ffmpeg"
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return nil, errors.New("raat: FFmpeg with fd protocol support is required")
	}
	config.FFmpegPath = resolved
	return &Manager{config: config, library: catalog, devices: make(map[string]discoveredDevice), prepared: make(map[string]preparedMedia), selected: make(map[string]preparedMedia), sessions: make(map[string]*session)}, nil
}
