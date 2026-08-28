package api

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

const (
	rendererStaleCloseReason     = "stale session epoch"
	rendererRevokedCloseReason   = "device revoked"
	rendererShutdownCloseReason  = "server shutting down"
	rendererProtocolCloseReason  = "renderer protocol error"
	rendererHeartbeatCloseReason = "renderer heartbeat expired"
)

var errRendererSessionTerminal = errors.New("renderer session is terminal")

type rendererSessionCloseSignal struct {
	code    uint16
	reason  string
	revoked bool
}

type rendererSessionSignal struct {
	mu        sync.Mutex
	closing   rendererSessionCloseSignal
	terminal  bool
	changed   chan struct{}
	interrupt func()
}

func newRendererSessionSignal(interrupt func()) *rendererSessionSignal {
	return &rendererSessionSignal{changed: make(chan struct{}, 1), interrupt: interrupt}
}

func (signal *rendererSessionSignal) Close() error {
	signal.revoke()
	return nil
}

func (signal *rendererSessionSignal) revoke() {
	signal.stop(rendererSessionCloseSignal{
		code: closePolicyViolation, reason: rendererRevokedCloseReason, revoked: true,
	})
}

func (signal *rendererSessionSignal) stop(closing rendererSessionCloseSignal) {
	signal.mu.Lock()
	changed := !signal.terminal && signal.closing.code == 0
	if !signal.terminal && closing.revoked && !signal.closing.revoked {
		changed = true
	}
	if changed {
		signal.closing = closing
		select {
		case signal.changed <- struct{}{}:
		default:
		}
	}
	interrupt := signal.interrupt
	signal.mu.Unlock()
	if changed && interrupt != nil {
		interrupt()
	}
}

func (signal *rendererSessionSignal) writeIfActive(write func() error) (bool, error) {
	signal.mu.Lock()
	defer signal.mu.Unlock()
	if signal.terminal || signal.closing.code != 0 {
		return false, nil
	}
	return true, write()
}

func (signal *rendererSessionSignal) terminate(
	proposed rendererSessionCloseSignal,
	beforeClose func() error,
	writeCloseFrame func(rendererSessionCloseSignal) error,
) error {
	signal.mu.Lock()
	defer signal.mu.Unlock()
	if signal.terminal {
		return nil
	}
	selected := false
	if signal.closing.code == 0 || (proposed.revoked && !signal.closing.revoked) {
		signal.closing = proposed
		selected = proposed.code != 0
	}
	signal.terminal = true
	if signal.closing.code == 0 {
		return nil
	}
	if selected && beforeClose != nil {
		if err := beforeClose(); err != nil {
			return err
		}
	}
	return writeCloseFrame(signal.closing)
}

var _ io.Closer = (*rendererSessionSignal)(nil)

type rendererSessionRegistry struct {
	mu          sync.Mutex
	current     map[playback.RendererID]*rendererSessionSignal
	subscribers map[playback.RendererID]map[*rendererSessionSignal]struct{}
	revoked     map[playback.RendererID]struct{}
	shutdown    <-chan struct{}
}

func newRendererSessionRegistry(ctx context.Context) *rendererSessionRegistry {
	registry := &rendererSessionRegistry{
		current:     make(map[playback.RendererID]*rendererSessionSignal),
		subscribers: make(map[playback.RendererID]map[*rendererSessionSignal]struct{}),
		revoked:     make(map[playback.RendererID]struct{}),
	}
	if ctx != nil {
		registry.shutdown = ctx.Done()
	}
	return registry
}

func (registry *rendererSessionRegistry) subscribe(
	rendererID playback.RendererID,
	signal *rendererSessionSignal,
) func() {
	registry.mu.Lock()
	if registry.subscribers[rendererID] == nil {
		registry.subscribers[rendererID] = make(map[*rendererSessionSignal]struct{})
	}
	registry.subscribers[rendererID][signal] = struct{}{}
	_, revoked := registry.revoked[rendererID]
	registry.mu.Unlock()
	if revoked {
		signal.revoke()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			registry.mu.Lock()
			delete(registry.subscribers[rendererID], signal)
			if len(registry.subscribers[rendererID]) == 0 {
				delete(registry.subscribers, rendererID)
			}
			if registry.current[rendererID] == signal {
				delete(registry.current, rendererID)
			}
			registry.mu.Unlock()
		})
	}
}

func (registry *rendererSessionRegistry) activate(
	rendererID playback.RendererID,
	signal *rendererSessionSignal,
) {
	registry.mu.Lock()
	previous := registry.current[rendererID]
	_, revoked := registry.revoked[rendererID]
	if !revoked {
		registry.current[rendererID] = signal
	}
	registry.mu.Unlock()
	if revoked {
		signal.revoke()
		return
	}
	if previous != nil && previous != signal {
		previous.stop(rendererSessionCloseSignal{code: closePolicyViolation, reason: rendererStaleCloseReason})
	}
}

func (registry *rendererSessionRegistry) revoke(rendererID playback.RendererID) {
	registry.mu.Lock()
	registry.revoked[rendererID] = struct{}{}
	signals := make([]*rendererSessionSignal, 0, len(registry.subscribers[rendererID]))
	for signal := range registry.subscribers[rendererID] {
		signals = append(signals, signal)
	}
	registry.mu.Unlock()
	for _, signal := range signals {
		signal.revoke()
	}
}
