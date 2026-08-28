package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/settings"
	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

func newK17UPnP(values settings.Values, store *playback.Store) (*upnp.Service, error) {
	if len(values.UPnPInterfaces) == 0 {
		return nil, nil
	}
	networks, err := upnp.ResolveNetworks(values.UPnPInterfaces)
	if err != nil {
		return nil, err
	}
	discoverer, err := upnp.NewDiscoverer(upnp.DiscoveryConfig{Networks: networks, ResponseWindow: 2 * time.Second})
	if err != nil {
		return nil, err
	}
	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          8,
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       30 * time.Second,
			ResponseHeaderTimeout: 3 * time.Second,
		},
	}
	inspector, err := upnp.NewInspector(upnp.InspectorConfig{
		Networks: networks, HTTPClient: httpClient,
		Policy: upnp.K17Policy{
			Manufacturer: "FiiO", Model: "FiiO K17", Firmware: []string{"V261"},
			ProtocolInfo: []string{
				"http-get:*:audio/flac:*", "http-get:*:audio/mpeg:*", "http-get:*:audio/ogg:*",
				"http-get:*:audio/wav:*", "http-get:*:audio/L16:*",
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return upnp.NewService(upnp.ServiceConfig{
		Discoverer: discoverer, Inspector: inspector, Clock: time.Now,
		Observe: func(ctx context.Context, device upnp.K17Device, observedAt time.Time) error {
			_, err := store.UpsertK17Renderer(ctx, playback.K17Renderer{
				ID: playback.RendererID(device.ID), DisplayName: device.FriendlyName, State: playback.RendererAvailable,
				UDN: device.UDN, Model: device.Evidence.Model, FirmwareVersion: device.Evidence.Firmware,
				DescriptionURL: device.DescriptionURL, AVTransportControlURL: device.AVTransportControlURL,
				ConnectionManagerControlURL: device.ConnectionControlURL, ProtocolInfo: device.Evidence.ProtocolInfo,
				Protocols: device.Protocols(), LastSeenAt: observedAt,
			})
			if err != nil {
				return fmt.Errorf("persist observed K17 %s: %w", device.ID, err)
			}
			return nil
		},
	})
}

type k17LifecycleDispatcher interface {
	RecoverPending(context.Context) error
	Dispatch(context.Context, playback.K17LifecycleResult) error
}

type k17LifecycleRuntime struct {
	service           *upnp.Service
	store             *playback.Store
	media             *media.Service
	compatibilityURL  string
	serverHTTPSOrigin api.ServerHTTPSOrigin
	errors            chan<- error
}

func startK17Lifecycle(ctx context.Context, runtime k17LifecycleRuntime) {
	baseURL := runtime.compatibilityURL
	if baseURL == "" {
		baseURL = runtime.serverHTTPSOrigin.String()
	}
	dispatcher := api.NewK17LifecycleDispatcher(api.K17LifecycleDispatcherConfig{
		Queue: runtime.store, Media: runtime.media, Provider: runtime.service, BaseURL: baseURL,
	})
	go func() {
		if err := dispatcher.RecoverPending(ctx); err != nil {
			runtime.errors <- err
			return
		}
		err := runtime.service.RunLifecycle(ctx, k17LifecycleConfig(runtime.store, dispatcher))
		if !errors.Is(err, context.Canceled) {
			runtime.errors <- err
		}
	}()
}

func k17LifecycleConfig(store *playback.Store, dispatcher k17LifecycleDispatcher) upnp.LifecycleConfig {
	return upnp.LifecycleConfig{
		ScanInterval: 30 * time.Second, PollInterval: 2 * time.Second, StaleAfter: 90 * time.Second,
		Targets: func(ctx context.Context) ([]upnp.LifecycleTarget, error) {
			targets, err := store.K17LifecycleTargets(ctx)
			result := make([]upnp.LifecycleTarget, len(targets))
			for index, target := range targets {
				result[index] = upnp.LifecycleTarget{
					RendererID: target.RendererID, ZoneID: target.ZoneID, LastSeenAt: target.LastSeenAt,
				}
			}
			return result, err
		},
		Record: func(ctx context.Context, state upnp.ObservedState) (playback.K17LifecycleResult, error) {
			return store.ApplyK17Observation(ctx, playback.K17Observation{
				RendererID: state.RendererID, ZoneID: state.ZoneID, Transport: string(state.Transport),
				Position: state.Position, CurrentURI: state.CurrentURI, Owned: state.Owned, ObservedAt: state.ObservedAt,
			})
		},
		Dispatch: func(ctx context.Context, result playback.K17LifecycleResult) error {
			if dispatcher == nil {
				return nil
			}
			return dispatcher.Dispatch(ctx, result)
		},
		Unavailable: func(ctx context.Context, rendererID upnp.RendererID) error {
			return store.MarkK17Unavailable(ctx, rendererID)
		},
	}
}
