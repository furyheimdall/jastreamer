package upnp

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type ObserveDeviceFunc func(context.Context, K17Device, time.Time) error

type ServiceConfig struct {
	Discoverer *Discoverer
	Inspector  *Inspector
	Observe    ObserveDeviceFunc
	Clock      func() time.Time
}

type ScanDiagnostic struct {
	Candidate Candidate
	Error     error
}

type ScanResult struct {
	Devices     []K17Device
	Diagnostics []ScanDiagnostic
}

type Service struct {
	discoverer *Discoverer
	inspector  *Inspector
	observe    ObserveDeviceFunc
	now        func() time.Time
	mu         sync.RWMutex
	last       ScanResult
	devices    map[RendererID]K17Device
	adapters   map[adapterKey]*K17Adapter
	lifecycle  []LifecycleObservation
}

func NewService(config ServiceConfig) (*Service, error) {
	if config.Discoverer == nil || config.Inspector == nil || config.Observe == nil {
		return nil, ErrInvalidConfig
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &Service{
		discoverer: config.Discoverer, inspector: config.Inspector, observe: config.Observe, now: config.Clock,
		devices: map[RendererID]K17Device{}, adapters: map[adapterKey]*K17Adapter{},
	}, nil
}

func (service *Service) Scan(ctx context.Context) (ScanResult, error) {
	candidates, err := service.discoverer.Discover(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	result := ScanResult{Devices: []K17Device{}, Diagnostics: []ScanDiagnostic{}}
	for _, candidate := range candidates {
		device, inspectErr := service.inspector.InspectK17(ctx, candidate)
		if inspectErr != nil {
			result.Diagnostics = append(result.Diagnostics, ScanDiagnostic{Candidate: candidate, Error: inspectErr})
			continue
		}
		if observeErr := service.observe(ctx, device, service.now()); observeErr != nil {
			return ScanResult{}, fmt.Errorf("record observed K17: %w", observeErr)
		}
		result.Devices = append(result.Devices, device)
	}
	service.mu.Lock()
	service.last = cloneScanResult(result)
	for _, device := range result.Devices {
		service.devices[device.ID] = device
	}
	service.mu.Unlock()
	return result, nil
}

func (service *Service) LastScan() ScanResult {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return cloneScanResult(service.last)
}

type adapterKey struct {
	rendererID RendererID
	zoneID     ZoneID
}

func (service *Service) PlaybackAdapter(rendererID RendererID, zoneID ZoneID) (playback.K17PlaybackAdapter, error) {
	return service.adapter(rendererID, zoneID)
}

func (service *Service) adapter(rendererID RendererID, zoneID ZoneID) (*K17Adapter, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	device, found := service.devices[rendererID]
	if !found {
		return nil, ErrIdentityRejected
	}
	key := adapterKey{rendererID: rendererID, zoneID: zoneID}
	if adapter := service.adapters[key]; adapter != nil {
		return adapter, nil
	}
	adapter, err := NewK17Adapter(AdapterConfig{
		Device: device, RendererID: rendererID, ZoneID: zoneID,
		HTTPClient: service.inspector.config.HTTPClient,
	})
	if err != nil {
		return nil, err
	}
	service.adapters[key] = adapter
	return adapter, nil
}

func (service *Service) expire(rendererID RendererID) {
	service.mu.Lock()
	defer service.mu.Unlock()
	delete(service.devices, rendererID)
}

func cloneScanResult(value ScanResult) ScanResult {
	return ScanResult{Devices: append([]K17Device(nil), value.Devices...), Diagnostics: append([]ScanDiagnostic(nil), value.Diagnostics...)}
}
