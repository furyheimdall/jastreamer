package raat

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type preparedMedia struct {
	deviceID   string
	resource   output.Resource
	track      library.Track
	positionMS int64
}

type Manager struct {
	config    Config
	library   *library.Service
	opMu      sync.Mutex
	refreshMu sync.Mutex
	mu        sync.Mutex
	devices   map[string]discoveredDevice
	prepared  map[string]preparedMedia
	selected  map[string]preparedMedia
	sessions  map[string]*session
	closed    bool
}

var _ output.Controller = (*Manager)(nil)
var _ output.Media = (*Manager)(nil)

func (m *Manager) Run(ctx context.Context) error {
	defer m.shutdown()
	_ = m.Refresh(ctx)
	ticker := time.NewTicker(m.discoveryInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = m.Refresh(ctx)
		}
	}
}

func (m *Manager) Refresh(ctx context.Context) error {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	found, err := discover(ctx, m.config.Interfaces)
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("raat: manager closed")
	}
	for _, record := range found {
		m.devices[record.device.ID] = record
	}
	now := time.Now()
	for id, record := range m.devices {
		seen, _ := time.Parse(time.RFC3339Nano, record.device.LastSeen)
		record.device.Online = now.Sub(seen) <= m.deviceTTL()
		m.devices[id] = record
	}
	m.mu.Unlock()
	return err
}

func (m *Manager) Devices() []output.Device {
	m.mu.Lock()
	defer m.mu.Unlock()
	devices := make([]output.Device, 0, len(m.devices))
	for _, record := range m.devices {
		d := record.device
		d.MediaAddresses = append([]string(nil), d.MediaAddresses...)
		d.ProtocolInfo = append([]string(nil), d.ProtocolInfo...)
		seen, _ := time.Parse(time.RFC3339Nano, d.LastSeen)
		d.Online = d.Online && time.Since(seen) <= m.deviceTTL()
		devices = append(devices, d)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	return devices
}
func (m *Manager) Device(id string) (output.Device, bool) {
	for _, d := range m.Devices() {
		if d.ID == id {
			return d, true
		}
	}
	return output.Device{}, false
}

func (m *Manager) discoveryInterval() time.Duration {
	if m.config.DiscoveryInterval > 0 {
		return m.config.DiscoveryInterval
	}
	return 30 * time.Second
}
func (m *Manager) deviceTTL() time.Duration { return max(90*time.Second, 3*m.discoveryInterval()) }
func (m *Manager) Prepare(ctx context.Context, device output.Device, track library.Track, playID string) (output.Resource, error) {
	if device.Protocol != output.ProtocolRAAT || playID == "" || len(playID) > 256 || track.ID == "" {
		return output.Resource{}, errors.New("raat: invalid private media request")
	}
	if _, ok := m.Device(device.ID); !ok {
		return output.Resource{}, errors.New("raat: unknown output")
	}
	file, info, err := m.library.OpenAudio(ctx, track.ID)
	if err != nil {
		return output.Resource{}, err
	}
	_ = file.Close()
	resource := output.Resource{URL: "raat://" + uuid.NewString(), Mime: info.Track.Mime, Title: info.Track.Title, Artist: info.Track.Artist, Album: info.Track.Album, DurationMS: info.Track.DurationMS, Size: info.Track.Size, Seekable: true, TrackID: info.Track.ID, PlayID: playID}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return output.Resource{}, errors.New("raat: manager closed")
	}
	m.prepared[resource.URL] = preparedMedia{deviceID: device.ID, resource: resource, track: info.Track}
	return resource, nil
}

func sameResource(a, b output.Resource) bool {
	return a.URL == b.URL && a.PlayID == b.PlayID && a.TrackID == b.TrackID && a.Mime == b.Mime && a.Title == b.Title && a.Artist == b.Artist && a.Album == b.Album && a.ArtworkURL == b.ArtworkURL && a.DurationMS == b.DurationMS && a.Size == b.Size && a.Seekable == b.Seekable && a.Transformed == nil && b.Transformed == nil
}
func sameTrack(a, b library.Track) bool {
	return a.ID == b.ID && a.Size == b.Size && a.ModifiedAt == b.ModifiedAt && a.RootID == b.RootID && a.Path == b.Path
}

func (m *Manager) SetURI(ctx context.Context, id string, resource output.Resource) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	prepared, ok := m.prepared[resource.URL]
	old := m.sessions[id]
	if m.closed || !ok || prepared.deviceID != id || !sameResource(prepared.resource, resource) {
		m.mu.Unlock()
		return errors.New("raat: resource was not prepared for output")
	}
	m.selected[id] = prepared
	delete(m.sessions, id)
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	return nil
}

func (m *Manager) Play(ctx context.Context, id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	old := m.sessions[id]
	position := m.selected[id].positionMS
	m.mu.Unlock()
	if old != nil {
		old.mu.Lock()
		state := old.snapshot.state
		position = old.positionLocked()
		old.mu.Unlock()
		if state == 2 {
			return nil
		}
		if state != 3 {
			position = 0
		}
	}
	return m.start(ctx, id, position)
}

func (m *Manager) start(ctx context.Context, id string, position int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	record, exists := m.devices[id]
	prepared, ready := m.selected[id]
	old := m.sessions[id]
	_, valid := m.prepared[prepared.resource.URL]
	if m.closed || !exists || !ready || !valid {
		m.mu.Unlock()
		return errors.New("raat: no prepared media for output")
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	if old != nil {
		old.close()
	}
	file, info, err := m.library.OpenAudio(ctx, prepared.track.ID)
	if err != nil {
		return err
	}
	if !sameTrack(prepared.track, info.Track) {
		_ = file.Close()
		return errors.New("raat: prepared media changed")
	}
	if position < 0 || (info.Track.DurationMS > 0 && position >= info.Track.DurationMS) {
		_ = file.Close()
		return errors.New("raat: seek outside media")
	}
	s, err := newSession(ctx, m, record, prepared, file, info, position)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	s.active.Store(true)
	s.startWorkers()
	return nil
}

func (m *Manager) Pause(ctx context.Context, id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	s := m.getSession(id)
	if s == nil {
		return errors.New("raat: no active session")
	}
	return s.pause(ctx)
}
func (m *Manager) Stop(ctx context.Context, id string) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	s := m.getSession(id)
	if s == nil {
		return nil
	}
	return s.stop(ctx)
}
func (m *Manager) Seek(ctx context.Context, id string, position int64) error {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	prepared, ok := m.selected[id]
	_, valid := m.prepared[prepared.resource.URL]
	s := m.sessions[id]
	if !ok || !valid {
		m.mu.Unlock()
		return errors.New("raat: no prepared media for output")
	}
	if position < 0 || (prepared.resource.DurationMS > 0 && position >= prepared.resource.DurationMS) {
		m.mu.Unlock()
		return errors.New("raat: seek outside media")
	}
	if s == nil {
		prepared.positionMS = position
		m.selected[id] = prepared
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()
	return s.seek(ctx, prepared, position)
}
func (m *Manager) getSession(id string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}
func (m *Manager) Observe(ctx context.Context, id string) (output.Observation, error) {
	if err := ctx.Err(); err != nil {
		return output.Observation{}, err
	}
	s := m.getSession(id)
	if s == nil {
		return output.Observation{State: "STOPPED", ObservedAt: time.Now()}, nil
	}
	return s.observation(), nil
}

func (m *Manager) Revoke(playID string) {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	var affected []*session
	for key, value := range m.prepared {
		if value.resource.PlayID == playID {
			delete(m.prepared, key)
		}
	}
	for id, value := range m.selected {
		if value.resource.PlayID == playID {
			delete(m.selected, id)
			if s := m.sessions[id]; s != nil {
				affected = append(affected, s)
			}
		}
	}
	m.mu.Unlock()
	for _, s := range affected {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = s.stop(ctx)
		cancel()
		s.mu.Lock()
		if s.resource.PlayID == playID {
			s.resource.PlayID = ""
			s.known = false
			s.generation.Add(1)
		}
		s.mu.Unlock()
	}
}
func (m *Manager) shutdown() {
	m.opMu.Lock()
	defer m.opMu.Unlock()
	m.mu.Lock()
	m.closed = true
	sessions := m.sessions
	m.sessions = make(map[string]*session)
	m.prepared = make(map[string]preparedMedia)
	m.selected = make(map[string]preparedMedia)
	m.mu.Unlock()
	for _, s := range sessions {
		s.close()
	}
}
