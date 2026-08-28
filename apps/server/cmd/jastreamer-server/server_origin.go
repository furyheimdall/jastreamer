package main

import (
	"crypto/x509"
	"fmt"
	"net"
	"net/netip"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/media"
)

type localInterfaceAddress struct {
	Name    string
	Address netip.Addr
}

type interfaceAddressEnumerator func() ([]localInterfaceAddress, error)

type serverHTTPSOriginConfig struct {
	Listener      net.Addr
	Certificate   *x509.Certificate
	K17Interfaces []string
	Enumerate     interfaceAddressEnumerator
}

func serverHTTPSOriginFromListener(config serverHTTPSOriginConfig) (api.ServerHTTPSOrigin, error) {
	host, port, err := net.SplitHostPort(config.Listener.String())
	if err != nil {
		return api.ServerHTTPSOrigin{}, fmt.Errorf("parse server HTTPS listener: %w", media.ErrInvalidConfig)
	}
	listenerIP, err := netip.ParseAddr(host)
	if err != nil {
		return api.ServerHTTPSOrigin{}, fmt.Errorf("parse server HTTPS listener: %w", media.ErrInvalidConfig)
	}
	if !listenerIP.IsUnspecified() {
		origin, originErr := api.NewServerHTTPSOrigin("https://"+net.JoinHostPort(listenerIP.String(), port), config.Listener.String(), config.Certificate)
		if originErr != nil {
			return api.ServerHTTPSOrigin{}, fmt.Errorf("bind server HTTPS origin to listener: %w", originErr)
		}
		return origin, nil
	}
	if config.Certificate == nil || config.Enumerate == nil {
		return api.ServerHTTPSOrigin{}, fmt.Errorf("select server HTTPS certificate identity: %w", media.ErrInvalidConfig)
	}
	localAddresses, err := config.Enumerate()
	if err != nil {
		return api.ServerHTTPSOrigin{}, fmt.Errorf("enumerate local interface addresses: %w", err)
	}
	allowedInterfaces := make(map[string]struct{}, len(config.K17Interfaces))
	for _, name := range config.K17Interfaces {
		allowedInterfaces[name] = struct{}{}
	}
	listenerIsIPv4 := listenerIP.Is4()
	assigned := make(map[netip.Addr]struct{}, len(localAddresses))
	for _, local := range localAddresses {
		_, allowed := allowedInterfaces[local.Name]
		if local.Address.Is4In6() || local.Address.Is4() != listenerIsIPv4 || (len(allowedInterfaces) != 0 && !allowed) {
			continue
		}
		assigned[local.Address] = struct{}{}
	}
	privateCandidates := make([]netip.Addr, 0, len(config.Certificate.IPAddresses))
	loopbackCandidates := make([]netip.Addr, 0, len(config.Certificate.IPAddresses))
	for _, raw := range config.Certificate.IPAddresses {
		address, valid := netip.AddrFromSlice(raw)
		if !valid || address.Is4In6() || address.Is4() != listenerIsIPv4 {
			continue
		}
		if address.IsPrivate() {
			privateCandidates = append(privateCandidates, address)
		} else if address.IsLoopback() {
			loopbackCandidates = append(loopbackCandidates, address)
		}
	}
	for _, address := range append(privateCandidates, loopbackCandidates...) {
		if _, present := assigned[address]; !present {
			continue
		}
		origin, originErr := api.NewServerHTTPSOrigin("https://"+net.JoinHostPort(address.String(), port), config.Listener.String(), config.Certificate)
		if originErr != nil {
			return api.ServerHTTPSOrigin{}, fmt.Errorf("bind server HTTPS origin to assigned certificate identity: %w", originErr)
		}
		return origin, nil
	}
	return api.ServerHTTPSOrigin{}, fmt.Errorf("select assigned private server HTTPS certificate identity: %w", media.ErrInvalidConfig)
}

func systemInterfaceAddresses() ([]localInterfaceAddress, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	result := make([]localInterfaceAddress, 0)
	for _, networkInterface := range interfaces {
		addresses, addressErr := networkInterface.Addrs()
		if addressErr != nil {
			return nil, fmt.Errorf("list addresses for interface %s: %w", networkInterface.Name, addressErr)
		}
		for _, raw := range addresses {
			prefix, parseErr := netip.ParsePrefix(raw.String())
			if parseErr != nil {
				return nil, fmt.Errorf("parse address for interface %s: %w", networkInterface.Name, parseErr)
			}
			address := prefix.Addr()
			if address.Is4In6() {
				continue
			}
			result = append(result, localInterfaceAddress{Name: networkInterface.Name, Address: address})
		}
	}
	return result, nil
}
