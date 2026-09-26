package dlna

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	ssdpAddress    = "239.255.255.250:1900"
	ssdpPort       = 1900
	maxSSDPPacket  = 64 << 10
	responseWindow = 1500 * time.Millisecond
	// probeWindow bounds one unicast address probe, and probeResend repeats the search
	// once inside that window because SSDP runs over UDP.
	probeWindow = 1500 * time.Millisecond
	probeResend = 500 * time.Millisecond
	// maxProbeReadFailures bounds non-timeout socket errors inside one probe round.
	maxProbeReadFailures = 8
	defaultMaxAge        = 180 * time.Second
	minimumMaxAge        = 5 * time.Second
	maximumMaxAge        = 24 * time.Hour
)

type localNetwork struct {
	name   string
	index  int
	local  netip.Addr
	prefix netip.Prefix
}

// maxDeviceAddresses bounds the addresses recorded for one renderer. A renderer may
// answer SSDP from one address and publish its description on another, so a device is
// tracked as a small set rather than a single address.
const maxDeviceAddresses = 8

// addressSet is an ordered, de-duplicated and bounded list of device addresses.
type addressSet []netip.Addr

func (set addressSet) contains(address netip.Addr) bool {
	return slices.Contains(set, address)
}

// with returns a set extended by one address. The result never shares storage with
// the receiver, so a recorded set cannot be mutated through a copy.
func (set addressSet) with(address netip.Addr) addressSet {
	if !address.IsValid() || set.contains(address) || len(set) >= maxDeviceAddresses {
		return set
	}
	return append(slices.Clip(set), address)
}

func (set addressSet) merge(other addressSet) addressSet {
	for _, address := range other {
		set = set.with(address)
	}
	return set
}

func (set addressSet) strings() []string {
	values := make([]string, 0, len(set))
	for _, address := range set {
		values = append(values, address.String())
	}
	return values
}

// endpointScope names the addresses the server may contact for one renderer: the
// address its SSDP response came from and the address its LOCATION points at.
type endpointScope struct {
	allowed addressSet
	network localNetwork
}

type advertisement struct {
	source       netip.AddrPort
	locationAddr netip.Addr
	network      localNetwork
	location     string
	udn          string
	maxAge       time.Duration
	bootID       string
	configID     string
	nextBoot     string
	kind         string
}

func (value advertisement) scope() endpointScope {
	return endpointScope{
		allowed: addressSet{}.with(value.source.Addr()).with(value.locationAddr),
		network: value.network,
	}
}

type discoveryClient interface {
	Search(context.Context, []localNetwork) ([]advertisement, error)
}

type ssdpDiscoverer struct{}

// addressProbe finds the other addresses one renderer answers on. A renderer with
// several addresses on one interface may notify from one address and answer a unicast
// search from another, so the addresses it will fetch media from are only complete
// after asking every address already known for it.
type addressProbe interface {
	Probe(ctx context.Context, network localNetwork, targets addressSet, udn string) addressSet
}

// ssdpProbe searches the SSDP port of each known renderer address.
type ssdpProbe struct {
	port uint16
}

func resolveNetworks(names []string) ([]localNetwork, error) {
	requested := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, ErrInvalidConfig
		}
		requested[name] = struct{}{}
	}
	var interfaces []net.Interface
	var err error
	if len(requested) == 0 {
		interfaces, err = net.Interfaces()
	} else {
		interfaces = make([]net.Interface, 0, len(requested))
		for name := range requested {
			value, lookupErr := net.InterfaceByName(name)
			if lookupErr != nil {
				return nil, fmt.Errorf("dlna: resolve interface %q: %w", name, ErrInvalidConfig)
			}
			interfaces = append(interfaces, *value)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("dlna: list interfaces: %w", err)
	}
	result := make([]localNetwork, 0, len(interfaces))
	seen := make(map[string]struct{})
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		automatic := len(requested) == 0
		if automatic && iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, addressErr := iface.Addrs()
		if addressErr != nil {
			if automatic {
				continue
			}
			return nil, fmt.Errorf("dlna: inspect interface %q: %w", iface.Name, addressErr)
		}
		for _, raw := range addresses {
			prefix, parseErr := netip.ParsePrefix(raw.String())
			if parseErr != nil || !SupportsNetworkAddress(prefix) {
				continue
			}
			key := iface.Name + "\x00" + prefix.Addr().String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, localNetwork{name: iface.Name, index: iface.Index, local: prefix.Addr(), prefix: prefix.Masked()})
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("dlna: no private IPv4 multicast interface: %w", ErrInvalidConfig)
	}
	return result, nil
}

func allowedLocalAddress(address netip.Addr) bool {
	return address.Is4() && (address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLoopback())
}

// SupportsNetworkAddress reports whether an interface prefix is permitted for DLNA discovery and media.
func SupportsNetworkAddress(prefix netip.Prefix) bool {
	return prefix.IsValid() && allowedLocalAddress(prefix.Addr()) && allowedLocalPrefix(prefix.Masked())
}

func allowedLocalPrefix(prefix netip.Prefix) bool {
	for _, scope := range [...]netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("127.0.0.0/8"),
	} {
		if prefix.Bits() >= scope.Bits() && scope.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func (ssdpDiscoverer) Search(ctx context.Context, networks []localNetwork) ([]advertisement, error) {
	type searchResult struct {
		values []advertisement
		err    error
	}
	results := make(chan searchResult, len(networks))
	for _, network := range networks {
		network := network
		go func() {
			values, err := searchSSDPNetwork(ctx, network)
			results <- searchResult{values: values, err: err}
		}()
	}
	unique := make(map[string]advertisement)
	failures := 0
	var firstErr error
	for range networks {
		result := <-results
		if result.err != nil {
			failures++
			if firstErr == nil {
				firstErr = result.err
			}
			continue
		}
		for _, value := range result.values {
			unique[value.udn+"\x00"+value.location] = value
		}
	}
	if failures == len(networks) {
		return nil, firstErr
	}
	values := make([]advertisement, 0, len(unique))
	for _, value := range unique {
		values = append(values, value)
	}
	return values, nil
}

func searchSSDPNetwork(ctx context.Context, network localNetwork) ([]advertisement, error) {
	target, err := net.ResolveUDPAddr("udp4", ssdpAddress)
	if err != nil {
		return nil, fmt.Errorf("dlna: resolve SSDP multicast: %w", err)
	}
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IP(network.local.AsSlice())})
	if err != nil {
		return nil, fmt.Errorf("dlna: bind SSDP interface %q: %w", network.name, err)
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.SetReadDeadline(time.Now()) })
	defer stop()
	deadline := time.Now().Add(responseWindow)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("dlna: set SSDP deadline: %w", err)
	}
	request := "M-SEARCH * HTTP/1.1\r\nHOST: " + ssdpAddress + "\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: " + mediaRendererTarget + "\r\n\r\n"
	if _, err := connection.WriteToUDP([]byte(request), target); err != nil {
		return nil, fmt.Errorf("dlna: send SSDP search on %q: %w", network.name, err)
	}
	buffer := make([]byte, maxSSDPPacket)
	values := make([]advertisement, 0)
	for {
		count, source, readErr := connection.ReadFromUDPAddrPort(buffer)
		if readErr != nil {
			if timeout, ok := readErr.(net.Error); ok && timeout.Timeout() {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return values, nil
			}
			return nil, fmt.Errorf("dlna: receive SSDP on %q: %w", network.name, readErr)
		}
		value, parseErr := parseSearchResponse(buffer[:count], source, network)
		if parseErr == nil {
			values = append(values, value)
		}
	}
}

// Probe asks every address already known for one renderer, over unicast M-SEARCH on
// the interface that discovered it, and returns the addresses that answered for the
// same UDN. UDA 1.1 unicast searches carry no MX, and the search is repeated once
// inside the bounded window because SSDP is unreliable.
func (probe ssdpProbe) Probe(ctx context.Context, network localNetwork, targets addressSet, udn string) addressSet {
	if len(targets) == 0 || udn == "" {
		return nil
	}
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IP(network.local.AsSlice())})
	if err != nil {
		return nil
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.SetReadDeadline(time.Now()) })
	defer stop()
	deadline := time.Now().Add(probeWindow)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil
	}
	found := addressSet{}
	buffer := make([]byte, maxSSDPPacket)
	// One search, one bounded retry: SSDP runs over UDP and a renderer that answers
	// from a second address may drop either datagram.
	for round := range 2 {
		sent := 0
		for _, target := range targets {
			if probe.sendSearch(connection, target) {
				sent++
			}
		}
		if sent == 0 {
			return found
		}
		until := deadline
		if round == 0 {
			if early := time.Now().Add(probeResend); early.Before(until) {
				until = early
			}
		}
		complete, stopped := collectProbeReplies(connection, network, udn, until, buffer, &found)
		if complete || stopped || !time.Now().Before(deadline) {
			return found
		}
	}
	return found
}

// collectProbeReplies reads matching answers until the round ends. It reports whether
// the address set is full and whether reading stopped before the round ended.
func collectProbeReplies(connection *net.UDPConn, network localNetwork, udn string, until time.Time, buffer []byte, found *addressSet) (bool, bool) {
	if err := connection.SetReadDeadline(until); err != nil {
		return false, true
	}
	failures := 0
	for {
		count, source, err := connection.ReadFromUDPAddrPort(buffer)
		if err != nil {
			// A closed port on one target address must not end the round: only a
			// timeout or repeated socket failures do.
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				return false, false
			}
			failures++
			if failures >= maxProbeReadFailures {
				return false, true
			}
			continue
		}
		value, parseErr := parseSearchResponse(buffer[:count], source, network)
		if parseErr != nil || value.udn != udn {
			continue
		}
		*found = found.with(source.Addr())
		if len(*found) >= maxDeviceAddresses {
			return true, false
		}
	}
}

func (probe ssdpProbe) sendSearch(connection *net.UDPConn, target netip.Addr) bool {
	if !target.Is4() || !allowedLocalAddress(target) {
		return false
	}
	host := netip.AddrPortFrom(target, probe.port)
	request := "M-SEARCH * HTTP/1.1\r\nHOST: " + host.String() + "\r\nMAN: \"ssdp:discover\"\r\nST: " + mediaRendererTarget + "\r\n\r\n"
	_, err := connection.WriteToUDPAddrPort([]byte(request), host)
	return err == nil
}

func parseSearchResponse(data []byte, source netip.AddrPort, network localNetwork) (advertisement, error) {
	if len(data) == 0 || len(data) > maxSSDPPacket {
		return advertisement{}, ErrInvalidResponse
	}
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(data)), &http.Request{Method: http.MethodGet})
	if err != nil {
		return advertisement{}, ErrInvalidResponse
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK || !isMediaRendererType(response.Header.Get("ST")) {
		return advertisement{}, ErrInvalidResponse
	}
	return advertisementFromHeaders(source, network, response.Header, "alive")
}

func parseNotification(data []byte, source netip.AddrPort, network localNetwork) (advertisement, error) {
	if len(data) == 0 || len(data) > maxSSDPPacket {
		return advertisement{}, ErrInvalidResponse
	}
	request, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(data)))
	if err != nil || request.Method != "NOTIFY" {
		return advertisement{}, ErrInvalidResponse
	}
	defer request.Body.Close()
	nt := strings.TrimSpace(request.Header.Get("NT"))
	usn := strings.TrimSpace(request.Header.Get("USN"))
	if !isMediaRendererType(nt) && !isMediaRendererUSN(usn) {
		return advertisement{}, ErrInvalidResponse
	}
	kind := strings.ToLower(strings.TrimSpace(request.Header.Get("NTS")))
	switch kind {
	case "ssdp:alive", "ssdp:update":
		kind = strings.TrimPrefix(kind, "ssdp:")
		return advertisementFromHeaders(source, network, request.Header, kind)
	case "ssdp:byebye":
		udn := udnFromUSN(usn)
		if udn == "" || !network.prefix.Contains(source.Addr()) {
			return advertisement{}, ErrInvalidResponse
		}
		return advertisement{source: source, network: network, udn: udn, kind: "byebye"}, nil
	default:
		return advertisement{}, ErrInvalidResponse
	}
}

func isMediaRendererUSN(usn string) bool {
	index := strings.Index(usn, "::")
	return index >= 0 && isMediaRendererType(usn[index+2:])
}

func isMediaRendererType(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) <= len(mediaRendererPrefix) || !strings.EqualFold(value[:len(mediaRendererPrefix)], mediaRendererPrefix) {
		return false
	}
	for _, char := range value[len(mediaRendererPrefix):] {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func advertisementFromHeaders(source netip.AddrPort, network localNetwork, header http.Header, kind string) (advertisement, error) {
	udn := udnFromUSN(header.Get("USN"))
	if udn == "" || !network.prefix.Contains(source.Addr()) {
		return advertisement{}, ErrInvalidResponse
	}
	location, locationAddr, err := parseDeviceURL(header.Get("Location"), network)
	if err != nil {
		return advertisement{}, ErrInvalidResponse
	}
	return advertisement{
		source: source, locationAddr: locationAddr, network: network, location: location, udn: udn,
		maxAge:   parseMaxAge(header.Get("Cache-Control")),
		bootID:   strings.TrimSpace(header.Get("BOOTID.UPNP.ORG")),
		configID: strings.TrimSpace(header.Get("CONFIGID.UPNP.ORG")),
		nextBoot: strings.TrimSpace(header.Get("NEXTBOOTID.UPNP.ORG")), kind: kind,
	}, nil
}

func udnFromUSN(raw string) string {
	value := strings.TrimSpace(raw)
	if index := strings.Index(value, "::"); index >= 0 {
		value = value[:index]
	}
	if len(value) < len("uuid:") || !strings.EqualFold(value[:len("uuid:")], "uuid:") {
		return ""
	}
	return strings.ToLower(value)
}

func parseMaxAge(raw string) time.Duration {
	for _, part := range strings.Split(raw, ",") {
		name, value, found := strings.Cut(strings.TrimSpace(part), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(name), "max-age") {
			continue
		}
		seconds, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(value), `"`), 10, 64)
		if err != nil || seconds < 0 {
			break
		}
		if seconds == 0 {
			return 0
		}
		if seconds > int64(maximumMaxAge/time.Second) {
			return maximumMaxAge
		}
		duration := time.Duration(seconds) * time.Second
		if duration < minimumMaxAge {
			return minimumMaxAge
		}
		return duration
	}
	return defaultMaxAge
}

// trustedAbsoluteURL accepts only the addresses this renderer already presented.
func trustedAbsoluteURL(raw string, scope endpointScope) (string, netip.Addr, error) {
	parsed, address, err := parseDeviceURL(raw, scope.network)
	if err != nil || !scope.allowed.contains(address) {
		return "", netip.Addr{}, ErrUnavailable
	}
	return parsed, address, nil
}

// parseDeviceURL accepts a device URL that names any address on the discovery network,
// because a renderer with several addresses on one interface answers SSDP from one of
// them and publishes its description on another.
func parseDeviceURL(raw string, network localNetwork) (string, netip.Addr, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" {
		return "", netip.Addr{}, ErrUnavailable
	}
	address, err := netip.ParseAddr(parsed.Hostname())
	if err != nil || !network.prefix.Contains(address) || !allowedLocalAddress(address) {
		return "", netip.Addr{}, ErrUnavailable
	}
	return parsed.String(), address, nil
}
