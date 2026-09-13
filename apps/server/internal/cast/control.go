package cast

import (
	"context"
	"errors"
	"math"
	"net/netip"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

const (
	mediaCommandPause = int64(1)
	mediaCommandSeek  = int64(2)
)

func (manager *Manager) SetURI(ctx context.Context, id string, resource output.Resource) error {
	record, err := manager.controlRecord(id, "SetURI")
	if err != nil {
		return err
	}
	if err := validateResource(resource, record.endpoint); err != nil {
		return unsupported("SetURI", err)
	}
	record.session.opMu.Lock()
	defer record.session.opMu.Unlock()
	var loaded mediaStatus
	var application receiverApplication
	err = manager.withClient(ctx, record, "SetURI", func(actionContext context.Context, client *client) error {
		current, running, err := client.defaultReceiver(actionContext)
		if err != nil {
			return err
		}
		if !running {
			current, err = client.launchDefaultReceiver(actionContext)
			if err != nil {
				return err
			}
		}
		application = current
		if err := client.connect(actionContext, application.TransportID); err != nil {
			return err
		}
		metadata := map[string]any{"metadataType": 3}
		if resource.Title != "" {
			metadata["title"] = resource.Title
		}
		if resource.Artist != "" {
			metadata["artist"] = resource.Artist
		}
		if resource.Album != "" {
			metadata["albumName"] = resource.Album
		}
		if resource.ArtworkURL != "" {
			metadata["images"] = []map[string]string{{"url": resource.ArtworkURL}}
		}
		media := map[string]any{
			"contentId": resource.URL, "contentType": resource.Mime,
			"streamType": "BUFFERED", "metadata": metadata,
		}
		if resource.DurationMS > 0 {
			media["duration"] = float64(resource.DurationMS) / 1000
		}
		request := map[string]any{
			"type": "LOAD", "media": media, "autoplay": false, "currentTime": 0,
		}
		if resource.PlayID != "" {
			request["customData"] = map[string]string{"jastreamerPlayId": resource.PlayID}
		}
		statuses, err := client.mediaCommand(actionContext, application.TransportID, request)
		if err != nil {
			return err
		}
		status, found := statusForURI(statuses, resource.URL)
		if !found || status.MediaSessionID <= 0 {
			return ErrInvalidResponse
		}
		if state := normalizedPlayerState(status.PlayerState); state == "playing" || state == "unknown" {
			return ErrInvalidResponse
		}
		loaded = status
		return nil
	})
	if err != nil {
		return err
	}
	record.session.mu.Lock()
	record.session.resource = resource
	record.session.mediaSessionID = loaded.MediaSessionID
	record.session.transportID = application.TransportID
	record.session.terminal = output.Observation{}
	record.session.hasTerminal = false
	record.session.appSessionID = application.SessionID
	record.session.lastStatus = loaded
	record.session.hasStatus = true
	record.session.mu.Unlock()
	return nil
}

func (manager *Manager) Play(ctx context.Context, id string) error {
	return manager.controlMedia(ctx, id, "Play", func(status mediaStatus, resource output.Resource) (map[string]any, bool, error) {
		switch normalizedPlayerState(status.PlayerState) {
		case "playing", "transitioning":
			return nil, true, nil
		case "paused":
			return map[string]any{"type": "PLAY"}, false, nil
		default:
			return nil, false, unsupported("Play", errors.New("owned Cast media is not playable"))
		}
	})
}

func (manager *Manager) Pause(ctx context.Context, id string) error {
	return manager.controlMedia(ctx, id, "Pause", func(status mediaStatus, resource output.Resource) (map[string]any, bool, error) {
		if status.SupportedCommands&mediaCommandPause == 0 {
			return nil, false, unsupported("Pause", errors.New("Cast receiver does not support pause for this media"))
		}
		switch normalizedPlayerState(status.PlayerState) {
		case "paused":
			return nil, true, nil
		case "playing", "transitioning":
			return map[string]any{"type": "PAUSE"}, false, nil
		default:
			return nil, false, unsupported("Pause", errors.New("owned Cast media cannot be paused"))
		}
	})
}

func (manager *Manager) Stop(ctx context.Context, id string) error {
	return manager.controlMedia(ctx, id, "Stop", func(status mediaStatus, resource output.Resource) (map[string]any, bool, error) {
		if normalizedPlayerState(status.PlayerState) == "stopped" {
			return nil, true, nil
		}
		return map[string]any{"type": "STOP"}, false, nil
	})
}

func (manager *Manager) Seek(ctx context.Context, id string, positionMS int64) error {
	if positionMS < 0 {
		return unsupported("Seek", ErrInvalidResource)
	}
	return manager.controlMedia(ctx, id, "Seek", func(status mediaStatus, resource output.Resource) (map[string]any, bool, error) {
		if !resource.Seekable || (resource.DurationMS > 0 && positionMS > resource.DurationMS) {
			return nil, false, unsupported("Seek", errors.New("seek position is unsupported for this media"))
		}
		if status.SupportedCommands&mediaCommandSeek == 0 {
			return nil, false, unsupported("Seek", errors.New("Cast receiver does not support seek for this media"))
		}
		return map[string]any{"type": "SEEK", "currentTime": float64(positionMS) / 1000}, false, nil
	})
}

type mediaCommandBuilder func(mediaStatus, output.Resource) (request map[string]any, complete bool, err error)

func (manager *Manager) controlMedia(ctx context.Context, id, action string, build mediaCommandBuilder) error {
	record, err := manager.controlRecord(id, action)
	if err != nil {
		return err
	}
	session := record.session
	session.opMu.Lock()
	defer session.opMu.Unlock()
	resource, mediaSessionID, appSessionID, previous, ok := session.snapshot()
	if !ok {
		return unsupported(action, errors.New("no owned Cast media session"))
	}
	var acknowledged mediaStatus
	var hasAcknowledgement bool
	err = manager.withClient(ctx, record, action, func(actionContext context.Context, client *client) error {
		application, running, err := client.defaultReceiver(actionContext)
		if err != nil {
			return err
		}
		if !running || application.SessionID != appSessionID {
			return unsupported(action, errors.New("the owned Cast application session ended"))
		}
		session.updateTransport(appSessionID, application.TransportID)
		if err := client.connect(actionContext, application.TransportID); err != nil {
			return err
		}
		statuses, err := client.mediaStatus(actionContext, application.TransportID)
		if err != nil {
			return err
		}
		current, found := ownedStatus(statuses, mediaSessionID, resource.URL, previous)
		if !found {
			// A successful empty status query confirms that no media remains.
			// A non-empty result without our session belongs to another owner.
			if action == "Stop" && len(statuses) == 0 {
				return nil
			}
			return unsupported(action, errors.New("the owned Cast media was replaced"))
		}
		request, complete, err := build(current, resource)
		if err != nil {
			return err
		}
		if complete {
			acknowledged, hasAcknowledgement = current, true
			return nil
		}
		request["mediaSessionId"] = mediaSessionID
		responses, err := client.mediaCommand(actionContext, application.TransportID, request)
		if err != nil {
			return err
		}
		response, found := statusForSession(responses, mediaSessionID)
		if !found {
			if action == "Stop" && len(responses) == 0 {
				return nil
			}
			return ErrInvalidResponse
		}
		acknowledged = mergeStatus(response, current)
		if acknowledged.Media.ContentID != resource.URL {
			return ErrInvalidResponse
		}
		switch action {
		case "Play":
			state := normalizedPlayerState(acknowledged.PlayerState)
			if state != "playing" && state != "transitioning" {
				return ErrInvalidResponse
			}
		case "Pause":
			if normalizedPlayerState(acknowledged.PlayerState) != "paused" {
				return ErrInvalidResponse
			}
		case "Stop":
			if normalizedPlayerState(acknowledged.PlayerState) != "stopped" {
				return ErrInvalidResponse
			}
		}
		hasAcknowledgement = true
		return nil
	})
	if err != nil {
		return err
	}
	if hasAcknowledgement {
		session.mu.Lock()
		if session.mediaSessionID == mediaSessionID && session.resource.URL == resource.URL {
			session.lastStatus = acknowledged
			session.hasStatus = true
		}
		session.mu.Unlock()
	}
	return nil
}

func (manager *Manager) Observe(ctx context.Context, id string) (output.Observation, error) {
	record, err := manager.controlRecord(id, "Observe")
	if err != nil {
		return output.Observation{}, err
	}
	session := record.session
	session.opMu.Lock()
	defer session.opMu.Unlock()
	resource, mediaSessionID, appSessionID, previous, ok := session.snapshot()
	if !ok {
		return output.Observation{}, unsupported("Observe", errors.New("no owned Cast media session"))
	}
	observation := output.Observation{ObservedAt: manager.now(), TransportStatus: "OK"}
	var observed mediaStatus
	var hasObserved bool
	err = manager.withClient(ctx, record, "Observe", func(actionContext context.Context, client *client) error {
		application, running, err := client.defaultReceiver(actionContext)
		if err != nil {
			return err
		}
		if !running {
			observation = missingMediaObservation(manager.now())
			return nil
		}
		if err := client.connect(actionContext, application.TransportID); err != nil {
			return err
		}
		if application.SessionID == appSessionID {
			session.updateTransport(appSessionID, application.TransportID)
		}
		statuses, err := client.mediaStatus(actionContext, application.TransportID)
		if err != nil {
			return err
		}
		if len(statuses) == 0 {
			if application.SessionID == appSessionID {
				if terminal, available := session.terminalSnapshot(); available {
					observation = terminal
					return nil
				}
			}
			observation = missingMediaObservation(manager.now())
			return nil
		}
		status := statuses[0]
		for _, candidate := range statuses {
			if candidate.MediaSessionID == mediaSessionID {
				status = candidate
				break
			}
		}
		sameApplication := application.SessionID == appSessionID
		if sameApplication && status.MediaSessionID == mediaSessionID {
			status = mergeStatus(status, previous)
		}
		owned := sameApplication && status.MediaSessionID == mediaSessionID && status.Media.ContentID == resource.URL
		observation = observationFromStatus(status, owned, manager.now())
		if owned {
			observed, hasObserved = status, true
		}
		return nil
	})
	if err != nil {
		return output.Observation{}, err
	}
	if hasObserved {
		session.mu.Lock()
		if session.mediaSessionID == mediaSessionID && session.resource.URL == resource.URL {
			session.lastStatus = observed
			session.hasStatus = true
			if observation.State == "stopped" {
				session.terminal = observation
				session.hasTerminal = true
			} else {
				session.terminal = output.Observation{}
				session.hasTerminal = false
			}
		}
		session.mu.Unlock()
	}
	return observation, nil
}

func (session *playbackSession) snapshot() (output.Resource, int64, string, mediaStatus, bool) {
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.resource, session.mediaSessionID, session.appSessionID, session.lastStatus,
		session.mediaSessionID > 0 && session.appSessionID != "" && session.resource.URL != "" && session.hasStatus
}

func (session *playbackSession) terminalSnapshot() (output.Observation, bool) {
	session.mu.RLock()
	defer session.mu.RUnlock()
	return session.terminal, session.hasTerminal
}

func (session *playbackSession) clearClient(value *client) {
	session.mu.Lock()
	if session.client == value {
		session.client = nil
	}
	session.mu.Unlock()
}

func (session *playbackSession) updateTransport(appSessionID, transportID string) {
	session.mu.Lock()
	if session.appSessionID == appSessionID {
		session.transportID = transportID
	}
	session.mu.Unlock()
}

func (session *playbackSession) closeClient() {
	session.mu.Lock()
	value := session.client
	session.client = nil
	session.mu.Unlock()
	if value != nil {
		value.Close()
	}
}

func (session *playbackSession) handleMediaEvent(source string, statuses []mediaStatus, observedAt time.Time) {
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.mediaSessionID <= 0 || session.resource.URL == "" || source != session.transportID {
		return
	}
	status, found := statusForSession(statuses, session.mediaSessionID)
	if !found {
		if len(statuses) > 0 {
			session.terminal = output.Observation{}
			session.hasTerminal = false
		}
		return
	}
	status = mergeStatus(status, session.lastStatus)
	if status.Media.ContentID != session.resource.URL {
		session.terminal = output.Observation{}
		session.hasTerminal = false
		return
	}
	session.lastStatus = status
	session.hasStatus = true
	observation := observationFromStatus(status, true, observedAt)
	if observation.State == "stopped" {
		session.terminal = observation
		session.hasTerminal = true
	} else {
		session.terminal = output.Observation{}
		session.hasTerminal = false
	}
}

func statusForURI(values []mediaStatus, uri string) (mediaStatus, bool) {
	for _, value := range values {
		if value.Media.ContentID == uri {
			return value, true
		}
	}
	return mediaStatus{}, false
}

func statusForSession(values []mediaStatus, mediaSessionID int64) (mediaStatus, bool) {
	for _, value := range values {
		if value.MediaSessionID == mediaSessionID {
			return value, true
		}
	}
	return mediaStatus{}, false
}

func ownedStatus(values []mediaStatus, mediaSessionID int64, uri string, previous mediaStatus) (mediaStatus, bool) {
	status, ok := statusForSession(values, mediaSessionID)
	if !ok {
		return mediaStatus{}, false
	}
	status = mergeStatus(status, previous)
	return status, status.Media.ContentID == uri
}

func mergeStatus(current, previous mediaStatus) mediaStatus {
	if current.Media.ContentID == "" {
		current.Media.ContentID = previous.Media.ContentID
	}
	if current.Media.ContentType == "" {
		current.Media.ContentType = previous.Media.ContentType
	}
	if current.Media.Duration == nil {
		current.Media.Duration = previous.Media.Duration
	}
	if current.CurrentTime == nil {
		current.CurrentTime = previous.CurrentTime
	}
	return current
}

func observationFromStatus(status mediaStatus, owned bool, now time.Time) output.Observation {
	observation := output.Observation{
		State: normalizedPlayerState(status.PlayerState), URI: status.Media.ContentID,
		HasURI: true, ObservedAt: now, TransportStatus: "OK", CompletionKnown: true,
	}
	if !owned {
		observation.TransportStatus = "ERROR_OCCURRED"
	}
	if status.CurrentTime != nil {
		observation.PositionMS = secondsToMilliseconds(*status.CurrentTime)
		observation.HasPosition = true
	}
	if status.Media.Duration != nil {
		observation.DurationMS = secondsToMilliseconds(*status.Media.Duration)
	}
	if observation.State == "stopped" && owned {
		switch strings.ToUpper(strings.TrimSpace(status.IdleReason)) {
		case "FINISHED":
			observation.CompletionKnown = true
			observation.Completed = true
			if !observation.HasPosition && observation.DurationMS > 0 {
				observation.PositionMS = observation.DurationMS
				observation.HasPosition = true
			}
		case "CANCELLED", "CANCELED", "INTERRUPTED", "ERROR":
			observation.CompletionKnown = true
			observation.TransportStatus = "ERROR_OCCURRED"
		default:
			observation.TransportStatus = "ERROR_OCCURRED"
		}
	}
	return observation
}

func missingMediaObservation(now time.Time) output.Observation {
	return output.Observation{
		State: "stopped", URI: "", HasURI: true, ObservedAt: now,
		TransportStatus: "ERROR_OCCURRED", CompletionKnown: true, Completed: false,
	}
}

func normalizedPlayerState(value string) string {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "PLAYING":
		return "playing"
	case "PAUSED":
		return "paused"
	case "BUFFERING", "LOADING":
		return "transitioning"
	case "IDLE":
		return "stopped"
	default:
		return "unknown"
	}
}

func validateResource(resource output.Resource, target endpoint) error {
	if resource.DurationMS < 0 || resource.Size < 0 || len(resource.URL) > 8192 || len(resource.ArtworkURL) > 8192 ||
		len(resource.Mime) == 0 || len(resource.Mime) > 255 || len(resource.Title) > 4096 || len(resource.Artist) > 4096 || len(resource.Album) > 4096 {
		return ErrInvalidResource
	}
	for _, value := range []string{resource.URL, resource.ArtworkURL, resource.Mime, resource.Title, resource.Artist, resource.Album} {
		if !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
			return ErrInvalidResource
		}
	}
	if strings.ContainsAny(resource.Mime, "\r\n") || !validMediaURL(resource.URL, target) {
		return ErrInvalidResource
	}
	if resource.ArtworkURL != "" && !validMediaURL(resource.ArtworkURL, target) {
		return ErrInvalidResource
	}
	return nil
}

func validMediaURL(raw string, target endpoint) bool {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" || parsed.Path == "" {
		return false
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	return err == nil && address.Is4() && allowedLocalAddress(address) && target.network.Contains(address)
}

func finiteNonnegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func secondsToMilliseconds(value float64) int64 {
	if value <= 0 {
		return 0
	}
	maximum := float64(math.MaxInt64) / 1000
	if value >= maximum {
		return math.MaxInt64
	}
	return int64(math.Round(value * 1000))
}
