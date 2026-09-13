package cast

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

type endpoint struct {
	address netip.Addr
	local   netip.Addr
	port    int
	network netip.Prefix
}

type deviceRecord struct {
	device    output.Device
	endpoint  endpoint
	expiresAt time.Time
	session   *playbackSession
}

type playbackSession struct {
	opMu sync.Mutex
	mu   sync.RWMutex

	resource       output.Resource
	mediaSessionID int64
	appSessionID   string
	transportID    string
	lastStatus     mediaStatus
	hasStatus      bool
	client         *client
	terminal       output.Observation
	hasTerminal    bool
}

type managerDependencies struct {
	networks []localNetwork
	discover discoverer
	now      func() time.Time
	dial     func(context.Context, endpoint) (net.Conn, error)
}

type Manager struct {
	config   Config
	networks []localNetwork
	discover discoverer
	now      func() time.Time
	dial     func(context.Context, endpoint) (net.Conn, error)

	mu      sync.RWMutex
	devices map[string]deviceRecord

	lifecycleMu sync.Mutex
	closed      bool
	connections map[net.Conn]struct{}
}

var _ output.Controller = (*Manager)(nil)

func New(config Config) (*Manager, error) {
	validated, err := validateConfig(config)
	if err != nil {
		return nil, err
	}
	networks, err := resolveNetworks(validated.Interfaces)
	if err != nil {
		return nil, err
	}
	return newManager(validated, managerDependencies{
		networks: networks, discover: mdnsDiscoverer{}, now: time.Now, dial: dialCast,
	})
}

func newManager(config Config, dependencies managerDependencies) (*Manager, error) {
	if config.DiscoveryInterval <= 0 || config.Notify == nil || len(dependencies.networks) == 0 || dependencies.discover == nil || dependencies.now == nil || dependencies.dial == nil {
		return nil, ErrInvalidConfig
	}
	return &Manager{
		config: config, networks: append([]localNetwork(nil), dependencies.networks...),
		discover: dependencies.discover, now: dependencies.now, dial: dependencies.dial,
		devices: make(map[string]deviceRecord), connections: make(map[net.Conn]struct{}),
	}, nil
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
	manager.lifecycleMu.Lock()
	closed := manager.closed
	manager.lifecycleMu.Unlock()
	if closed {
		return ErrUnavailable
	}
	values, err := manager.discover.Search(ctx, manager.networks)
	if err != nil {
		return err
	}
	grouped := make(map[string][]advertisement)
	for _, value := range values {
		grouped[value.identity] = append(grouped[value.identity], value)
	}
	changed := false
	for identity, candidates := range grouped {
		id := "cast:" + identity
		manager.mu.Lock()
		previous, existed := manager.devices[id]
		selected := selectAdvertisement(candidates, previous)
		selectedNetwork, ok := manager.networkFor(selected.localAddress, selected.address)
		if !ok {
			manager.mu.Unlock()
			continue
		}
		nextEndpoint := endpoint{
			address: selected.address, local: selected.localAddress,
			port: selected.port, network: selectedNetwork.prefix,
		}
		session := previous.session
		if session == nil {
			session = &playbackSession{}
		} else if existed && previous.endpoint != nextEndpoint {
			session.closeClient()
			session = &playbackSession{}
		}
		next := deviceRecord{
			device: output.Device{
				ID: id, Name: selected.name, Model: selected.model,
				Address:      netip.AddrPortFrom(selected.address, uint16(selected.port)).String(),
				LocalAddress: selected.localAddress.String(), Online: true,
				LastSeen:     selected.lastSeen.UTC().Format(time.RFC3339Nano),
				Capabilities: output.Capabilities{Play: true, Pause: true, Stop: true, Seek: true},
				ProtocolInfo: []string{}, Protocol: output.ProtocolCast,
			},
			endpoint: nextEndpoint, expiresAt: selected.expiresAt, session: session,
		}
		manager.devices[id] = next
		manager.mu.Unlock()
		if !existed || !sameDevice(previous.device, next.device) {
			changed = true
		}
	}
	if changed {
		manager.config.Notify("renderers")
	}
	return nil
}

func selectAdvertisement(values []advertisement, previous deviceRecord) advertisement {
	if previous.device.ID != "" {
		for _, value := range values {
			if value.address == previous.endpoint.address && value.localAddress == previous.endpoint.local && value.port == previous.endpoint.port {
				return value
			}
		}
	}
	sort.Slice(values, func(i, j int) bool {
		left := values[i].localAddress.String() + "\x00" + values[i].address.String()
		right := values[j].localAddress.String() + "\x00" + values[j].address.String()
		if left == right {
			return values[i].port < values[j].port
		}
		return left < right
	})
	return values[0]
}

func (manager *Manager) networkFor(local, remote netip.Addr) (localNetwork, bool) {
	for _, network := range manager.networks {
		if network.local == local && network.prefix.Contains(remote) {
			return network, true
		}
	}
	return localNetwork{}, false
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

func sameDevice(left, right output.Device) bool {
	return left.ID == right.ID && left.Name == right.Name && left.Model == right.Model &&
		left.Address == right.Address && left.LocalAddress == right.LocalAddress && left.Online == right.Online
}

func (manager *Manager) controlRecord(id, action string) (deviceRecord, error) {
	manager.mu.RLock()
	record, ok := manager.devices[id]
	manager.mu.RUnlock()
	if !ok || !record.device.Online {
		return deviceRecord{}, output.NewActionError(output.ErrorUnsupported, action, 0, ErrUnavailable)
	}
	return record, nil
}

func (manager *Manager) expire(now time.Time) {
	var closeSessions []*playbackSession
	changed := false
	manager.mu.Lock()
	for id, record := range manager.devices {
		if record.device.Online && !record.expiresAt.After(now) {
			record.device.Online = false
			manager.devices[id] = record
			if record.session != nil {
				closeSessions = append(closeSessions, record.session)
			}
			changed = true
		}
	}
	manager.mu.Unlock()
	for _, session := range closeSessions {
		session.closeClient()
	}
	if changed {
		manager.config.Notify("renderers")
	}
}

func (manager *Manager) trackConnection(connection net.Conn) bool {
	manager.lifecycleMu.Lock()
	defer manager.lifecycleMu.Unlock()
	if manager.closed {
		return false
	}
	manager.connections[connection] = struct{}{}
	return true
}

func (manager *Manager) untrackConnection(connection net.Conn) {
	manager.lifecycleMu.Lock()
	delete(manager.connections, connection)
	manager.lifecycleMu.Unlock()
}

func (manager *Manager) shutdown() {
	manager.lifecycleMu.Lock()
	if manager.closed {
		manager.lifecycleMu.Unlock()
		return
	}
	manager.closed = true
	connections := make([]net.Conn, 0, len(manager.connections))
	for connection := range manager.connections {
		connections = append(connections, connection)
	}
	manager.lifecycleMu.Unlock()
	manager.mu.RLock()
	sessions := make([]*playbackSession, 0, len(manager.devices))
	for _, record := range manager.devices {
		if record.session != nil {
			sessions = append(sessions, record.session)
		}
	}
	manager.mu.RUnlock()
	for _, session := range sessions {
		session.closeClient()
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func dialCast(ctx context.Context, target endpoint) (net.Conn, error) {
	if !target.address.Is4() || !target.local.Is4() || !target.network.Contains(target.address) || !allowedLocalAddress(target.address) || !target.network.Contains(target.local) || target.port < 1 || target.port > 65535 {
		return nil, ErrUnavailable
	}
	dialer := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.IP(target.local.AsSlice())}}
	raw, err := dialer.DialContext(ctx, "tcp4", netip.AddrPortFrom(target.address, uint16(target.port)).String())
	if err != nil {
		return nil, err
	}
	// Cast receivers use a device-generated certificate for their local Cast V2
	// socket. It is not a Web PKI HTTPS identity and has no stable trust root or
	// hostname to verify. InsecureSkipVerify is therefore scoped to this one TLS
	// client only. The peer is first admitted from _googlecast._tcp on a selected
	// private interface, must remain inside that exact interface prefix, and is
	// dialed by its literal advertised IPv4 address; this does not weaken any
	// process-wide HTTP or TLS policy.
	tlsConnection := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}) //nolint:gosec -- see trust boundary above
	if err := tlsConnection.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, err
	}
	return tlsConnection, nil
}

func (manager *Manager) withClient(ctx context.Context, record deviceRecord, action string, operation func(context.Context, *client) error) error {
	actionContext, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	session := record.session
	session.mu.Lock()
	castClient := session.client
	if castClient != nil && !castClient.isAlive() {
		session.client = nil
		castClient = nil
	}
	if castClient == nil {
		connection, err := manager.dial(actionContext, record.endpoint)
		if err != nil {
			session.mu.Unlock()
			return classifyActionError(actionContext, action, err)
		}
		if !manager.trackConnection(connection) {
			session.mu.Unlock()
			_ = connection.Close()
			return output.NewActionError(output.ErrorCancelled, action, 0, ErrUnavailable)
		}
		var persistent *client
		persistent = newClient(connection,
			func() {
				manager.untrackConnection(connection)
				session.clearClient(persistent)
			},
			func(source string, statuses []mediaStatus) {
				session.handleMediaEvent(source, statuses, manager.now())
			},
		)
		session.client = persistent
		castClient = persistent
		castClient.start()
	}
	session.mu.Unlock()
	if err := castClient.connect(actionContext, receiverID); err != nil {
		castClient.Close()
		return classifyActionError(actionContext, action, err)
	}
	if err := operation(actionContext, castClient); err != nil {
		return classifyActionError(actionContext, action, err)
	}
	return nil
}

func classifyActionError(ctx context.Context, action string, err error) error {
	var actionError *output.ActionError
	if errors.As(err, &actionError) {
		return err
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return output.NewActionError(output.ErrorTimeout, action, 0, err)
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return output.NewActionError(output.ErrorCancelled, action, 0, err)
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return output.NewActionError(output.ErrorTimeout, action, 0, err)
	}
	var response *remoteError
	if errors.As(err, &response) {
		return output.NewActionError(output.ErrorResponse, action, 0, err)
	}
	if errors.Is(err, ErrInvalidResponse) || errors.Is(err, ErrUnavailable) {
		return output.NewActionError(output.ErrorResponse, action, 0, err)
	}
	return output.NewActionError(output.ErrorTransport, action, 0, fmt.Errorf("cast transport: %w", err))
}

func unsupported(action string, cause error) error {
	return output.NewActionError(output.ErrorUnsupported, action, 0, cause)
}
