package browseroutput

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

const (
	leaseDuration    = 15 * time.Second
	pollAfter        = 250 * time.Millisecond
	maximumDevices   = 32
	maximumMIMETypes = 24
	maximumMediaMS   = int64((7 * 24 * time.Hour) / time.Millisecond)
)

type pendingCommand struct {
	command Command
	result  chan error
}

type registration struct {
	id           string
	token        string
	address      string
	name         string
	protocolInfo []string
	expiresAt    time.Time
	lastSeen     time.Time
	nextSequence uint64
	cancelBefore uint64
	pending      *pendingCommand
	resource     *output.Resource
	resourceSeq  uint64
	observation  output.Observation
}

type Service struct {
	media  output.Media
	notify func(string)
	now    func() time.Time

	mu      sync.Mutex
	devices map[string]*registration
	closed  bool
}

var _ output.Controller = (*Service)(nil)
var _ output.Media = (*Service)(nil)

func New(media output.Media, notify func(string)) (*Service, error) {
	if media == nil {
		return nil, errors.New("browser output: media service is required")
	}
	if notify == nil {
		notify = func(string) {}
	}
	return &Service{media: media, notify: notify, now: time.Now, devices: make(map[string]*registration)}, nil
}

func (service *Service) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			service.shutdown()
			return nil
		case now := <-ticker.C:
			service.expire(now)
		}
	}
}

func (service *Service) Refresh(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return context.Cause(ctx)
}

func (service *Service) Devices() []output.Device {
	service.mu.Lock()
	defer service.mu.Unlock()
	now := service.now()
	items := make([]output.Device, 0, len(service.devices))
	for id, value := range service.devices {
		if !now.Before(value.expiresAt) {
			continue
		}
		items = append(items, service.deviceLocked(id, value))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items
}

func (service *Service) Device(id string) (output.Device, bool) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value := service.devices[id]
	if value == nil || !service.now().Before(value.expiresAt) {
		return output.Device{}, false
	}
	return service.deviceLocked(id, value), true
}

func (service *Service) Register(address, name string, protocolInfo []string) (Registration, error) {
	address = strings.TrimSpace(address)
	name = strings.TrimSpace(name)
	if address == "" || name == "" || len(name) > 80 {
		return Registration{}, ErrInvalidRequest
	}
	info, err := validateProtocolInfo(protocolInfo)
	if err != nil {
		return Registration{}, err
	}
	idToken, err := randomToken(18)
	if err != nil {
		return Registration{}, err
	}
	ownerToken, err := randomToken(32)
	if err != nil {
		return Registration{}, err
	}
	now := service.now().UTC()
	value := &registration{
		id: "browser:" + idToken, token: ownerToken, address: address, name: name,
		protocolInfo: info, expiresAt: now.Add(leaseDuration), lastSeen: now,
		observation: output.Observation{State: "stopped", HasURI: true, ObservedAt: now, TransportStatus: "OK"},
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return Registration{}, ErrNotFound
	}
	service.removeExpiredLocked(now)
	if len(service.devices) >= maximumDevices {
		service.mu.Unlock()
		return Registration{}, ErrCapacity
	}
	service.devices[value.id] = value
	device := service.deviceLocked(value.id, value)
	service.mu.Unlock()
	service.notify("renderers")
	return Registration{ID: value.id, OwnerToken: ownerToken, LeaseExpiresAt: now.Add(leaseDuration), LeaseDurationMS: leaseDuration.Milliseconds(), PollAfterMS: pollAfter.Milliseconds(), Device: device}, nil
}

// Rename changes only the display name of an existing, owner-authenticated output.
func (service *Service) Rename(id, token, address, name string) (output.Device, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return output.Device{}, ErrInvalidRequest
	}
	service.mu.Lock()
	value, err := service.ownerLocked(id, token, address)
	if err != nil {
		service.mu.Unlock()
		return output.Device{}, err
	}
	changed := value.name != name
	value.name = name
	device := service.deviceLocked(id, value)
	service.mu.Unlock()
	if changed {
		service.notify("renderers")
	}
	return device, nil
}

func (service *Service) Renew(id, token, address string) (Lease, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.ownerLocked(id, token, address)
	if err != nil {
		return Lease{}, err
	}
	service.renewLocked(value)
	return service.leaseLocked(value), nil
}

func (service *Service) Poll(id, token, address string) (CommandPoll, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.ownerLocked(id, token, address)
	if err != nil {
		return CommandPoll{}, err
	}
	service.renewLocked(value)
	result := CommandPoll{
		LeaseExpiresAt: value.expiresAt, LeaseDurationMS: leaseDuration.Milliseconds(), CancelBeforeSequence: value.cancelBefore,
		PollAfterMS: pollAfter.Milliseconds(),
	}
	if value.pending != nil {
		copy := value.pending.command
		if copy.Resource != nil {
			resource := *copy.Resource
			copy.Resource = &resource
		}
		result.Command = &copy
	}
	return result, nil
}

func (service *Service) Disconnect(id, token, address string) error {
	service.mu.Lock()
	value, err := service.ownerLocked(id, token, address)
	if err == nil {
		delete(service.devices, id)
		service.cancelPendingLocked(value, output.NewActionError(output.ErrorCancelled, "BrowserDisconnect", 0, errors.New("browser output disconnected")))
	}
	service.mu.Unlock()
	if err == nil {
		service.notify("renderers")
	}
	return err
}

func (service *Service) Report(id, token, address string, report Report) error {
	service.mu.Lock()
	defer service.mu.Unlock()
	value, err := service.ownerLocked(id, token, address)
	if err != nil {
		return err
	}
	service.renewLocked(value)
	if report.Result == "" {
		if report.ErrorCode != "" || report.Observation == nil {
			return ErrInvalidRequest
		}
		if value.observation.PlayID == report.Observation.PlayID &&
			report.Sequence < value.observation.CommandSequence {
			return ErrStaleReport
		}
		if (value.observation.Completed || value.observation.TransportStatus == "ERROR_OCCURRED") &&
			value.observation.PlayID == report.Observation.PlayID &&
			report.Sequence <= value.observation.CommandSequence {
			return ErrStaleReport
		}
		observation, observationErr := service.observationLocked(value, report.Sequence, report.Observation, nil)
		if observationErr != nil {
			return observationErr
		}
		value.observation = observation
		return nil
	}
	if report.Result != "succeeded" && report.Result != "failed" {
		return ErrInvalidRequest
	}
	pending := value.pending
	if pending == nil || report.Sequence != pending.command.Sequence {
		return ErrStaleReport
	}
	if report.Result == "failed" {
		if !validReportError(report.ErrorCode) {
			return ErrInvalidRequest
		}
		if report.Observation != nil {
			if observation, observationErr := service.observationLocked(value, report.Sequence, report.Observation, pending); observationErr == nil {
				value.observation = observation
			}
		}
		actionErr := reportActionError(pending.command.Action, report.ErrorCode)
		value.pending = nil
		pending.result <- actionErr
		return nil
	}
	if report.ErrorCode != "" || report.Observation == nil {
		return ErrInvalidRequest
	}
	observation, err := service.observationLocked(value, report.Sequence, report.Observation, pending)
	if err != nil || !matchesSuccessfulAction(pending.command.Action, report.Observation.Event) {
		if err != nil {
			return err
		}
		return ErrInvalidRequest
	}
	switch pending.command.Action {
	case "set_uri":
		resource := *pending.command.Resource
		value.resource = &resource
		value.resourceSeq = report.Sequence
	case "stop":
		value.resource = nil
		value.resourceSeq = report.Sequence
	}
	value.observation = observation
	value.pending = nil
	pending.result <- nil
	return nil
}

func (service *Service) SetURI(ctx context.Context, id string, resource output.Resource) error {
	if resource.URL == "" || resource.PlayID == "" || resource.TrackID == "" {
		return output.NewActionError(output.ErrorUnsupported, "SetURI", 0, ErrInvalidRequest)
	}
	copy := resource
	return service.dispatch(ctx, id, Command{Action: "set_uri", PlayID: resource.PlayID, Resource: &copy})
}

func (service *Service) Play(ctx context.Context, id string) error {
	return service.dispatch(ctx, id, Command{Action: "play"})
}

func (service *Service) Pause(ctx context.Context, id string) error {
	return service.dispatch(ctx, id, Command{Action: "pause"})
}

func (service *Service) Stop(ctx context.Context, id string) error {
	return service.dispatch(ctx, id, Command{Action: "stop"})
}

func (service *Service) Seek(ctx context.Context, id string, positionMS int64) error {
	if positionMS < 0 || positionMS > maximumMediaMS {
		return output.NewActionError(output.ErrorUnsupported, "Seek", 0, ErrInvalidRequest)
	}
	return service.dispatch(ctx, id, Command{Action: "seek", PositionMS: positionMS})
}

func (service *Service) Observe(ctx context.Context, id string) (output.Observation, error) {
	if err := context.Cause(ctx); err != nil {
		return output.Observation{}, err
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	value := service.devices[id]
	if value == nil || !service.now().Before(value.expiresAt) {
		return output.Observation{}, output.NewActionError(output.ErrorTransport, "Observe", 0, ErrNotFound)
	}
	if value.resource != nil && !value.observation.Completed && value.observation.TransportStatus != "ERROR_OCCURRED" &&
		service.now().Sub(value.observation.ObservedAt) >= leaseDuration {
		return output.Observation{}, output.NewActionError(output.ErrorTimeout, "Observe", 0, errors.New("browser media observation expired"))
	}
	return value.observation, nil
}

func (service *Service) Prepare(ctx context.Context, device output.Device, track library.Track, playID string) (output.Resource, error) {
	if device.Protocol != output.ProtocolBrowser {
		return output.Resource{}, output.ErrNotFound
	}
	return service.media.Prepare(ctx, device, track, playID)
}

func (service *Service) Revoke(playID string) {
	service.media.Revoke(playID)
	service.mu.Lock()
	for _, value := range service.devices {
		if value.resource == nil || value.resource.PlayID != playID {
			continue
		}
		if value.nextSequence > value.cancelBefore {
			value.cancelBefore = value.nextSequence
		}
		service.cancelPendingLocked(value, output.NewActionError(output.ErrorCancelled, "Revoke", 0, ErrStaleReport))
		value.resource = nil
		value.resourceSeq = value.nextSequence
		value.observation = output.Observation{
			State: "stopped", HasURI: true, ObservedAt: service.now().UTC(), TransportStatus: "OK",
		}
	}
	service.mu.Unlock()
}

func (service *Service) dispatch(ctx context.Context, id string, command Command) error {
	if ctx == nil {
		ctx = context.Background()
	}
	service.mu.Lock()
	value := service.devices[id]
	if value == nil || !service.now().Before(value.expiresAt) {
		service.mu.Unlock()
		return output.NewActionError(output.ErrorTransport, command.Action, 0, ErrNotFound)
	}
	if value.pending != nil {
		service.mu.Unlock()
		return output.NewActionError(output.ErrorFault, command.Action, 0, ErrConflict)
	}
	if command.Action != "set_uri" {
		if value.resource == nil {
			service.mu.Unlock()
			if command.Action == "stop" {
				return nil
			}
			return output.NewActionError(output.ErrorUnsupported, command.Action, 0, ErrInvalidRequest)
		}
		command.PlayID = value.resource.PlayID
	}
	value.nextSequence++
	command.Sequence = value.nextSequence
	pending := &pendingCommand{command: command, result: make(chan error, 1)}
	value.pending = pending
	service.mu.Unlock()

	select {
	case err := <-pending.result:
		return err
	case <-ctx.Done():
		service.mu.Lock()
		if current := service.devices[id]; current == value && current.pending == pending {
			current.pending = nil
			if command.Sequence > current.cancelBefore {
				current.cancelBefore = command.Sequence
			}
		}
		service.mu.Unlock()
		return output.NewActionError(output.ErrorCancelled, command.Action, 0, context.Cause(ctx))
	}
}

func (service *Service) observationLocked(value *registration, sequence uint64, report *ObservationReport, pending *pendingCommand) (output.Observation, error) {
	if sequence <= value.cancelBefore {
		return output.Observation{}, ErrStaleReport
	}
	if report == nil || sequence == 0 || sequence > value.nextSequence || report.PositionMS < 0 || report.PositionMS > maximumMediaMS || report.DurationMS < 0 || report.DurationMS > maximumMediaMS {
		return output.Observation{}, ErrInvalidRequest
	}
	resource := value.resource
	minimumSequence := value.resourceSeq
	if pending != nil && pending.command.Action == "set_uri" {
		resource = pending.command.Resource
		minimumSequence = pending.command.Sequence
	}
	if pending != nil && pending.command.Action == "stop" {
		resource = value.resource
	}
	if resource == nil || report.PlayID != resource.PlayID || sequence < minimumSequence {
		return output.Observation{}, ErrStaleReport
	}
	state := ""
	transport := "OK"
	completionKnown := true
	completed := false
	uri := resource.URL
	hasURI := true
	switch report.Event {
	case "loaded", "pause":
		state = "paused"
	case "playing":
		state = "playing"
	case "seeked", "timeupdate":
		if report.State != "playing" && report.State != "paused" {
			return output.Observation{}, ErrInvalidRequest
		}
		state = report.State
	case "ended":
		state, completionKnown, completed = "stopped", true, true
	case "error":
		state, transport = "stopped", "ERROR_OCCURRED"
	case "stopped":
		if pending == nil || pending.command.Action != "stop" {
			return output.Observation{}, ErrInvalidRequest
		}
		state, uri = "stopped", ""
	default:
		return output.Observation{}, ErrInvalidRequest
	}
	return output.Observation{
		State: state, PositionMS: report.PositionMS, DurationMS: report.DurationMS,
		URI: uri, HasURI: hasURI, HasPosition: report.HasPosition, ObservedAt: service.now().UTC(),
		TransportStatus: transport, CompletionKnown: completionKnown, Completed: completed,
		PlayID: resource.PlayID, CommandSequence: sequence,
	}, nil
}

func matchesSuccessfulAction(action, event string) bool {
	switch action {
	case "set_uri":
		return event == "loaded"
	case "play":
		return event == "playing"
	case "pause":
		return event == "pause"
	case "stop":
		return event == "stopped"
	case "seek":
		return event == "seeked"
	default:
		return false
	}
}

func validReportError(code string) bool {
	return code == "media_unsupported" || code == "media_error" || code == "action_failed"
}

func reportActionError(action, code string) error {
	kind := output.ErrorFault
	if code == "media_unsupported" {
		kind = output.ErrorUnsupported
	}
	return output.NewActionError(kind, action, 0, errors.New("browser output: "+code))
}

func (service *Service) ownerLocked(id, token, address string) (*registration, error) {
	value := service.devices[id]
	if value == nil || !service.now().Before(value.expiresAt) {
		return nil, ErrNotFound
	}
	if address != value.address || !sameToken(token, value.token) {
		return nil, ErrUnauthorized
	}
	return value, nil
}

func (service *Service) renewLocked(value *registration) {
	now := service.now().UTC()
	value.lastSeen = now
	value.expiresAt = now.Add(leaseDuration)
}

func (service *Service) leaseLocked(value *registration) Lease {
	return Lease{LeaseExpiresAt: value.expiresAt, LeaseDurationMS: leaseDuration.Milliseconds(), CancelBeforeSequence: value.cancelBefore}
}

func (service *Service) deviceLocked(id string, value *registration) output.Device {
	return output.Device{
		ID: id, Name: value.name, Manufacturer: "JaStreamer", Model: "Web browser",
		Address: value.address, Online: true, LastSeen: value.lastSeen.UTC().Format(time.RFC3339Nano),
		Capabilities: output.Capabilities{Play: true, Pause: true, Stop: true, Seek: true},
		ProtocolInfo: append([]string(nil), value.protocolInfo...), Protocol: output.ProtocolBrowser,
	}
}

func (service *Service) expire(now time.Time) {
	service.mu.Lock()
	changed := service.removeExpiredLocked(now)
	service.mu.Unlock()
	if changed {
		service.notify("renderers")
	}
}

func (service *Service) removeExpiredLocked(now time.Time) bool {
	changed := false
	for id, value := range service.devices {
		if now.Before(value.expiresAt) {
			continue
		}
		delete(service.devices, id)
		service.cancelPendingLocked(value, output.NewActionError(output.ErrorTransport, "BrowserLease", 0, ErrNotFound))
		changed = true
	}
	return changed
}

func (service *Service) cancelPendingLocked(value *registration, err error) {
	if value.pending == nil {
		return
	}
	if value.pending.command.Sequence > value.cancelBefore {
		value.cancelBefore = value.pending.command.Sequence
	}
	value.pending.result <- err
	value.pending = nil
}

func (service *Service) shutdown() {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return
	}
	service.closed = true
	for id, value := range service.devices {
		delete(service.devices, id)
		service.cancelPendingLocked(value, output.NewActionError(output.ErrorCancelled, "BrowserShutdown", 0, context.Canceled))
	}
	service.mu.Unlock()
}

func validateProtocolInfo(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maximumMIMETypes {
		return nil, ErrInvalidRequest
	}
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, raw := range values {
		if len(raw) > 128 {
			return nil, ErrInvalidRequest
		}
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(raw))
		if err != nil || !strings.HasPrefix(strings.ToLower(mediaType), "audio/") {
			return nil, ErrInvalidRequest
		}
		canonical := mime.FormatMediaType(strings.ToLower(mediaType), parameters)
		if canonical == "" || seen[canonical] {
			continue
		}
		seen[canonical] = true
		result = append(result, canonical)
	}
	if len(result) == 0 {
		return nil, ErrInvalidRequest
	}
	sort.Strings(result)
	return result, nil
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("browser output: create credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func sameToken(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
