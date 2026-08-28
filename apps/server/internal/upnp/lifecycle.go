package upnp

import (
	"context"
	"fmt"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type LifecycleTarget struct {
	RendererID RendererID
	ZoneID     ZoneID
	LastSeenAt time.Time
}

type LifecycleConfig struct {
	ScanInterval time.Duration
	PollInterval time.Duration
	StaleAfter   time.Duration
	Clock        func() time.Time
	Ticker       func(time.Duration) LifecycleTicker
	Scan         func(context.Context) (ScanResult, error)
	Targets      func(context.Context) ([]LifecycleTarget, error)
	Record       func(context.Context, ObservedState) (playback.K17LifecycleResult, error)
	Dispatch     func(context.Context, playback.K17LifecycleResult) error
	Unavailable  func(context.Context, RendererID) error
	Observed     func(LifecycleObservation)
}

type LifecycleTicker interface {
	C() <-chan time.Time
	Stop()
}

type lifecycleTicker struct{ value *time.Ticker }

func (ticker lifecycleTicker) C() <-chan time.Time { return ticker.value.C }
func (ticker lifecycleTicker) Stop()               { ticker.value.Stop() }

type LifecycleObservation struct {
	State  ObservedState
	Result playback.K17LifecycleResult
	Error  error
}

func (service *Service) RunLifecycle(ctx context.Context, config LifecycleConfig) error {
	if config.ScanInterval <= 0 || config.PollInterval <= 0 || config.StaleAfter <= 0 ||
		config.Targets == nil || config.Record == nil || config.Unavailable == nil {
		return ErrInvalidConfig
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.Ticker == nil {
		config.Ticker = func(interval time.Duration) LifecycleTicker {
			return lifecycleTicker{value: time.NewTicker(interval)}
		}
	}
	scanTicker := config.Ticker(config.ScanInterval)
	pollTicker := config.Ticker(config.PollInterval)
	defer scanTicker.Stop()
	defer pollTicker.Stop()
	sightings := map[RendererID]time.Time{}
	if err := service.refresh(ctx, config, sightings); err != nil {
		return err
	}
	service.poll(ctx, config)
	for {
		select {
		case <-scanTicker.C():
			if err := service.refresh(ctx, config, sightings); err != nil {
				return err
			}
		case <-pollTicker.C():
			service.poll(ctx, config)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (service *Service) refresh(ctx context.Context, config LifecycleConfig, sightings map[RendererID]time.Time) error {
	scan := config.Scan
	if scan == nil {
		scan = service.Scan
	}
	result, err := scan(ctx)
	if err != nil {
		return fmt.Errorf("refresh K17 discovery: %w", err)
	}
	now := config.Clock()
	service.mu.Lock()
	for _, device := range result.Devices {
		service.devices[device.ID] = device
		sightings[device.ID] = now
	}
	service.mu.Unlock()
	targets, targetErr := config.Targets(ctx)
	if targetErr != nil {
		return fmt.Errorf("load K17 lifecycle targets: %w", targetErr)
	}
	for _, target := range targets {
		if _, found := sightings[target.RendererID]; !found && !target.LastSeenAt.IsZero() {
			sightings[target.RendererID] = target.LastSeenAt
		}
	}
	for rendererID, seenAt := range sightings {
		if now.Sub(seenAt) < config.StaleAfter {
			continue
		}
		if err := config.Unavailable(ctx, rendererID); err != nil {
			return fmt.Errorf("expire K17 %s: %w", rendererID, err)
		}
		service.expire(rendererID)
		delete(sightings, rendererID)
	}
	return nil
}

func (service *Service) poll(ctx context.Context, config LifecycleConfig) {
	targets, err := config.Targets(ctx)
	if err != nil {
		service.recordLifecycle(config, LifecycleObservation{Error: err})
		return
	}
	for _, target := range targets {
		adapter, adapterErr := service.adapter(target.RendererID, target.ZoneID)
		if adapterErr != nil {
			service.recordLifecycle(config, LifecycleObservation{Error: adapterErr})
			continue
		}
		state, observeErr := adapter.Observe(ctx)
		if observeErr != nil {
			service.recordLifecycle(config, LifecycleObservation{Error: observeErr})
			continue
		}
		result, recordErr := config.Record(ctx, state)
		if recordErr != nil {
			service.recordLifecycle(config, LifecycleObservation{State: state, Error: recordErr})
			continue
		}
		if config.Dispatch != nil {
			if dispatchErr := config.Dispatch(ctx, result); dispatchErr != nil {
				service.recordLifecycle(config, LifecycleObservation{State: state, Result: result, Error: dispatchErr})
				continue
			}
		}
		service.recordLifecycle(config, LifecycleObservation{State: state, Result: result})
	}
}

func (service *Service) recordLifecycle(config LifecycleConfig, observation LifecycleObservation) {
	service.mu.Lock()
	service.lifecycle = append(service.lifecycle, observation)
	if len(service.lifecycle) > 32 {
		service.lifecycle = append([]LifecycleObservation(nil), service.lifecycle[len(service.lifecycle)-32:]...)
	}
	service.mu.Unlock()
	if config.Observed != nil {
		config.Observed(observation)
	}
}

func (service *Service) LifecycleObservations() []LifecycleObservation {
	service.mu.RLock()
	defer service.mu.RUnlock()
	return append([]LifecycleObservation(nil), service.lifecycle...)
}
