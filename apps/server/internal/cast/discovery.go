package cast

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/enbility/zeroconf/v3"
	"github.com/google/uuid"
)

const (
	castService      = "_googlecast._tcp"
	maxTXTEntry      = 4096
	maxTXTTotal      = 64 << 10
	maxAdvertisement = 512
	minimumExpiry    = 5 * time.Second
	maximumExpiry    = time.Hour
	fallbackExpiry   = 3 * defaultDiscoveryInterval
)

type localNetwork struct {
	iface  net.Interface
	local  netip.Addr
	prefix netip.Prefix
}

type advertisement struct {
	identity     string
	name         string
	model        string
	address      netip.Addr
	localAddress netip.Addr
	port         int
	lastSeen     time.Time
	expiresAt    time.Time
}

type discoverer interface {
	Search(context.Context, []localNetwork) ([]advertisement, error)
}

type mdnsDiscoverer struct{}

func resolveNetworks(names []string) ([]localNetwork, error) {
	requested := make(map[string]struct{}, len(names))
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, ErrInvalidConfig
		}
		requested[name] = struct{}{}
	}
	var interfaces []net.Interface
	if len(requested) == 0 {
		var err error
		interfaces, err = net.Interfaces()
		if err != nil {
			return nil, fmt.Errorf("cast: list interfaces: %w", err)
		}
	} else {
		interfaces = make([]net.Interface, 0, len(requested))
		for name := range requested {
			iface, err := net.InterfaceByName(name)
			if err != nil {
				return nil, fmt.Errorf("cast: resolve interface %q: %w", name, ErrInvalidConfig)
			}
			interfaces = append(interfaces, *iface)
		}
	}
	sort.Slice(interfaces, func(i, j int) bool { return interfaces[i].Index < interfaces[j].Index })
	result := make([]localNetwork, 0, len(interfaces))
	seen := make(map[string]struct{})
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
			return nil, fmt.Errorf("cast: inspect interface %q: %w", iface.Name, err)
		}
		for _, raw := range addresses {
			prefix, err := netip.ParsePrefix(raw.String())
			if err != nil || !allowedLocalAddress(prefix.Addr()) || !allowedLocalPrefix(prefix.Masked()) {
				continue
			}
			key := iface.Name + "\x00" + prefix.Addr().String()
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, localNetwork{iface: iface, local: prefix.Addr(), prefix: prefix.Masked()})
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("cast: no private IPv4 multicast interface: %w", ErrInvalidConfig)
	}
	return result, nil
}

func allowedLocalAddress(address netip.Addr) bool {
	return address.Is4() && (address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLoopback())
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

func (mdnsDiscoverer) Search(ctx context.Context, networks []localNetwork) ([]advertisement, error) {
	searchCtx, cancel := context.WithTimeout(ctx, discoveryWindow)
	defer cancel()
	type result struct {
		values []advertisement
		err    error
	}
	results := make(chan result, len(networks))
	for _, network := range networks {
		network := network
		go func() {
			values, err := browseNetwork(searchCtx, network)
			results <- result{values: values, err: err}
		}()
	}
	unique := make(map[string]advertisement)
	failures := 0
	var firstErr error
	for range networks {
		value := <-results
		if value.err != nil && !errors.Is(value.err, context.Canceled) && !errors.Is(value.err, context.DeadlineExceeded) {
			failures++
			if firstErr == nil {
				firstErr = value.err
			}
		}
		for _, candidate := range value.values {
			key := candidate.identity + "\x00" + candidate.address.String() + "\x00" + candidate.localAddress.String()
			unique[key] = candidate
		}
	}
	if failures == len(networks) && len(unique) == 0 {
		return nil, fmt.Errorf("cast: mDNS discovery failed: %w", firstErr)
	}
	values := make([]advertisement, 0, len(unique))
	for _, value := range unique {
		values = append(values, value)
	}
	return values, nil
}

func browseNetwork(ctx context.Context, network localNetwork) ([]advertisement, error) {
	entries := make(chan *zeroconf.ServiceEntry)
	removed := make(chan *zeroconf.ServiceEntry)
	errs := make(chan error, 1)
	go func() {
		errs <- zeroconf.Browse(ctx, castService, "local.", entries, removed,
			zeroconf.SelectIfaces([]net.Interface{network.iface}),
			zeroconf.SelectIPTraffic(zeroconf.IPv4))
	}()
	current := make(map[string]advertisement)
	for {
		select {
		case entry, open := <-entries:
			if !open {
				entries = nil
				continue
			}
			if value, ok := advertisementFromEntry(entry, network, time.Now()); ok {
				current[advertisementKey(entry)] = value
			}
		case entry, open := <-removed:
			if !open {
				removed = nil
				continue
			}
			if entry != nil {
				delete(current, advertisementKey(entry))
			}
		case err := <-errs:
			values := make([]advertisement, 0, len(current))
			for _, value := range current {
				values = append(values, value)
			}
			return values, err
		}
	}
}

func advertisementKey(entry *zeroconf.ServiceEntry) string {
	return strings.ToLower(entry.Instance) + "\x00" + strings.ToLower(entry.HostName) + "\x00" + fmt.Sprint(entry.Port)
}

func advertisementFromEntry(entry *zeroconf.ServiceEntry, network localNetwork, now time.Time) (advertisement, bool) {
	if entry == nil || entry.Port < 1 || entry.Port > 65535 || len(entry.AddrIPv4) > 128 ||
		!validAdvertisementText(entry.Instance, maxAdvertisement) || !validAdvertisementText(strings.TrimSuffix(entry.HostName, "."), maxAdvertisement) {
		return advertisement{}, false
	}
	properties, ok := parseTXT(entry.Text)
	if !ok {
		return advertisement{}, false
	}
	identity, err := uuid.Parse(strings.TrimSpace(properties["id"]))
	if err != nil || identity == uuid.Nil {
		return advertisement{}, false
	}
	if rawCapabilities, advertised := properties["ca"]; advertised {
		capabilities, parseErr := strconv.ParseUint(strings.TrimSpace(rawCapabilities), 10, 64)
		if parseErr != nil || capabilities&4 == 0 {
			return advertisement{}, false
		}
	}
	var address netip.Addr
	for _, raw := range entry.AddrIPv4 {
		candidate, valid := netip.AddrFromSlice(raw)
		if !valid {
			continue
		}
		candidate = candidate.Unmap()
		if allowedLocalAddress(candidate) && network.prefix.Contains(candidate) {
			address = candidate
			break
		}
	}
	if !address.IsValid() {
		return advertisement{}, false
	}
	name := strings.TrimSpace(unescapeDNSValue(properties["fn"]))
	if name == "" {
		name = strings.TrimSpace(unescapeDNSValue(entry.Instance))
	}
	model := strings.TrimSpace(unescapeDNSValue(properties["md"]))
	if !validAdvertisementText(name, maxAdvertisement) || !validAdvertisementText(model, maxAdvertisement) {
		return advertisement{}, false
	}
	if name == "" {
		name = strings.TrimSuffix(entry.HostName, ".")
	}
	expiresAt := entry.Expiry
	if expiresAt.IsZero() || !expiresAt.After(now) {
		expiresAt = now.Add(fallbackExpiry)
	}
	remaining := expiresAt.Sub(now)
	if remaining < minimumExpiry {
		expiresAt = now.Add(minimumExpiry)
	} else if remaining > maximumExpiry {
		expiresAt = now.Add(maximumExpiry)
	}
	return advertisement{
		identity: identity.String(), name: name, model: model,
		address: address, localAddress: network.local, port: entry.Port,
		lastSeen: now, expiresAt: expiresAt,
	}, true
}

func parseTXT(values []string) (map[string]string, bool) {
	result := make(map[string]string)
	total := 0
	for _, value := range values {
		total += len(value)
		if len(value) > maxTXTEntry || total > maxTXTTotal {
			return nil, false
		}
		key, content, found := strings.Cut(value, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !found || key == "" || len(key) > 128 || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
			continue
		}
		if len(result) >= 128 {
			return nil, false
		}
		result[key] = content
	}
	return result, true
}

func validAdvertisementText(value string, maximum int) bool {
	if len(value) > maximum || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	for _, character := range value {
		if character < 0x20 && character != '\t' {
			return false
		}
	}
	return true
}

func unescapeDNSValue(value string) string {
	if !strings.ContainsRune(value, '\\') {
		return value
	}
	var decoded strings.Builder
	decoded.Grow(len(value))
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			decoded.WriteByte(value[index])
			continue
		}
		if index+1 >= len(value) {
			return "\x00"
		}
		if index+3 < len(value) && value[index+1] >= '0' && value[index+1] <= '9' &&
			value[index+2] >= '0' && value[index+2] <= '9' && value[index+3] >= '0' && value[index+3] <= '9' {
			number := int(value[index+1]-'0')*100 + int(value[index+2]-'0')*10 + int(value[index+3]-'0')
			if number > 255 {
				return "\x00"
			}
			decoded.WriteByte(byte(number))
			index += 3
		} else {
			index++
			decoded.WriteByte(value[index])
		}
	}
	return decoded.String()
}
