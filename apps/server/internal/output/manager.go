package output

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
)

const (
	refreshTimeout  = 20 * time.Second
	shutdownTimeout = 5 * time.Second
)

var (
	ErrNotFound         = errors.New("output: device not found")
	ErrAmbiguous        = errors.New("output: device identity is ambiguous")
	ErrUnsupported      = errors.New("output: operation is not supported")
	ErrInvalidBackend   = errors.New("output: invalid backend")
	ErrShutdownTimedOut = errors.New("output: backend shutdown timed out")
)

type Manager struct {
	backends []Backend
}

func NewManager(backends ...Backend) *Manager {
	return &Manager{backends: append([]Backend(nil), backends...)}
}

type backendResult struct {
	protocol string
	err      error
}

func (manager *Manager) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !manager.valid() {
		return ErrInvalidBackend
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan backendResult, len(manager.backends))
	running := len(manager.backends)
	for _, backend := range manager.backends {
		go func(backend Backend) {
			results <- backendResult{protocol: backend.Protocol, err: backend.Controller.Run(runCtx)}
		}(backend)
	}

	remaining := running
	var failures []error
	for remaining > 0 {
		select {
		case result := <-results:
			remaining--
			if result.err != nil && !errors.Is(result.err, context.Canceled) {
				failures = append(failures, fmt.Errorf("%s output: %w", result.protocol, result.err))
				cancel()
				return errors.Join(append(failures, drainBackends(results, remaining)...)...)
			}
			if ctx.Err() == nil {
				failures = append(failures, fmt.Errorf("%s output stopped unexpectedly", result.protocol))
				cancel()
				return errors.Join(append(failures, drainBackends(results, remaining)...)...)
			}
		case <-ctx.Done():
			cancel()
			return errors.Join(drainBackends(results, remaining)...)
		}
	}
	return errors.Join(failures...)
}

func drainBackends(results <-chan backendResult, remaining int) []error {
	if remaining == 0 {
		return nil
	}
	timer := time.NewTimer(shutdownTimeout)
	defer timer.Stop()
	var failures []error
	for remaining > 0 {
		select {
		case result := <-results:
			remaining--
			if result.err != nil && !errors.Is(result.err, context.Canceled) {
				failures = append(failures, fmt.Errorf("%s output: %w", result.protocol, result.err))
			}
		case <-timer.C:
			return append(failures, ErrShutdownTimedOut)
		}
	}
	return failures
}

func (manager *Manager) Refresh(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !manager.valid() {
		return ErrInvalidBackend
	}
	refreshCtx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	results := make(chan backendResult, len(manager.backends))
	running := len(manager.backends)
	for _, backend := range manager.backends {
		go func(backend Backend) {
			results <- backendResult{protocol: backend.Protocol, err: backend.Controller.Refresh(refreshCtx)}
		}(backend)
	}

	var failures []error
	for range running {
		select {
		case result := <-results:
			if result.err != nil {
				failures = append(failures, fmt.Errorf("%s output refresh: %w", result.protocol, result.err))
			}
		case <-refreshCtx.Done():
			failures = append(failures, fmt.Errorf("output refresh: %w", refreshCtx.Err()))
			return errors.Join(failures...)
		}
	}

	return errors.Join(failures...)
}

func (manager *Manager) Devices() []Device {
	var devices []Device
	for _, backend := range manager.backends {
		if !validBackend(backend) {
			continue
		}
		for _, device := range backend.Controller.Devices() {
			device.Protocol = backend.Protocol
			devices = append(devices, cloneDevice(device))
		}
	}
	sort.Slice(devices, func(left, right int) bool {
		leftName := strings.ToLower(devices[left].Name)
		rightName := strings.ToLower(devices[right].Name)
		if leftName != rightName {
			return leftName < rightName
		}
		if devices[left].Protocol != devices[right].Protocol {
			return devices[left].Protocol < devices[right].Protocol
		}
		return devices[left].ID < devices[right].ID
	})
	return devices
}

func (manager *Manager) Device(id string) (Device, bool) {
	backend, device, err := manager.backendForID(id)
	if err != nil {
		return Device{}, false
	}
	device.Protocol = backend.Protocol
	return cloneDevice(device), true
}

func (manager *Manager) SetURI(ctx context.Context, id string, resource Resource) error {
	backend, _, err := manager.backendForID(id)
	if err != nil {
		return err
	}
	return backend.Controller.SetURI(ctx, id, resource)
}

func (manager *Manager) Play(ctx context.Context, id string) error {
	backend, _, err := manager.backendForID(id)
	if err != nil {
		return err
	}
	return backend.Controller.Play(ctx, id)
}

func (manager *Manager) Pause(ctx context.Context, id string) error {
	backend, _, err := manager.backendForID(id)
	if err != nil {
		return err
	}
	return backend.Controller.Pause(ctx, id)
}

func (manager *Manager) Stop(ctx context.Context, id string) error {
	backend, _, err := manager.backendForID(id)
	if err != nil {
		return err
	}
	return backend.Controller.Stop(ctx, id)
}

func (manager *Manager) Seek(ctx context.Context, id string, positionMS int64) error {
	backend, _, err := manager.backendForID(id)
	if err != nil {
		return err
	}
	return backend.Controller.Seek(ctx, id, positionMS)
}

func (manager *Manager) Observe(ctx context.Context, id string) (Observation, error) {
	backend, _, err := manager.backendForID(id)
	if err != nil {
		return Observation{}, err
	}
	return backend.Controller.Observe(ctx, id)
}

func (manager *Manager) Prepare(ctx context.Context, device Device, track library.Track, playID string) (Resource, error) {
	backend, current, err := manager.backendForID(device.ID)
	if err != nil {
		return Resource{}, err
	}
	if device.Protocol != backend.Protocol || current.Protocol != backend.Protocol {
		return Resource{}, ErrNotFound
	}
	if backend.Media == nil {
		return Resource{}, ErrUnsupported
	}
	current.Protocol = backend.Protocol
	return backend.Media.Prepare(ctx, current, track, playID)
}

func (manager *Manager) Revoke(playID string) {
	for _, backend := range manager.backends {
		if backend.Media != nil {
			backend.Media.Revoke(playID)
		}
	}
}

func (manager *Manager) Pair(ctx context.Context, id string, request PairingRequest) (PairingStatus, error) {
	backend, _, err := manager.backendForID(id)
	if err != nil {
		return PairingStatus{}, err
	}
	if backend.Pairer == nil {
		return PairingStatus{}, NewActionError(ErrorUnsupported, "Pair", 0, ErrUnsupported)
	}
	return backend.Pairer.Pair(ctx, id, request)
}

func (manager *Manager) backendForID(id string) (Backend, Device, error) {
	var matched Backend
	var device Device
	found := false
	for _, backend := range manager.backends {
		if !validBackend(backend) {
			continue
		}
		candidate, ok := backend.Controller.Device(id)
		if !ok || candidate.Protocol != backend.Protocol {
			continue
		}
		if found {
			return Backend{}, Device{}, ErrAmbiguous
		}
		matched = backend
		device = candidate
		found = true
	}
	if !found {
		return Backend{}, Device{}, ErrNotFound
	}
	return matched, device, nil
}

func validBackend(backend Backend) bool {
	return backend.Controller != nil && (backend.Protocol == ProtocolUPnP || backend.Protocol == ProtocolAirPlay)
}

func (manager *Manager) valid() bool {
	if len(manager.backends) == 0 {
		return false
	}
	for _, backend := range manager.backends {
		if !validBackend(backend) {
			return false
		}
	}
	return true
}

func cloneDevice(device Device) Device {
	device.ProtocolInfo = append([]string(nil), device.ProtocolInfo...)
	if device.ProtocolInfo == nil {
		device.ProtocolInfo = []string{}
	}
	return device
}
