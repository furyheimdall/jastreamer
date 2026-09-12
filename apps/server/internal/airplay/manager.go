package airplay

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

const (
	helperProtocolVersion = 1
	helperPyatvVersion    = "0.18.0"
)

type audioFormat struct {
	sampleRate int
	channels   int
	sampleSize int
}

var supportedAudioFormat = audioFormat{sampleRate: 44100, channels: 2, sampleSize: 16}

func selectAudioFormat(properties map[string]string) (audioFormat, error) {
	format := supportedAudioFormat
	for _, field := range []struct {
		key      string
		expected int
	}{
		{key: "sr", expected: format.sampleRate},
		{key: "ch", expected: format.channels},
		{key: "ss", expected: format.sampleSize},
	} {
		raw, advertised := properties[field.key]
		if !advertised {
			continue
		}
		value, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil {
			return audioFormat{}, helperFailure{kind: "unsupported", message: fmt.Sprintf("AirPlay receiver advertised malformed %s audio format", field.key)}
		}
		if value != field.expected {
			return audioFormat{}, helperFailure{kind: "unsupported", message: fmt.Sprintf("AirPlay receiver %s=%d is incompatible with the supported 44100 Hz, stereo, 16-bit format", field.key, value)}
		}
	}
	return format, nil
}

func (format audioFormat) mime() string {
	return fmt.Sprintf("audio/L%d;rate=%d;channels=%d", format.sampleSize, format.sampleRate, format.channels)
}

func (format audioFormat) normalizedProperties(properties map[string]string) map[string]string {
	result := cloneProperties(properties)
	result["sr"] = strconv.Itoa(format.sampleRate)
	result["ch"] = strconv.Itoa(format.channels)
	result["ss"] = strconv.Itoa(format.sampleSize)
	return result
}

type endpointConfig struct {
	address          netip.Addr
	localAddress     netip.Addr
	port             int
	properties       map[string]string
	pairingRequired  bool
	passwordRequired bool
}

type deviceRecord struct {
	device    output.Device
	endpoint  endpointConfig
	auth      storedAuth
	expiresAt time.Time
}

type preparedResource struct {
	deviceID string
	resource output.Resource
	track    library.Track
}

type Manager struct {
	config   Config
	db       *sql.DB
	library  *library.Service
	networks []localNetwork
	discover discoverer
	identity string

	pairMu   sync.Mutex
	mu       sync.RWMutex
	devices  map[string]deviceRecord
	prepared map[string]preparedResource
	sessions map[string]*playbackSession
	pending  map[string]*pairingSession
}

func New(db *sql.DB, libraryService *library.Service, config Config) (*Manager, error) {
	if db == nil || libraryService == nil {
		return nil, errors.New("airplay: database and library service are required")
	}
	validated, err := validateConfig(config)
	if err != nil {
		return nil, err
	}
	if err := checkHelper(validated.HelperPath); err != nil {
		return nil, err
	}
	networks, err := resolveNetworks(validated.Interfaces)
	if err != nil {
		return nil, err
	}
	identity, err := initializeCredentials(context.Background(), db)
	if err != nil {
		return nil, err
	}
	return &Manager{
		config: validated, db: db, library: libraryService, networks: networks,
		discover: mdnsDiscoverer{}, identity: identity, devices: make(map[string]deviceRecord),
		prepared: make(map[string]preparedResource), sessions: make(map[string]*playbackSession),
		pending: make(map[string]*pairingSession),
	}, nil
}

func checkHelper(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output bytes.Buffer
	command := exec.CommandContext(ctx, path, "--check")
	command.Stdout = &limitedBuffer{buffer: &output, remaining: 4096}
	command.Stderr = &limitedBuffer{remaining: 4096}
	if err := command.Run(); err != nil {
		return fmt.Errorf("airplay: sender helper self-check failed: %w", err)
	}
	expected := fmt.Sprintf("jastreamer-airplay protocol=%d pyatv=%s required=%s", helperProtocolVersion, helperPyatvVersion, helperPyatvVersion)
	if strings.TrimSpace(output.String()) != expected {
		return errors.New("airplay: sender helper reported an incompatible protocol or pyatv version")
	}
	return nil
}

type limitedBuffer struct {
	buffer    *bytes.Buffer
	remaining int
}

func (writer *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	if writer.remaining > 0 && writer.buffer != nil {
		count := min(writer.remaining, len(value))
		_, _ = writer.buffer.Write(value[:count])
		writer.remaining -= count
	}
	return original, nil
}

func (manager *Manager) Run(ctx context.Context) error {
	_ = manager.Refresh(ctx)
	discoveryTicker := time.NewTicker(manager.config.DiscoveryInterval)
	expiryTicker := time.NewTicker(time.Second)
	defer discoveryTicker.Stop()
	defer expiryTicker.Stop()
	defer manager.shutdown()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-discoveryTicker.C:
			_ = manager.Refresh(ctx)
		case now := <-expiryTicker.C:
			manager.expire(now)
		}
	}
}

func (manager *Manager) Refresh(ctx context.Context) error {
	values, err := manager.discover.Search(ctx, manager.networks)
	if err != nil {
		return err
	}
	grouped := make(map[string][]advertisement)
	for _, value := range values {
		grouped[value.identity] = append(grouped[value.identity], value)
	}
	changed := false
	for identity, group := range grouped {
		value := mergeAdvertisements(group)
		id := "airplay:" + identity
		loadedAuth, err := loadAuth(ctx, manager.db, id)
		if err != nil {
			return err
		}
		endpoint := endpointConfig{
			address: value.address, localAddress: value.localAddress, port: value.port,
			properties: value.properties, pairingRequired: value.pairingRequired,
			passwordRequired: value.passwordRequired,
		}

		manager.mu.Lock()
		previous, existed := manager.devices[id]
		auth := loadedAuth
		if existed {
			// In-memory authorization is newer than this refresh's database
			// snapshot when pairing or invalidation raced with discovery.
			auth = previous.auth
		}
		record := deviceRecord{
			device: output.Device{
				ID: id, Name: value.name, Manufacturer: manufacturer(value.model), Model: value.model,
				Address: netip.AddrPortFrom(value.address, uint16(value.port)).String(), Online: true,
				LastSeen:     value.lastSeen.UTC().Format(time.RFC3339Nano),
				Capabilities: output.Capabilities{Play: true, Pause: true, Stop: true, Seek: true},
				ProtocolInfo: []string{supportedAudioFormat.mime()}, Protocol: output.ProtocolAirPlay,
				PairingRequired:  endpoint.pairingRequired && auth.credentials == "",
				PasswordRequired: endpoint.passwordRequired && auth.password == "",
			},
			endpoint: endpoint, auth: auth, expiresAt: value.expiresAt,
		}
		manager.devices[id] = record
		manager.mu.Unlock()
		if !existed || !sameDevice(previous.device, record.device) {
			changed = true
		}
	}
	if changed {
		manager.config.Notify("renderers")
	}
	return nil
}

func mergeAdvertisements(values []advertisement) advertisement {
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].service == values[j].service {
			return values[i].lastSeen.After(values[j].lastSeen)
		}
		return values[i].service == raopService
	})
	selected := values[0]
	properties := make(map[string]string)
	pairing, password := false, false
	for index := len(values) - 1; index >= 0; index-- {
		for key, value := range values[index].properties {
			properties[key] = value
		}
		pairing = pairing || values[index].pairingRequired
		password = password || values[index].passwordRequired
		if selected.model == "" {
			selected.model = values[index].model
		}
		if values[index].expiresAt.After(selected.expiresAt) {
			selected.expiresAt = values[index].expiresAt
		}
		if values[index].lastSeen.After(selected.lastSeen) {
			selected.lastSeen = values[index].lastSeen
		}
	}
	selected.properties = properties
	selected.pairingRequired = pairing
	selected.passwordRequired = password
	return selected
}

func manufacturer(model string) string {
	lower := strings.ToLower(model)
	if strings.HasPrefix(lower, "appletv") || strings.HasPrefix(lower, "audioaccessory") || strings.HasPrefix(lower, "airport") {
		return "Apple"
	}
	return ""
}

func sameDevice(left, right output.Device) bool {
	return left.ID == right.ID && left.Name == right.Name && left.Manufacturer == right.Manufacturer &&
		left.Model == right.Model && left.Address == right.Address && left.Online == right.Online &&
		left.PairingRequired == right.PairingRequired && left.PasswordRequired == right.PasswordRequired
}

func (manager *Manager) Devices() []output.Device {
	manager.mu.RLock()
	values := make([]output.Device, 0, len(manager.devices))
	for _, record := range manager.devices {
		values = append(values, cloneDevice(record.device))
	}
	manager.mu.RUnlock()
	sort.Slice(values, func(i, j int) bool {
		left, right := strings.ToLower(values[i].Name), strings.ToLower(values[j].Name)
		if left == right {
			return values[i].ID < values[j].ID
		}
		return left < right
	})
	return values
}

func (manager *Manager) Device(id string) (output.Device, bool) {
	manager.mu.RLock()
	record, ok := manager.devices[id]
	manager.mu.RUnlock()
	if !ok {
		return output.Device{}, false
	}
	return cloneDevice(record.device), true
}

func cloneDevice(value output.Device) output.Device {
	value.ProtocolInfo = append([]string(nil), value.ProtocolInfo...)
	if value.ProtocolInfo == nil {
		value.ProtocolInfo = []string{}
	}
	return value
}

func (manager *Manager) deviceRecord(id, action string) (deviceRecord, error) {
	manager.mu.RLock()
	record, ok := manager.devices[id]
	manager.mu.RUnlock()
	if !ok || !record.device.Online {
		return deviceRecord{}, output.NewActionError(output.ErrorUnsupported, action, 0, errors.New("AirPlay receiver is unavailable"))
	}
	return record, nil
}

func (manager *Manager) expire(now time.Time) {
	var stop []*playbackSession
	changed := false
	manager.mu.Lock()
	for id, record := range manager.devices {
		if record.device.Online && !record.expiresAt.After(now) {
			record.device.Online = false
			manager.devices[id] = record
			if session := manager.sessions[id]; session != nil {
				stop = append(stop, session)
			}
			changed = true
		}
	}
	manager.mu.Unlock()
	for _, session := range stop {
		session.stopAsync()
	}
	if changed {
		manager.config.Notify("renderers")
	}
}

func (manager *Manager) shutdown() {
	manager.mu.Lock()
	sessions := make([]*playbackSession, 0, len(manager.sessions))
	for _, session := range manager.sessions {
		sessions = append(sessions, session)
	}
	pending := make([]*pairingSession, 0, len(manager.pending))
	for _, session := range manager.pending {
		pending = append(pending, session)
	}
	manager.mu.Unlock()
	for _, session := range pending {
		session.close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), processStopTimeout)
	defer cancel()
	var wait sync.WaitGroup
	for _, session := range sessions {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_ = session.stop(ctx)
		}()
	}
	wait.Wait()
}
