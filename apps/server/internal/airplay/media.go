package airplay

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

func resourceKey(deviceID, playID string) string { return deviceID + "\x00" + playID }

func (manager *Manager) Prepare(ctx context.Context, device output.Device, track library.Track, playID string) (output.Resource, error) {
	if device.Protocol != output.ProtocolAirPlay || !strings.HasPrefix(device.ID, "airplay:") || playID == "" || len(playID) > 256 || strings.ContainsRune(playID, '\x00') {
		return output.Resource{}, errors.New("airplay: invalid private media request")
	}
	record, err := manager.deviceRecord(device.ID, "Prepare")
	if err != nil {
		return output.Resource{}, err
	}
	format, err := selectAudioFormat(record.endpoint.properties)
	if err != nil {
		return output.Resource{}, actionError("Prepare", err)
	}
	file, current, err := manager.library.Open(ctx, track.ID)
	if err != nil {
		return output.Resource{}, fmt.Errorf("airplay: open library track: %w", err)
	}
	if err := file.Close(); err != nil {
		return output.Resource{}, fmt.Errorf("airplay: close library track: %w", err)
	}
	resource := output.Resource{
		URL:  "airplay://receiver/" + url.PathEscape(device.ID) + "/" + url.PathEscape(playID),
		Mime: format.mime(), Title: current.Title, Artist: current.Artist,
		Album: current.Album, DurationMS: current.DurationMS, Size: current.Size, Seekable: true,
		TrackID: current.ID, PlayID: playID,
	}
	manager.mu.Lock()
	manager.prepared[resourceKey(device.ID, playID)] = preparedResource{deviceID: device.ID, resource: resource, track: current}
	manager.mu.Unlock()
	return resource, nil
}

func (manager *Manager) Revoke(playID string) {
	var stop []*playbackSession
	manager.mu.Lock()
	for key, prepared := range manager.prepared {
		if prepared.resource.PlayID == playID {
			delete(manager.prepared, key)
		}
	}
	for _, session := range manager.sessions {
		if session.playID() == playID {
			stop = append(stop, session)
		}
	}
	manager.mu.Unlock()
	for _, session := range stop {
		session.stopAsync()
	}
}

func (manager *Manager) SetURI(ctx context.Context, id string, resource output.Resource) error {
	record, err := manager.deviceRecord(id, "SetURI")
	if err != nil {
		return err
	}
	if record.device.PairingRequired || record.device.PasswordRequired {
		return output.NewActionError(output.ErrorResponse, "SetURI", 401, errors.New("AirPlay receiver authorization is required"))
	}
	manager.mu.RLock()
	prepared, ok := manager.prepared[resourceKey(id, resource.PlayID)]
	manager.mu.RUnlock()
	if !ok || resource.TrackID == "" || prepared.resource != resource {
		return output.NewActionError(output.ErrorUnsupported, "SetURI", 0, errors.New("private AirPlay resource is invalid or revoked"))
	}
	if err := ctx.Err(); err != nil {
		return classifyContext("SetURI", err)
	}
	session := newPlaybackSession(manager, record, prepared)
	manager.mu.Lock()
	previous := manager.sessions[id]
	manager.sessions[id] = session
	manager.mu.Unlock()
	if previous != nil {
		previous.stopAsync()
	}
	manager.config.Notify("renderers")
	return nil
}

func (manager *Manager) session(id, action string) (*playbackSession, error) {
	if _, err := manager.deviceRecord(id, action); err != nil {
		return nil, err
	}
	manager.mu.RLock()
	session := manager.sessions[id]
	manager.mu.RUnlock()
	if session == nil {
		return nil, output.NewActionError(output.ErrorUnsupported, action, 0, errors.New("no AirPlay resource is selected"))
	}
	return session, nil
}

func (manager *Manager) Play(ctx context.Context, id string) error {
	session, err := manager.session(id, "Play")
	if err != nil {
		return err
	}
	return session.play(ctx)
}

func (manager *Manager) Pause(ctx context.Context, id string) error {
	session, err := manager.session(id, "Pause")
	if err != nil {
		return err
	}
	return session.pause(ctx)
}

func (manager *Manager) Stop(ctx context.Context, id string) error {
	session, err := manager.session(id, "Stop")
	if err != nil {
		return err
	}
	return session.stop(ctx)
}

func (manager *Manager) Seek(ctx context.Context, id string, positionMS int64) error {
	session, err := manager.session(id, "Seek")
	if err != nil {
		return err
	}
	return session.seek(ctx, positionMS)
}

func (manager *Manager) Observe(ctx context.Context, id string) (output.Observation, error) {
	session, err := manager.session(id, "Observe")
	if err != nil {
		return output.Observation{}, err
	}
	if err := ctx.Err(); err != nil {
		return output.Observation{}, classifyContext("Observe", err)
	}
	return session.observe(), nil
}
