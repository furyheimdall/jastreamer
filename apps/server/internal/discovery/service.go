package discovery

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	zeroconf "github.com/enbility/zeroconf/v3"
	"github.com/google/uuid"
)

const (
	Product  = "jastreamer"
	Protocol = 1
)

type Metadata struct {
	Product  string `json:"product"`
	Protocol int    `json:"protocol"`
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
}

type Service struct {
	metadata Metadata
}

func New(ctx context.Context, db *sql.DB, configuredName, version string) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("discovery: database is required")
	}
	name := configuredName
	if name == "" {
		var err error
		name, err = os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("discovery: determine server hostname: %w", err)
		}
		if name == "" {
			return nil, fmt.Errorf("discovery: server hostname is empty")
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: begin identity storage: %w", err)
	}
	defer tx.Rollback()
	const schema = `CREATE TABLE IF NOT EXISTS discovery_identity (
  singleton INTEGER PRIMARY KEY CHECK(singleton = 1),
  id TEXT NOT NULL CHECK(length(id) = 36)
) STRICT;`
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("discovery: initialize identity storage: %w", err)
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT id FROM discovery_identity WHERE singleton = 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		candidate, randomErr := uuid.NewRandom()
		if randomErr != nil {
			return nil, fmt.Errorf("discovery: generate server identity: %w", randomErr)
		}
		id = candidate.String()
		if _, err = tx.ExecContext(ctx, `INSERT INTO discovery_identity(singleton, id) VALUES(1, ?)`, id); err != nil {
			return nil, fmt.Errorf("discovery: persist server identity: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("discovery: load server identity: %w", err)
	}
	parsed, parseErr := uuid.Parse(id)
	if parseErr != nil || parsed.String() != id {
		return nil, fmt.Errorf("discovery: stored server identity is not a canonical UUID")
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("discovery: commit identity storage: %w", err)
	}
	return &Service{metadata: Metadata{Product: Product, Protocol: Protocol, ID: id, Name: name, Version: version}}, nil
}

func (service *Service) Metadata() Metadata {
	return service.metadata
}

func (service *Service) Hostname() string {
	return "jastreamer-" + service.shortID() + ".local"
}

func (service *Service) shortID() string {
	return strings.ReplaceAll(service.metadata.ID, "-", "")[:12]
}

type Advertisement struct {
	server *zeroconf.Server
}

func (advertisement *Advertisement) Close() {
	if advertisement != nil && advertisement.server != nil {
		advertisement.server.Shutdown()
	}
}

func (service *Service) Advertise(listener net.Listener, scheme string, interfaceNames []string) (*Advertisement, error) {
	if scheme != "http" && scheme != "https" {
		return nil, fmt.Errorf("discovery: unsupported listener scheme %q", scheme)
	}
	port, interfaces, addresses, err := advertisementNetwork(listener, interfaceNames)
	if err != nil {
		return nil, err
	}
	text := []string{
		"id=" + service.metadata.ID,
		"name=" + truncateUTF8(service.metadata.Name, 255-len("name=")),
		"version=" + service.metadata.Version,
		"protocol=" + strconv.Itoa(service.metadata.Protocol),
		"scheme=" + scheme,
		"path=/",
	}
	for _, entry := range text {
		if len(entry) > 255 {
			return nil, fmt.Errorf("discovery: TXT entry exceeds 255 bytes")
		}
	}
	shortID := service.shortID()
	server, err := zeroconf.RegisterProxy(
		"Jastreamer-"+shortID,
		"_jastreamer._tcp",
		"local.",
		port,
		strings.TrimSuffix(service.Hostname(), ".local"),
		addresses,
		text,
		interfaces,
	)
	if err != nil {
		return nil, fmt.Errorf("discovery: register DNS-SD service: %w", err)
	}
	return &Advertisement{server: server}, nil
}

func truncateUTF8(value string, maximumBytes int) string {
	if len(value) <= maximumBytes {
		return value
	}
	for maximumBytes > 0 && !utf8.RuneStart(value[maximumBytes]) {
		maximumBytes--
	}
	return value[:maximumBytes]
}

func advertisementNetwork(listener net.Listener, interfaceNames []string) (int, []net.Interface, []string, error) {
	if listener == nil {
		return 0, nil, nil, fmt.Errorf("discovery: listener is required")
	}
	tcpAddress, ok := listener.Addr().(*net.TCPAddr)
	if !ok || tcpAddress.Port < 1 || tcpAddress.Port > 65535 {
		return 0, nil, nil, fmt.Errorf("discovery: listener has no TCP port")
	}
	var bound netip.Addr
	if len(tcpAddress.IP) != 0 {
		if parsed, valid := netip.AddrFromSlice(tcpAddress.IP); valid {
			bound = parsed.Unmap()
		}
	}
	if bound.IsValid() && !bound.IsUnspecified() && (!eligibleAddress(bound) || bound.IsLoopback()) {
		return 0, nil, nil, fmt.Errorf("discovery: listener is not bound to a private non-loopback address")
	}

	interfaces, err := selectedInterfaces(interfaceNames)
	if err != nil {
		return 0, nil, nil, err
	}
	advertisedInterfaces := make([]net.Interface, 0, len(interfaces))
	addresses := make([]string, 0, len(interfaces))
	seen := make(map[netip.Addr]struct{})
	for _, networkInterface := range interfaces {
		if networkInterface.Flags&net.FlagUp == 0 || networkInterface.Flags&net.FlagLoopback != 0 || networkInterface.Flags&net.FlagMulticast == 0 {
			continue
		}
		rawAddresses, addressErr := networkInterface.Addrs()
		if addressErr != nil {
			return 0, nil, nil, fmt.Errorf("discovery: inspect interface %s: %w", networkInterface.Name, addressErr)
		}
		interfaceEligible := false
		for _, raw := range rawAddresses {
			prefix, parseErr := netip.ParsePrefix(raw.String())
			if parseErr != nil {
				continue
			}
			address := prefix.Addr().Unmap()
			if !eligibleAddress(address) ||
				(bound.IsValid() && !bound.IsUnspecified() && address != bound) ||
				(bound.IsValid() && bound.IsUnspecified() && bound.Is4() && !address.Is4()) {
				continue
			}
			interfaceEligible = true
			if _, exists := seen[address]; !exists {
				seen[address] = struct{}{}
				addresses = append(addresses, address.String())
			}
		}
		if interfaceEligible {
			advertisedInterfaces = append(advertisedInterfaces, networkInterface)
		}
	}
	if len(advertisedInterfaces) == 0 || len(addresses) == 0 {
		return 0, nil, nil, fmt.Errorf("discovery: no private non-loopback listener addresses are available on the selected interfaces")
	}
	return tcpAddress.Port, advertisedInterfaces, addresses, nil
}

func selectedInterfaces(names []string) ([]net.Interface, error) {
	if len(names) == 0 {
		interfaces, err := net.Interfaces()
		if err != nil {
			return nil, fmt.Errorf("discovery: list network interfaces: %w", err)
		}
		return interfaces, nil
	}
	interfaces := make([]net.Interface, 0, len(names))
	for _, name := range names {
		networkInterface, err := net.InterfaceByName(name)
		if err != nil {
			return nil, fmt.Errorf("discovery: select interface %s: %w", name, err)
		}
		interfaces = append(interfaces, *networkInterface)
	}
	return interfaces, nil
}

func eligibleAddress(address netip.Addr) bool {
	return address.IsValid() && !address.IsUnspecified() && !address.IsLoopback() && (address.IsPrivate() || address.IsLinkLocalUnicast())
}
