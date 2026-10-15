package raat

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"golang.org/x/net/ipv4"
)

const (
	soodServiceID         = "5e2042ad-9bc5-4508-be92-ff68f19bdc93"
	maxSOODPacket         = 16 << 10
	maxSOODProperties     = 128
	maxDiscoveryEndpoints = 64
	maxDiscoveryNetworks  = 64
	soodWindow            = 3 * time.Second
)

type deviceInfo struct {
	uniqueID        string
	protocol        string
	codeSigningKeys json.RawMessage
	name            string
	model           string
	manufacturer    string
}

type discoveredDevice struct {
	device   output.Device
	endpoint endpoint
	info     deviceInfo
}

type localNetwork struct {
	iface  net.Interface
	local  netip.Addr
	prefix netip.Prefix
}

type soodAdvertisement struct {
	endpoint endpoint
	name     string
}

func allowedLocalAddress(address netip.Addr) bool {
	return address.Is4() && (address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLoopback())
}

func allowedLocalPrefix(prefix netip.Prefix) bool {
	for _, scope := range [...]netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("127.0.0.0/8"),
	} {
		if prefix.Bits() >= scope.Bits() && scope.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func resolveNetworks(names []string) ([]localNetwork, error) {
	requested := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, errors.New("raat: invalid network interface")
		}
		requested[name] = struct{}{}
	}
	var interfaces []net.Interface
	if len(requested) == 0 {
		var err error
		interfaces, err = net.Interfaces()
		if err != nil {
			return nil, errors.New("raat: cannot list network interfaces")
		}
	} else {
		for name := range requested {
			iface, err := net.InterfaceByName(name)
			if err != nil {
				return nil, errors.New("raat: invalid network interface")
			}
			interfaces = append(interfaces, *iface)
		}
	}
	sort.Slice(interfaces, func(i, j int) bool { return interfaces[i].Index < interfaces[j].Index })
	var networks []localNetwork
	seen := make(map[string]bool)
	for _, iface := range interfaces {
		automatic := len(requested) == 0
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 || (automatic && iface.Flags&net.FlagLoopback != 0) {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			if automatic {
				continue
			}
			return nil, errors.New("raat: cannot inspect network interface")
		}
		for _, raw := range addresses {
			prefix, err := netip.ParsePrefix(raw.String())
			if err != nil || !allowedLocalAddress(prefix.Addr()) || !allowedLocalPrefix(prefix.Masked()) {
				continue
			}
			key := iface.Name + "\x00" + prefix.Addr().String()
			if seen[key] {
				continue
			}
			seen[key] = true
			if len(networks) >= maxDiscoveryNetworks {
				return nil, errors.New("raat: too many discovery networks")
			}
			networks = append(networks, localNetwork{iface: iface, local: prefix.Addr(), prefix: prefix.Masked()})
		}
	}
	if len(networks) == 0 {
		return nil, errors.New("raat: no private IPv4 multicast interface")
	}
	return networks, nil
}

// parseSOOD preserves null values as nil and rejects ambiguous or truncated fields.
func parseSOOD(packet []byte) (byte, map[string]*string, error) {
	if len(packet) < 6 || len(packet) > maxSOODPacket || string(packet[:4]) != "SOOD" || packet[4] != 2 || (packet[5] != 'R' && packet[5] != 'Q') {
		return 0, nil, errors.New("raat: invalid SOOD packet")
	}
	properties := make(map[string]*string)
	for offset := 6; offset < len(packet); {
		length := int(packet[offset])
		offset++
		if length == 0 || len(properties) >= maxSOODProperties || len(packet)-offset < length+2 {
			return 0, nil, errors.New("raat: invalid SOOD property")
		}
		key := string(packet[offset : offset+length])
		offset += length
		if !validDiscoveryText(key, 255) {
			return 0, nil, errors.New("raat: invalid SOOD key")
		}
		if _, exists := properties[key]; exists {
			return 0, nil, errors.New("raat: duplicate SOOD property")
		}
		valueLength := int(binary.BigEndian.Uint16(packet[offset : offset+2]))
		offset += 2
		if valueLength == 0xffff {
			properties[key] = nil
			continue
		}
		if len(packet)-offset < valueLength {
			return 0, nil, errors.New("raat: truncated SOOD property")
		}
		value := string(packet[offset : offset+valueLength])
		offset += valueLength
		if !validDiscoveryText(value, maxSOODPacket) {
			return 0, nil, errors.New("raat: invalid SOOD value")
		}
		properties[key] = &value
	}
	return packet[5], properties, nil
}

func validDiscoveryText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func soodQuery() []byte {
	packet := []byte{'S', 'O', 'O', 'D', 2, 'Q'}
	for _, property := range [][2]string{{"service_id", soodServiceID}, {"_tid", uuid.NewString()}, {"_replyport", "9003"}} {
		packet = append(packet, byte(len(property[0])))
		packet = append(packet, property[0]...)
		packet = binary.BigEndian.AppendUint16(packet, uint16(len(property[1])))
		packet = append(packet, property[1]...)
	}
	return packet
}

func advertisementFromSOOD(packet []byte, source netip.Addr, network localNetwork) (soodAdvertisement, bool) {
	if !allowedLocalAddress(source) || !network.prefix.Contains(source) {
		return soodAdvertisement{}, false
	}
	// Directed broadcast and subnet network addresses are not control endpoints.
	if network.prefix.Bits() < 31 {
		bytes := source.As4()
		mask := uint32(1)<<uint(32-network.prefix.Bits()) - 1
		if source == network.prefix.Addr() || binary.BigEndian.Uint32(bytes[:])&mask == mask {
			return soodAdvertisement{}, false
		}
	}
	kind, properties, err := parseSOOD(packet)
	if err != nil || kind != 'R' {
		return soodAdvertisement{}, false
	}
	value := func(key string) string {
		if properties[key] == nil {
			return ""
		}
		return *properties[key]
	}
	if value("service_id") != soodServiceID {
		return soodAdvertisement{}, false
	}
	tid := value("_tid")
	if len(tid) != 36 {
		return soodAdvertisement{}, false
	}
	id, err := uuid.Parse(tid)
	if err != nil || id == uuid.Nil {
		return soodAdvertisement{}, false
	}
	port, err := strconv.Atoi(value("tcp_port"))
	if err != nil || port < 1 || port > 65535 {
		return soodAdvertisement{}, false
	}
	name := value("name")
	if !validDiscoveryText(name, 512) {
		return soodAdvertisement{}, false
	}
	return soodAdvertisement{endpoint: endpoint{address: source.String(), localAddress: network.local.String(), port: port}, name: name}, true
}

func searchSOOD(ctx context.Context, networks []localNetwork) ([]soodAdvertisement, error) {
	group := &net.UDPAddr{IP: net.IPv4(239, 255, 90, 90), Port: 9003}
	conn, err := net.ListenMulticastUDP("udp4", &networks[0].iface, group)
	if err != nil {
		return nil, errors.New("raat: cannot open SOOD listener")
	}
	defer conn.Close()
	packetConn := ipv4.NewPacketConn(conn)
	if err := packetConn.SetControlMessage(ipv4.FlagInterface|ipv4.FlagDst, true); err != nil {
		return nil, errors.New("raat: cannot select SOOD interface")
	}
	joined := map[int]bool{networks[0].iface.Index: true}
	for _, network := range networks[1:] {
		if joined[network.iface.Index] {
			continue
		}
		if err := packetConn.JoinGroup(&network.iface, group); err != nil {
			return nil, errors.New("raat: cannot join SOOD multicast group")
		}
		joined[network.iface.Index] = true
	}
	searchCtx, cancel := context.WithTimeout(ctx, soodWindow)
	defer cancel()
	deadline, _ := searchCtx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, errors.New("raat: cannot set SOOD deadline")
	}
	stop := context.AfterFunc(searchCtx, func() { _ = conn.Close() })
	defer stop()
	query := soodQuery()
	sent := false
	for _, network := range networks {
		if _, err := packetConn.WriteTo(query, &ipv4.ControlMessage{IfIndex: network.iface.Index, Src: net.IP(network.local.AsSlice())}, group); err == nil {
			sent = true
		}
	}
	if !sent {
		return nil, errors.New("raat: SOOD query failed")
	}
	buffer := make([]byte, maxSOODPacket+1)
	found := make(map[endpoint]soodAdvertisement)
	for {
		n, control, source, err := packetConn.ReadFrom(buffer)
		if err != nil {
			if searchCtx.Err() != nil || time.Now().After(deadline) {
				break
			}
			return nil, errors.New("raat: SOOD receive failed")
		}
		udp, ok := source.(*net.UDPAddr)
		if !ok || control == nil || n > maxSOODPacket {
			continue
		}
		address, ok := netip.AddrFromSlice(udp.IP)
		if !ok {
			continue
		}
		address = address.Unmap()
		for _, network := range networks {
			if network.iface.Index != control.IfIndex {
				continue
			}
			advert, ok := advertisementFromSOOD(buffer[:n], address, network)
			if ok && len(found) < maxDiscoveryEndpoints {
				found[advert.endpoint] = advert
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	values := make([]soodAdvertisement, 0, len(found))
	for _, advert := range found {
		values = append(values, advert)
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].endpoint.address != values[j].endpoint.address {
			return values[i].endpoint.address < values[j].endpoint.address
		}
		if values[i].endpoint.port != values[j].endpoint.port {
			return values[i].endpoint.port < values[j].endpoint.port
		}
		return values[i].endpoint.localAddress < values[j].endpoint.localAddress
	})
	return values, nil
}

func parseDeviceInfo(payload []byte) (deviceInfo, error) {
	var response struct {
		Status string `json:"status"`
		Info   struct {
			UniqueID        string          `json:"unique_id"`
			Protocol        string          `json:"protocol_version"`
			CodeSigningKeys json.RawMessage `json:"code_signing_keys"`
			Name            string          `json:"name"`
			Model           string          `json:"model"`
			Vendor          string          `json:"vendor"`
		} `json:"info"`
	}
	if len(payload) > maxControlBody || !utf8.Valid(payload) || json.Unmarshal(payload, &response) != nil || response.Status != "Success" {
		return deviceInfo{}, errors.New("raat: invalid device information")
	}
	info := response.Info
	if strings.TrimSpace(info.UniqueID) == "" || !validDiscoveryText(info.UniqueID, 4096) || !validDiscoveryText(info.Protocol, 128) ||
		!validDiscoveryText(info.Name, 512) || !validDiscoveryText(info.Model, 512) || !validDiscoveryText(info.Vendor, 512) {
		return deviceInfo{}, errors.New("raat: invalid device identity")
	}
	return deviceInfo{uniqueID: info.UniqueID, protocol: info.Protocol, codeSigningKeys: info.CodeSigningKeys, name: info.Name, model: info.Model, manufacturer: info.Vendor}, nil
}

func inspectAdvertisement(ctx context.Context, advert soodAdvertisement) (discoveredDevice, error) {
	infoCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	client, err := newControlClient(infoCtx, advert.endpoint)
	if err != nil {
		return discoveredDevice{}, err
	}
	defer client.Close()
	payload, err := client.call(infoCtx, managementRequest, []byte(`{"request":"get_info"}`))
	if err != nil {
		return discoveredDevice{}, err
	}
	info, err := parseDeviceInfo(payload)
	if err != nil {
		return discoveredDevice{}, err
	}
	name := strings.TrimSpace(info.name)
	if name == "" {
		name = strings.TrimSpace(advert.name)
	}
	if name == "" {
		name = strings.TrimSpace(info.manufacturer + " " + info.model)
	}
	if name == "" {
		name = "RAAT output"
	}
	hash := sha256.Sum256([]byte(info.uniqueID))
	device := output.Device{
		ID: "raat:" + hex.EncodeToString(hash[:]), Name: name, Model: info.model, Manufacturer: info.manufacturer,
		Address: advert.endpoint.address, LocalAddress: advert.endpoint.localAddress, Protocol: "raat",
		Online: true, LastSeen: time.Now().UTC().Format(time.RFC3339Nano),
		Capabilities: output.Capabilities{Play: true, Pause: true, Stop: true, Seek: true},
	}
	return discoveredDevice{device: device, endpoint: advert.endpoint, info: info}, nil
}

func discover(ctx context.Context, interfaces []string) ([]discoveredDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	networks, err := resolveNetworks(interfaces)
	if err != nil {
		return nil, err
	}
	searchCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	adverts, err := searchSOOD(searchCtx, networks)
	if err != nil {
		return nil, err
	}
	results := make([]discoveredDevice, len(adverts))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(8, len(adverts)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				if value, err := inspectAdvertisement(searchCtx, adverts[index]); err == nil {
					results[index] = value
				}
			}
		}()
	}
	for index := range adverts {
		select {
		case jobs <- index:
		case <-searchCtx.Done():
		}
	}
	close(jobs)
	workers.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	unique := make(map[string]bool)
	devices := make([]discoveredDevice, 0, len(results))
	for _, value := range results {
		if value.device.ID == "" || unique[value.device.ID] {
			continue
		}
		unique[value.device.ID] = true
		devices = append(devices, value)
	}
	if len(devices) == 0 && searchCtx.Err() != nil {
		return nil, fmt.Errorf("raat: discovery deadline: %w", searchCtx.Err())
	}
	return devices, nil
}
