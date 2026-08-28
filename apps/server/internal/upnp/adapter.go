package upnp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

const defaultActionTimeout = 5 * time.Second

type AdapterConfig struct {
	Device        K17Device
	RendererID    RendererID
	ZoneID        ZoneID
	HTTPClient    *http.Client
	ActionTimeout time.Duration
	Clock         func() time.Time
}

type K17Adapter struct {
	device        K17Device
	rendererID    RendererID
	zoneID        ZoneID
	httpClient    *http.Client
	actionTimeout time.Duration
	now           func() time.Time
	mu            sync.RWMutex
	expectedURI   string
}

type Observation struct {
	State ObservedState
	Error error
}

func NewK17Adapter(config AdapterConfig) (*K17Adapter, error) {
	if config.Device.ID == "" || config.RendererID == "" || config.ZoneID == "" || config.HTTPClient == nil || config.Device.AVTransportControlURL == "" {
		return nil, ErrInvalidConfig
	}
	if config.ActionTimeout < 0 {
		return nil, ErrInvalidConfig
	}
	if config.ActionTimeout == 0 {
		config.ActionTimeout = defaultActionTimeout
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	return &K17Adapter{device: config.Device, rendererID: config.RendererID, zoneID: config.ZoneID, httpClient: config.HTTPClient, actionTimeout: config.ActionTimeout, now: config.Clock}, nil
}

func (adapter *K17Adapter) RendererID() playback.RendererID { return adapter.rendererID }
func (adapter *K17Adapter) ZoneID() playback.ZoneID         { return adapter.zoneID }

func (adapter *K17Adapter) SetAVTransportURI(ctx context.Context, resource playback.MediaResource) error {
	if !adapter.device.Supports(ActionSetAVTransportURI) {
		return ErrActionUnavailable
	}
	if err := adapter.validateMediaURL(resource.URL); err != nil {
		return err
	}
	metadata, err := didlMetadata(resource)
	if err != nil {
		return err
	}
	if err := adapter.action(ctx, ActionSetAVTransportURI, map[string]string{"InstanceID": "0", "CurrentURI": resource.URL, "CurrentURIMetaData": metadata}); err != nil {
		return err
	}
	adapter.mu.Lock()
	adapter.expectedURI = resource.URL
	adapter.mu.Unlock()
	return nil
}

func (adapter *K17Adapter) Play(ctx context.Context) error {
	return adapter.supportedAction(ctx, ActionPlay, map[string]string{"InstanceID": "0", "Speed": "1"})
}

func (adapter *K17Adapter) Pause(ctx context.Context) error {
	return adapter.supportedAction(ctx, ActionPause, map[string]string{"InstanceID": "0"})
}

func (adapter *K17Adapter) Stop(ctx context.Context) error {
	return adapter.supportedAction(ctx, ActionStop, map[string]string{"InstanceID": "0"})
}

func (adapter *K17Adapter) Seek(ctx context.Context, position time.Duration) error {
	if position < 0 {
		return ErrInvalidConfig
	}
	hours := int64(position / time.Hour)
	minutes := int64(position/time.Minute) % 60
	seconds := int64(position/time.Second) % 60
	target := fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	return adapter.supportedAction(ctx, ActionSeek, map[string]string{"InstanceID": "0", "Unit": "REL_TIME", "Target": target})
}

func (adapter *K17Adapter) supportedAction(ctx context.Context, action Action, arguments map[string]string) error {
	if !adapter.device.Supports(action) {
		return ErrActionUnavailable
	}
	return adapter.action(ctx, action, arguments)
}

func (adapter *K17Adapter) action(ctx context.Context, action Action, arguments map[string]string) error {
	actionContext, cancel := context.WithTimeout(ctx, adapter.actionTimeout)
	defer cancel()
	_, err := executeSOAP(actionContext, soapRequest{URL: adapter.device.AVTransportControlURL, Service: avTransportService, Action: string(action), Arguments: arguments, HTTPClient: adapter.httpClient})
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &DiagnosticError{Kind: DiagnosticTimeout, Action: action, Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return &DiagnosticError{Kind: DiagnosticCancelled, Action: action, Cause: err}
	}
	var fault *SOAPFault
	if errors.As(err, &fault) {
		return fault
	}
	return &DiagnosticError{Kind: DiagnosticTransport, Action: action, Cause: err}
}

func (adapter *K17Adapter) validateMediaURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" || len(parsed.Path) <= len("/media/v1/") || parsed.Path[:len("/media/v1/")] != "/media/v1/" {
		return ErrOffSubnetURL
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	if err != nil {
		return ErrOffSubnetURL
	}
	for _, network := range adapter.device.networks {
		if network.Subnet.Contains(address) {
			return nil
		}
	}
	return ErrOffSubnetURL
}

func (adapter *K17Adapter) Observe(ctx context.Context) (ObservedState, error) {
	if _, ok := adapter.device.queryActions["GetTransportInfo"]; !ok {
		return ObservedState{}, ErrActionUnavailable
	}
	if _, ok := adapter.device.queryActions["GetPositionInfo"]; !ok {
		return ObservedState{}, ErrActionUnavailable
	}
	transportData, err := adapter.query(ctx, "GetTransportInfo")
	if err != nil {
		return ObservedState{}, err
	}
	positionData, err := adapter.query(ctx, "GetPositionInfo")
	if err != nil {
		return ObservedState{}, err
	}
	var transport struct {
		State string `xml:"Body>GetTransportInfoResponse>CurrentTransportState"`
	}
	var position struct {
		Relative string `xml:"Body>GetPositionInfoResponse>RelTime"`
		TrackURI string `xml:"Body>GetPositionInfoResponse>TrackURI"`
	}
	if err := decodeBoundedXML(bytes.NewReader(transportData), &transport); err != nil {
		return ObservedState{}, err
	}
	if err := decodeBoundedXML(bytes.NewReader(positionData), &position); err != nil {
		return ObservedState{}, err
	}
	duration, err := parseDuration(position.Relative)
	if err != nil {
		return ObservedState{}, err
	}
	state := TransportState(transport.State)
	switch state {
	case TransportStopped, TransportPlaying, TransportPaused, TransportTransition:
	case "PAUSED":
		state = TransportPaused
	default:
		state = TransportUnknown
	}
	adapter.mu.RLock()
	expectedURI := adapter.expectedURI
	adapter.mu.RUnlock()
	return ObservedState{
		RendererID: adapter.rendererID, ZoneID: adapter.zoneID, Transport: state, Position: duration,
		CurrentURI: position.TrackURI, Owned: expectedURI != "" && position.TrackURI == expectedURI, ObservedAt: adapter.now(),
	}, nil
}

func (adapter *K17Adapter) query(ctx context.Context, action string) ([]byte, error) {
	queryContext, cancel := context.WithTimeout(ctx, adapter.actionTimeout)
	defer cancel()
	data, err := executeSOAP(queryContext, soapRequest{URL: adapter.device.AVTransportControlURL, Service: avTransportService, Action: action, Arguments: map[string]string{"InstanceID": strconv.Itoa(0)}, HTTPClient: adapter.httpClient})
	if err != nil {
		return nil, &DiagnosticError{Kind: DiagnosticTransport, Action: Action(action), Cause: err}
	}
	return data, nil
}

func (adapter *K17Adapter) ObserveStream(ctx context.Context, interval time.Duration) <-chan Observation {
	observations := make(chan Observation)
	go func() {
		defer close(observations)
		if interval <= 0 {
			select {
			case observations <- Observation{Error: ErrInvalidConfig}:
			case <-ctx.Done():
			}
			return
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			state, err := adapter.Observe(ctx)
			select {
			case observations <- Observation{State: state, Error: err}:
			case <-ctx.Done():
				return
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	return observations
}

var _ playback.K17PlaybackAdapter = (*K17Adapter)(nil)
