package airplay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/enbility/zeroconf/v3"
	"github.com/miekg/dns"
)

const (
	raopService    = "_raop._tcp"
	airplayService = "_airplay._tcp"
)

type localNetwork struct {
	iface  net.Interface
	local  netip.Addr
	prefix netip.Prefix
}

type advertisement struct {
	identity         string
	name             string
	address          netip.Addr
	localAddress     netip.Addr
	port             int
	service          string
	host             string
	properties       map[string]string
	pairingRequired  bool
	passwordRequired bool
	model            string
	lastSeen         time.Time
	expiresAt        time.Time
}

type discoverer interface {
	Search(context.Context, []localNetwork) ([]advertisement, error)
}

type mdnsDiscoverer struct{}

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
	if len(requested) == 0 {
		var err error
		interfaces, err = net.Interfaces()
		if err != nil {
			return nil, fmt.Errorf("airplay: list interfaces: %w", err)
		}
	} else {
		interfaces = make([]net.Interface, 0, len(requested))
		for name := range requested {
			iface, err := net.InterfaceByName(name)
			if err != nil {
				return nil, fmt.Errorf("airplay: resolve interface %q: %w", name, ErrInvalidConfig)
			}
			interfaces = append(interfaces, *iface)
		}
	}
	sort.Slice(interfaces, func(i, j int) bool { return interfaces[i].Index < interfaces[j].Index })
	result := make([]localNetwork, 0, len(interfaces))
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
			return nil, fmt.Errorf("airplay: inspect interface %q: %w", iface.Name, err)
		}
		for _, address := range addresses {
			prefix, err := netip.ParsePrefix(address.String())
			if err != nil || !allowedLocalAddress(prefix.Addr()) || !allowedLocalPrefix(prefix.Masked()) {
				continue
			}
			key := iface.Name + "\x00" + prefix.Addr().String()
			if seen[key] {
				continue
			}
			seen[key] = true
			result = append(result, localNetwork{iface: iface, local: prefix.Addr(), prefix: prefix.Masked()})
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("airplay: no private IPv4 multicast interface: %w", ErrInvalidConfig)
	}
	return result, nil
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

func (mdnsDiscoverer) Search(ctx context.Context, networks []localNetwork) ([]advertisement, error) {
	searchCtx, cancel := context.WithTimeout(ctx, discoveryWindow)
	defer cancel()
	interfaces := make([]net.Interface, 0, len(networks))
	seenInterfaces := make(map[int]bool)
	for _, network := range networks {
		if !seenInterfaces[network.iface.Index] {
			seenInterfaces[network.iface.Index] = true
			interfaces = append(interfaces, network.iface)
		}
	}
	type browseResult struct {
		values []advertisement
		err    error
	}
	results := make(chan browseResult, 2)
	for _, service := range []string{raopService, airplayService} {
		service := service
		go func() {
			entries := make(chan *zeroconf.ServiceEntry)
			removed := make(chan *zeroconf.ServiceEntry)
			errs := make(chan error, 1)
			go func() {
				errs <- zeroconf.Browse(searchCtx, service, "local.", entries, removed, zeroconf.SelectIfaces(interfaces), zeroconf.SelectIPTraffic(zeroconf.IPv4))
			}()
			current := make(map[string]advertisement)
			for {
				select {
				case entry, open := <-entries:
					if !open {
						entries = nil
						continue
					}
					if value, ok := advertisementFromEntry(entry, networks, time.Now()); ok {
						current[strings.ToLower(entry.Instance)] = value
					}
				case entry, open := <-removed:
					if !open {
						removed = nil
						continue
					}
					if entry != nil {
						delete(current, strings.ToLower(entry.Instance))
					}
				case err := <-errs:
					values := make([]advertisement, 0, len(current))
					for _, value := range current {
						values = append(values, value)
					}
					results <- browseResult{values: values, err: err}
					return
				}
			}
		}()
	}
	unique := make(map[string]advertisement)
	var firstErr error
	failures := 0
	for range 2 {
		result := <-results
		if result.err != nil && !errors.Is(result.err, context.Canceled) && !errors.Is(result.err, context.DeadlineExceeded) {
			failures++
			if firstErr == nil {
				firstErr = result.err
			}
		}
		for _, value := range result.values {
			key := value.identity + "\x00" + value.service + "\x00" + value.address.String() + "\x00" + strconv.Itoa(value.port)
			unique[key] = value
		}
	}
	if failures == 2 && len(unique) == 0 {
		return nil, fmt.Errorf("airplay: mDNS discovery failed: %w", firstErr)
	}
	values := make([]advertisement, 0, len(unique))
	for _, value := range unique {
		values = append(values, value)
	}
	return values, nil
}

func advertisementFromEntry(entry *zeroconf.ServiceEntry, networks []localNetwork, now time.Time) (advertisement, bool) {
	if entry == nil || entry.Port < 1 || entry.Port > 65535 {
		return advertisement{}, false
	}
	var selected netip.Addr
	var scope localNetwork
	for _, raw := range entry.AddrIPv4 {
		address, ok := netip.AddrFromSlice(raw)
		if !ok || !allowedLocalAddress(address.Unmap()) {
			continue
		}
		address = address.Unmap()
		for _, network := range networks {
			if network.prefix.Contains(address) {
				selected, scope = address, network
				break
			}
		}
		if selected.IsValid() {
			break
		}
	}
	if !selected.IsValid() {
		return advertisement{}, false
	}
	properties := parseTXT(entry.Text)
	identity := receiverIdentity(entry.Instance, entry.HostName, selected, properties)
	if identity == "" {
		return advertisement{}, false
	}
	name := strings.TrimSpace(unescapeInstanceName(entry.Instance))
	if strings.EqualFold(strings.TrimSpace(entry.Service), raopService) {
		if index := strings.IndexByte(name, '@'); index >= 0 && index+1 < len(name) {
			name = name[index+1:]
		}
	}
	if name == "" {
		name = strings.TrimSuffix(entry.HostName, ".")
	}
	expires := entry.Expiry
	if expires.IsZero() || !expires.After(now) {
		expires = now.Add(defaultDiscoveryInterval * 3)
	}
	flags := parseHex(properties["sf"])
	if flags == 0 {
		flags = parseHex(properties["flags"])
	}
	return advertisement{
		identity: identity, name: name, address: selected, localAddress: scope.local,
		port: entry.Port, service: strings.TrimSuffix(strings.TrimSpace(entry.Service), "."),
		host: strings.TrimSuffix(entry.HostName, "."), properties: properties,
		pairingRequired:  flags&(0x8|0x200) != 0,
		passwordRequired: strings.EqualFold(properties["pw"], "true") || flags&0x80 != 0,
		model:            firstNonEmpty(properties["am"], properties["model"]), lastSeen: now, expiresAt: expires,
	}, true
}

func unescapeInstanceName(value string) string {
	if !strings.ContainsRune(value, '\\') {
		return value
	}
	var wire [256]byte
	end, err := dns.PackDomainName(value+".", wire[:], 0, nil, false)
	if err != nil || end != int(wire[0])+2 {
		return value
	}
	return string(wire[1 : end-1])
}

func parseTXT(values []string) map[string]string {
	result := make(map[string]string, len(values))
	for _, value := range values {
		if len(value) > 4096 {
			continue
		}
		key, content, found := strings.Cut(value, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		if !found || key == "" || len(key) > 128 || strings.ContainsRune(key, '\x00') || strings.ContainsRune(content, '\x00') {
			continue
		}
		result[key] = content
	}
	return result
}

func receiverIdentity(instance, host string, address netip.Addr, properties map[string]string) string {
	for _, raw := range []string{properties["deviceid"], properties["pi"]} {
		if value := normalizeHardwareID(raw); value != "" {
			return value
		}
	}
	if prefix, _, ok := strings.Cut(instance, "@"); ok {
		if value := normalizeHardwareID(prefix); value != "" {
			return value
		}
	}
	hash := sha256.Sum256([]byte(strings.ToLower(strings.TrimSuffix(host, ".")) + "\x00" + address.String() + "\x00" + strings.ToLower(instance)))
	return hex.EncodeToString(hash[:16])
}

func normalizeHardwareID(value string) string {
	value = strings.ToLower(strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.TrimSpace(value)))
	if len(value) != 12 && len(value) != 32 && len(value) != 36 {
		return ""
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return ""
		}
	}
	return value
}

func parseHex(value string) uint64 {
	value = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(value), "0x"))
	parsed, _ := strconv.ParseUint(value, 16, 64)
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
