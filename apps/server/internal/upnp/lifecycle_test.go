package upnp_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

type manualLifecycleTicker struct {
	channel chan time.Time
	stopped chan struct{}
}

func (ticker *manualLifecycleTicker) C() <-chan time.Time { return ticker.channel }
func (ticker *manualLifecycleTicker) Stop()               { close(ticker.stopped) }

type lifecycleClock struct {
	mu  sync.Mutex
	now time.Time
}

func (clock *lifecycleClock) read() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *lifecycleClock) advance(value time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(value)
	clock.mu.Unlock()
}

func awaitLifecycle[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("lifecycle event was not delivered")
		var zero T
		return zero
	}
}

func TestLifecycle_expires_suspends_and_reconciles_without_adopting_external_URI(t *testing.T) {
	// Given: an assigned K17, deterministic tickers, and a successful initial sighting.
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V262", protocolInfo: fixtureProtocol})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	network, err := upnp.NewNetwork("fixture", "127.0.0.1", "127.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	discoverer, err := upnp.NewDiscoverer(upnp.DiscoveryConfig{Networks: []upnp.Network{network}, SearchAddress: "127.0.0.1:1900", ResponseWindow: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	service, err := upnp.NewService(upnp.ServiceConfig{
		Discoverer: discoverer, Inspector: fixture.inspector(t),
		Observe: func(context.Context, upnp.K17Device, time.Time) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	scanTicker := &manualLifecycleTicker{channel: make(chan time.Time), stopped: make(chan struct{})}
	pollTicker := &manualLifecycleTicker{channel: make(chan time.Time), stopped: make(chan struct{})}
	clock := &lifecycleClock{now: time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)}
	var scanMu sync.Mutex
	present := true
	scan := func(context.Context) (upnp.ScanResult, error) {
		scanMu.Lock()
		defer scanMu.Unlock()
		if present {
			return upnp.ScanResult{Devices: []upnp.K17Device{device}}, nil
		}
		return upnp.ScanResult{}, nil
	}
	observed := make(chan upnp.LifecycleObservation, 8)
	recorded := make(chan upnp.ObservedState, 8)
	dispatched := make(chan playback.K17LifecycleResult, 8)
	unavailable := make(chan upnp.RendererID, 1)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- service.RunLifecycle(ctx, upnp.LifecycleConfig{
			ScanInterval: time.Minute, PollInterval: time.Second, StaleAfter: 2 * time.Minute,
			Clock: clock.read, Scan: scan,
			Ticker: func(interval time.Duration) upnp.LifecycleTicker {
				if interval == time.Minute {
					return scanTicker
				}
				return pollTicker
			},
			Targets: func(context.Context) ([]upnp.LifecycleTarget, error) {
				return []upnp.LifecycleTarget{{RendererID: device.ID, ZoneID: "living", LastSeenAt: clock.read()}}, nil
			},
			Record: func(_ context.Context, state upnp.ObservedState) (playback.K17LifecycleResult, error) {
				recorded <- state
				return playback.K17LifecycleResult{Action: playback.K17LifecycleReconciled}, nil
			},
			Dispatch: func(_ context.Context, result playback.K17LifecycleResult) error {
				dispatched <- result
				return nil
			},
			Unavailable: func(_ context.Context, id upnp.RendererID) error { unavailable <- id; return nil },
			Observed:    func(value upnp.LifecycleObservation) { observed <- value },
		})
	}()
	awaitLifecycle(t, recorded)
	if result := awaitLifecycle(t, dispatched); result.Action != playback.K17LifecycleReconciled {
		t.Fatalf("dispatched lifecycle result = %+v", result)
	}
	awaitLifecycle(t, observed)
	adapterValue, err := service.PlaybackAdapter(device.ID, "living")
	if err != nil {
		t.Fatal(err)
	}
	adapter := adapterValue.(*upnp.K17Adapter)
	mediaURL := fixture.server.URL + "/media/v1/signed-fixture"
	if err := adapter.SetAVTransportURI(context.Background(), fixtureMediaResource(mediaURL)); err != nil {
		t.Fatal(err)
	}

	// When: polling sees Server-owned playback, then an external URI, then a SOAP fault.
	fixture.mu.Lock()
	fixture.device.currentURI = mediaURL
	fixture.mu.Unlock()
	pollTicker.channel <- clock.read()
	owned := awaitLifecycle(t, recorded)
	awaitLifecycle(t, observed)
	fixture.mu.Lock()
	fixture.device.currentURI = fixture.server.URL + "/external.flac"
	fixture.mu.Unlock()
	pollTicker.channel <- clock.read()
	external := awaitLifecycle(t, recorded)
	awaitLifecycle(t, observed)
	fixture.mu.Lock()
	fixture.device.soapFaultAction = "GetTransportInfo"
	fixture.mu.Unlock()
	pollTicker.channel <- clock.read()
	fault := awaitLifecycle(t, observed)

	// Then: ownership is explicit and the polling failure remains a typed SOAP diagnostic.
	if !owned.Owned || external.Owned || external.CurrentURI == "" {
		t.Fatalf("owned/external observations = %+v / %+v", owned, external)
	}
	var soapFault *upnp.SOAPFault
	if !errors.As(fault.Error, &soapFault) || soapFault.Action != upnp.Action("GetTransportInfo") {
		t.Fatalf("poll fault = %#v", fault.Error)
	}

	// When: the sighting ages out and later reappears.
	fixture.mu.Lock()
	fixture.device.soapFaultAction = ""
	fixture.device.currentURI = mediaURL
	fixture.mu.Unlock()
	scanMu.Lock()
	present = false
	scanMu.Unlock()
	clock.advance(2 * time.Minute)
	scanTicker.channel <- clock.read()
	if got := awaitLifecycle(t, unavailable); got != device.ID {
		t.Fatalf("expired renderer = %q", got)
	}
	if _, err := service.PlaybackAdapter(device.ID, "living"); !errors.Is(err, upnp.ErrIdentityRejected) {
		t.Fatalf("adapter remained available after expiry: %v", err)
	}
	scanMu.Lock()
	present = true
	scanMu.Unlock()
	scanTicker.channel <- clock.read()
	pollTicker.channel <- clock.read()
	reappeared := awaitLifecycle(t, recorded)
	awaitLifecycle(t, observed)
	if !reappeared.Owned {
		t.Fatalf("reappearance adopted ambiguous playback: %+v", reappeared)
	}

	// When/Then: cancellation closes both ticker resources and returns the context outcome.
	cancel()
	if err := awaitLifecycle(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("lifecycle cancellation = %v", err)
	}
	awaitLifecycle(t, scanTicker.stopped)
	awaitLifecycle(t, pollTicker.stopped)
}
