package dlna

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultDiscoveryInterval = 30 * time.Second
	inspectionTimeout        = 15 * time.Second
	maxConcurrentInspections = 8
)

type deviceRecord struct {
	device         output.Device
	udn            string
	descriptionURL string
	controlURL     string
	serviceType    string
	actions        map[string]bool
	queries        map[string]bool
	source         netip.Addr
	network        localNetwork
	lastSeen       time.Time
	expiresAt      time.Time
	bootID         string
	configID       string
	inspection     uint64
}

type pendingInspection struct {
	sequence  uint64
	candidate advertisement
}

type notificationListener interface {
	ReadFromUDPAddrPort([]byte) (int, netip.AddrPort, error)
	Close() error
}

type managerDependencies struct {
	networks   []localNetwork
	discoverer discoveryClient
	httpClient *http.Client
	now        func() time.Time
	listen     func(localNetwork) (notificationListener, error)
}

type Manager struct {
	config     Config
	networks   []localNetwork
	discoverer discoveryClient
	httpClient *http.Client
	now        func() time.Time
	listen     func(localNetwork) (notificationListener, error)
	notify     func(string)

	mu       sync.RWMutex
	devices  map[string]deviceRecord
	byUDN    map[string]string
	pending  map[string]pendingInspection
	sequence uint64

	cacheMu         sync.RWMutex
	xmlCache        map[string]xmlCacheEntry
	inspectionSlots chan struct{}
}

func New(config Config) (*Manager, error) {
	if config.DiscoveryInterval < 0 {
		return nil, ErrInvalidConfig
	}
	if config.DiscoveryInterval == 0 {
		config.DiscoveryInterval = defaultDiscoveryInterval
	}
	networks, err := resolveNetworks(config.Interfaces)
	if err != nil {
		return nil, err
	}
	return newManager(config, managerDependencies{
		networks: networks, discoverer: ssdpDiscoverer{}, httpClient: httpClient(),
		now: time.Now, listen: listenMulticast,
	})
}

func newManager(config Config, dependencies managerDependencies) (*Manager, error) {
	if config.DiscoveryInterval <= 0 || len(dependencies.networks) == 0 || dependencies.discoverer == nil || dependencies.httpClient == nil || dependencies.now == nil || dependencies.listen == nil {
		return nil, ErrInvalidConfig
	}
	notify := config.Notify
	if notify == nil {
		notify = func(string) {}
	}
	return &Manager{
		config: config, networks: append([]localNetwork(nil), dependencies.networks...),
		discoverer: dependencies.discoverer, httpClient: dependencies.httpClient,
		now: dependencies.now, listen: dependencies.listen, notify: notify,
		devices: make(map[string]deviceRecord), byUDN: make(map[string]string),
		pending: make(map[string]pendingInspection), xmlCache: make(map[string]xmlCacheEntry),
		inspectionSlots: make(chan struct{}, maxConcurrentInspections),
	}, nil
}

func (manager *Manager) Run(ctx context.Context) error {
	listeners := make([]notificationListener, 0, len(manager.networks))
	var listenersWG sync.WaitGroup
	for _, network := range manager.networks {
		listener, err := manager.listen(network)
		if err != nil {
			continue
		}
		listeners = append(listeners, listener)
		listenersWG.Add(1)
		go func(network localNetwork, listener notificationListener) {
			defer listenersWG.Done()
			manager.readNotifications(ctx, network, listener)
		}(network, listener)
	}
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
		listenersWG.Wait()
	}()
	_ = manager.Refresh(ctx)
	discoveryTicker := time.NewTicker(manager.config.DiscoveryInterval)
	expiryTicker := time.NewTicker(time.Second)
	defer discoveryTicker.Stop()
	defer expiryTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-discoveryTicker.C:
			_ = manager.Refresh(ctx)
		case <-expiryTicker.C:
			manager.expireDevices(manager.now())
		}
	}
}

func (manager *Manager) Refresh(ctx context.Context) error {
	values, err := manager.discoverer.Search(ctx, manager.networks)
	if err != nil {
		return fmt.Errorf("dlna: discovery failed: %w", err)
	}
	for _, value := range values {
		manager.acceptAdvertisement(ctx, value)
	}
	return nil
}

func (manager *Manager) Devices() []output.Device {
	manager.mu.RLock()
	values := make([]output.Device, 0, len(manager.devices))
	for _, record := range manager.devices {
		values = append(values, cloneDevice(record.device))
	}
	manager.mu.RUnlock()
	sort.Slice(values, func(left, right int) bool {
		leftName := strings.ToLower(values[left].Name)
		rightName := strings.ToLower(values[right].Name)
		if leftName == rightName {
			return values[left].ID < values[right].ID
		}
		return leftName < rightName
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

func (manager *Manager) SetURI(ctx context.Context, id string, resource output.Resource) error {
	record, err := manager.controlRecord(id, "SetAVTransportURI")
	if err != nil {
		return err
	}
	if err := validateResource(resource, record); err != nil {
		return err
	}
	metadata, err := didlMetadata(resource)
	if err != nil {
		return err
	}
	return manager.control(ctx, record, "SetAVTransportURI", []soapArgument{
		{name: "InstanceID", value: "0"},
		{name: "CurrentURI", value: resource.URL},
		{name: "CurrentURIMetaData", value: metadata},
	})
}

func (manager *Manager) Play(ctx context.Context, id string) error {
	record, err := manager.controlRecord(id, "Play")
	if err != nil {
		return err
	}
	return manager.control(ctx, record, "Play", []soapArgument{{name: "InstanceID", value: "0"}, {name: "Speed", value: "1"}})
}

func (manager *Manager) Pause(ctx context.Context, id string) error {
	record, err := manager.controlRecord(id, "Pause")
	if err != nil {
		return err
	}
	return manager.control(ctx, record, "Pause", []soapArgument{{name: "InstanceID", value: "0"}})
}

func (manager *Manager) Stop(ctx context.Context, id string) error {
	record, err := manager.controlRecord(id, "Stop")
	if err != nil {
		return err
	}
	return manager.control(ctx, record, "Stop", []soapArgument{{name: "InstanceID", value: "0"}})
}

func (manager *Manager) Seek(ctx context.Context, id string, positionMS int64) error {
	if positionMS < 0 {
		return ErrInvalidResource
	}
	record, err := manager.controlRecord(id, "Seek")
	if err != nil {
		return err
	}
	return manager.control(ctx, record, "Seek", []soapArgument{
		{name: "InstanceID", value: "0"}, {name: "Unit", value: "REL_TIME"}, {name: "Target", value: formatClock(positionMS)},
	})
}

func (manager *Manager) Observe(ctx context.Context, id string) (output.Observation, error) {
	record, err := manager.controlRecord(id, "GetTransportInfo")
	if err != nil {
		return output.Observation{}, err
	}
	observeContext, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	transportData, err := manager.soap(observeContext, record, "GetTransportInfo", []soapArgument{{name: "InstanceID", value: "0"}})
	if err != nil {
		return output.Observation{}, err
	}
	var transport struct {
		State  string `xml:"Body>GetTransportInfoResponse>CurrentTransportState"`
		Status string `xml:"Body>GetTransportInfoResponse>CurrentTransportStatus"`
	}
	if err := decodeSafeXML(bytes.NewReader(transportData), &transport); err != nil || strings.TrimSpace(transport.State) == "" {
		return output.Observation{}, output.NewActionError(output.ErrorResponse, "GetTransportInfo", 0, ErrInvalidResponse)
	}
	observation := output.Observation{
		State:           normalizeTransportState(transport.State),
		TransportStatus: strings.TrimSpace(transport.Status),
		ObservedAt:      manager.now(),
	}
	hasDuration := false
	if record.queries["GetPositionInfo"] {
		positionData, positionErr := manager.soap(observeContext, record, "GetPositionInfo", []soapArgument{{name: "InstanceID", value: "0"}})
		switch {
		case positionErr == nil:
			hasDuration, err = applyPositionInfo(positionData, &observation)
			if err != nil {
				return output.Observation{}, output.NewActionError(output.ErrorResponse, "GetPositionInfo", 0, ErrInvalidResponse)
			}
		case !errors.Is(positionErr, ErrUnsupported):
			return output.Observation{}, positionErr
		}
	}
	if (!observation.HasURI || !hasDuration) && record.queries["GetMediaInfo"] {
		mediaData, mediaErr := manager.soap(observeContext, record, "GetMediaInfo", []soapArgument{{name: "InstanceID", value: "0"}})
		if mediaErr == nil {
			enriched := observation
			if applyMediaInfo(mediaData, &enriched, hasDuration) == nil {
				observation = enriched
			}
		}
	}
	return observation, nil
}

func applyPositionInfo(data []byte, observation *output.Observation) (bool, error) {
	var position struct {
		Relative string  `xml:"Body>GetPositionInfoResponse>RelTime"`
		Duration string  `xml:"Body>GetPositionInfoResponse>TrackDuration"`
		URI      *string `xml:"Body>GetPositionInfoResponse>TrackURI"`
	}
	if err := decodeSafeXML(bytes.NewReader(data), &position); err != nil {
		return false, err
	}
	positionMS, hasPosition, err := parseClock(position.Relative)
	if err != nil {
		return false, err
	}
	durationMS, hasDuration, err := parseClock(position.Duration)
	if err != nil {
		return false, err
	}
	observation.PositionMS = positionMS
	observation.HasPosition = hasPosition
	if hasDuration {
		observation.DurationMS = durationMS
	}
	if position.URI != nil {
		observation.URI = strings.TrimSpace(*position.URI)
		observation.HasURI = true
	}
	return hasDuration, nil
}

func applyMediaInfo(data []byte, observation *output.Observation, hasDuration bool) error {
	var media struct {
		Duration string  `xml:"Body>GetMediaInfoResponse>MediaDuration"`
		URI      *string `xml:"Body>GetMediaInfoResponse>CurrentURI"`
	}
	if err := decodeSafeXML(bytes.NewReader(data), &media); err != nil {
		return err
	}
	durationMS, mediaHasDuration, err := parseClock(media.Duration)
	if err != nil {
		return err
	}
	if !hasDuration && mediaHasDuration {
		observation.DurationMS = durationMS
	}
	if !observation.HasURI && media.URI != nil {
		observation.URI = strings.TrimSpace(*media.URI)
		observation.HasURI = true
	}
	return nil
}

func (manager *Manager) control(ctx context.Context, record deviceRecord, action string, arguments []soapArgument) error {
	actionContext, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	_, err := manager.soap(actionContext, record, action, arguments)
	return err
}

func (manager *Manager) soap(ctx context.Context, record deviceRecord, action string, arguments []soapArgument) ([]byte, error) {
	candidate := advertisement{
		source: netip.AddrPortFrom(record.source, 0), network: record.network,
		location: record.descriptionURL, udn: record.udn,
	}
	return manager.executeSOAP(ctx, soapCall{
		candidate: candidate, url: record.controlURL, service: record.serviceType,
		action: action, arguments: arguments,
	})
}

func (manager *Manager) controlRecord(id, action string) (deviceRecord, error) {
	manager.mu.RLock()
	record, ok := manager.devices[id]
	manager.mu.RUnlock()
	if !ok {
		return deviceRecord{}, ErrNotFound
	}
	if !record.device.Online {
		return deviceRecord{}, ErrOffline
	}
	if !record.actions[action] {
		return deviceRecord{}, output.NewActionError(output.ErrorUnsupported, action, 0, ErrUnsupported)
	}
	return record, nil
}

func validateResource(resource output.Resource, record deviceRecord) error {
	if resource.DurationMS < 0 || resource.Size < 0 || len(resource.URL) > 8192 || len(resource.ArtworkURL) > 8192 || len(resource.Mime) > 255 || len(resource.Title) > 4096 || len(resource.Artist) > 4096 || len(resource.Album) > 4096 {
		return ErrInvalidResource
	}
	if !validMediaURL(resource.URL, record.network) || strings.TrimSpace(resource.Mime) == "" || strings.ContainsAny(resource.Mime, "\r\n") || !validXMLText(resource.Mime) || !validXMLText(resource.Title) || !validXMLText(resource.Artist) || !validXMLText(resource.Album) {
		return ErrInvalidResource
	}
	if resource.ArtworkURL != "" && !validMediaURL(resource.ArtworkURL, record.network) {
		return ErrInvalidResource
	}
	return nil
}

func validXMLText(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == '\t' || character == '\n' || character == '\r' || (character >= 0x20 && character <= 0xd7ff) || (character >= 0xe000 && character <= 0xfffd) || (character >= 0x10000 && character <= 0x10ffff) {
			continue
		}
		return false
	}
	return true
}

func validMediaURL(raw string, network localNetwork) bool {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" || parsed.Path == "" {
		return false
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	return err == nil && network.prefix.Contains(address) && allowedLocalAddress(address)
}

func normalizeTransportState(raw string) string {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "STOPPED", "NO_MEDIA_PRESENT":
		return "stopped"
	case "PLAYING":
		return "playing"
	case "PAUSED", "PAUSED_PLAYBACK", "PAUSED_RECORDING":
		return "paused"
	case "TRANSITIONING":
		return "transitioning"
	default:
		return "unknown"
	}
}

func rendererID(udn string) string {
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(udn))))
	return "renderer-" + hex.EncodeToString(digest[:12])
}

func (manager *Manager) acceptAdvertisement(parent context.Context, value advertisement) {
	if value.kind == "byebye" {
		manager.markOffline(value)
		return
	}
	now := manager.now()
	if value.maxAge < 0 {
		value.maxAge = defaultMaxAge
	}
	manager.mu.Lock()
	id, exists := manager.byUDN[value.udn]
	if exists {
		record := manager.devices[id]
		if value.bootID == "" {
			value.bootID = record.bootID
		}
		if value.configID == "" {
			value.configID = record.configID
		}
		if pending, ok := manager.pending[value.udn]; ok && sameInspectionCandidate(pending.candidate, value) {
			pending.candidate.maxAge = value.maxAge
			manager.pending[value.udn] = pending
			record.lastSeen = now
			record.expiresAt = now.Add(value.maxAge)
			manager.devices[id] = record
			manager.mu.Unlock()
			return
		}
		changedEndpoint := record.descriptionURL != value.location || record.source != value.source.Addr()
		changedVersion := (value.bootID != "" && value.bootID != record.bootID) || (value.configID != "" && value.configID != record.configID)
		if value.kind == "update" || value.nextBoot != "" {
			changedVersion = true
		}
		if !changedEndpoint && !changedVersion && record.inspection == 0 {
			wasOffline := !record.device.Online
			record.lastSeen = now
			record.expiresAt = now.Add(value.maxAge)
			record.device.Online = true
			record.device.LastSeen = now.UTC().Format(time.RFC3339Nano)
			manager.devices[id] = record
			manager.mu.Unlock()
			if wasOffline {
				manager.notify("renderers")
			}
			return
		}
		if !manager.reserveInspection() {
			manager.mu.Unlock()
			return
		}
		record.device.Online = false
		record.lastSeen = now
		record.expiresAt = now.Add(value.maxAge)
		record.descriptionURL = value.location
		record.source = value.source.Addr()
		record.network = value.network
		record.bootID = value.bootID
		record.configID = value.configID
		manager.sequence++
		record.inspection = manager.sequence
		manager.pending[value.udn] = pendingInspection{sequence: record.inspection, candidate: value}
		manager.devices[id] = record
		sequence := record.inspection
		manager.mu.Unlock()
		manager.notify("renderers")
		manager.launchInspection(parent, value, sequence)
		return
	}
	if pending, ok := manager.pending[value.udn]; ok && sameInspectionCandidate(pending.candidate, value) {
		pending.candidate.maxAge = value.maxAge
		manager.pending[value.udn] = pending
		manager.mu.Unlock()
		return
	}
	if !manager.reserveInspection() {
		manager.mu.Unlock()
		return
	}
	manager.sequence++
	sequence := manager.sequence
	manager.pending[value.udn] = pendingInspection{sequence: sequence, candidate: value}
	manager.mu.Unlock()
	manager.launchInspection(parent, value, sequence)
}

func (manager *Manager) reserveInspection() bool {
	select {
	case manager.inspectionSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func sameInspectionCandidate(left, right advertisement) bool {
	return left.udn == right.udn &&
		left.location == right.location &&
		left.source.Addr() == right.source.Addr() &&
		left.network.index == right.network.index &&
		left.network.local == right.network.local &&
		left.bootID == right.bootID &&
		left.configID == right.configID &&
		left.nextBoot == right.nextBoot &&
		left.kind == right.kind
}

func (manager *Manager) launchInspection(parent context.Context, value advertisement, sequence uint64) {
	go func() {
		defer func() { <-manager.inspectionSlots }()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), inspectionTimeout)
		defer cancel()
		inspected, err := manager.inspect(ctx, value)
		if err != nil {
			manager.clearInspection(value.udn, sequence)
			return
		}
		now := manager.now()
		inspected.device.LastSeen = now.UTC().Format(time.RFC3339Nano)
		inspected.device.Online = true
		if inspected.device.ProtocolInfo == nil {
			inspected.device.ProtocolInfo = []string{}
		}
		record := deviceRecord{
			device: inspected.device, udn: inspected.udn,
			descriptionURL: inspected.descriptionURL, controlURL: inspected.controlURL,
			serviceType: inspected.serviceType, actions: inspected.actions, queries: inspected.queries,
			source: inspected.source, network: inspected.network,
			lastSeen: now, bootID: value.bootID, configID: value.configID,
		}
		manager.mu.Lock()
		pending, ok := manager.pending[value.udn]
		if !ok || pending.sequence != sequence {
			manager.mu.Unlock()
			return
		}
		record.expiresAt = now.Add(pending.candidate.maxAge)
		delete(manager.pending, value.udn)
		if currentID, exists := manager.byUDN[value.udn]; exists {
			delete(manager.devices, currentID)
		}
		manager.devices[record.device.ID] = record
		manager.byUDN[value.udn] = record.device.ID
		manager.mu.Unlock()
		manager.notify("renderers")
	}()
}

func (manager *Manager) clearInspection(udn string, sequence uint64) {
	manager.mu.Lock()
	pending, ok := manager.pending[udn]
	if ok && pending.sequence == sequence {
		delete(manager.pending, udn)
		if id, exists := manager.byUDN[udn]; exists {
			record := manager.devices[id]
			if record.inspection == sequence {
				record.inspection = 0
				manager.devices[id] = record
			}
		}
	}
	manager.mu.Unlock()
}

func (manager *Manager) markOffline(value advertisement) {
	changed := false
	manager.mu.Lock()
	delete(manager.pending, value.udn)
	if id, exists := manager.byUDN[value.udn]; exists {
		record := manager.devices[id]
		if record.source == value.source.Addr() && record.device.Online {
			record.device.Online = false
			record.expiresAt = manager.now()
			manager.devices[id] = record
			changed = true
		}
	}
	manager.mu.Unlock()
	if changed {
		manager.notify("renderers")
	}
}

func (manager *Manager) expireDevices(now time.Time) {
	changed := false
	manager.mu.Lock()
	for id, record := range manager.devices {
		if record.device.Online && !record.expiresAt.After(now) {
			record.device.Online = false
			manager.devices[id] = record
			changed = true
		}
	}
	manager.mu.Unlock()
	if changed {
		manager.notify("renderers")
	}
}

func (manager *Manager) readNotifications(ctx context.Context, network localNetwork, listener notificationListener) {
	buffer := make([]byte, maxSSDPPacket)
	for {
		count, source, err := listener.ReadFromUDPAddrPort(buffer)
		if err != nil {
			return
		}
		value, err := parseNotification(buffer[:count], source, network)
		if err == nil {
			manager.acceptAdvertisement(ctx, value)
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func listenMulticast(network localNetwork) (notificationListener, error) {
	iface, err := net.InterfaceByIndex(network.index)
	if err != nil {
		return nil, err
	}
	group := &net.UDPAddr{IP: net.ParseIP("239.255.255.250"), Port: 1900}
	connection, err := net.ListenMulticastUDP("udp4", iface, group)
	if err != nil {
		return nil, err
	}
	_ = connection.SetReadBuffer(maxSSDPPacket * 4)
	return connection, nil
}
