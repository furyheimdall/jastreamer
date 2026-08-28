package upnp

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
	"strings"
	"time"
)

const maxSSDPPacket = 64 * 1024

type DiscoveryConfig struct {
	Networks       []Network
	SearchAddress  string
	ResponseWindow time.Duration
}

type Discoverer struct{ config DiscoveryConfig }

func NewDiscoverer(config DiscoveryConfig) (*Discoverer, error) {
	if len(config.Networks) == 0 || config.ResponseWindow <= 0 {
		return nil, ErrInvalidConfig
	}
	for _, network := range config.Networks {
		if network.Name == "" || !allowedDiscoveryAddress(network.LocalIP) || !allowedDiscoverySubnet(network.Subnet) || !network.Subnet.Contains(network.LocalIP) {
			return nil, ErrInvalidConfig
		}
	}
	if config.SearchAddress == "" {
		config.SearchAddress = "239.255.255.250:1900"
	}
	if _, err := net.ResolveUDPAddr("udp4", config.SearchAddress); err != nil {
		return nil, fmt.Errorf("resolve SSDP search address: %w", ErrInvalidConfig)
	}
	return &Discoverer{config: config}, nil
}

func ResolveNetworks(names []string) ([]Network, error) {
	if len(names) == 0 {
		return nil, ErrInvalidConfig
	}
	result := make([]Network, 0, len(names))
	for _, name := range names {
		iface, err := net.InterfaceByName(name)
		if err != nil {
			return nil, fmt.Errorf("resolve UPnP interface %q: %w", name, err)
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, fmt.Errorf("read UPnP interface %q: %w", name, err)
		}
		for _, raw := range addresses {
			prefix, err := netip.ParsePrefix(raw.String())
			if err != nil {
				continue
			}
			network, err := NewNetwork(name, prefix.Addr().String(), prefix.String())
			if err == nil {
				result = append(result, network)
			}
		}
	}
	if len(result) == 0 {
		return nil, ErrInvalidConfig
	}
	return result, nil
}

func (discoverer *Discoverer) Discover(ctx context.Context) ([]Candidate, error) {
	target, err := net.ResolveUDPAddr("udp4", discoverer.config.SearchAddress)
	if err != nil {
		return nil, fmt.Errorf("resolve SSDP target: %w", err)
	}
	unique := map[string]Candidate{}
	for _, network := range discoverer.config.Networks {
		candidates, err := discoverer.searchNetwork(ctx, network, target)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			unique[candidate.USN+"\x00"+candidate.Location] = candidate
		}
	}
	result := make([]Candidate, 0, len(unique))
	for _, candidate := range unique {
		result = append(result, candidate)
	}
	return result, nil
}

func (discoverer *Discoverer) searchNetwork(ctx context.Context, network Network, target *net.UDPAddr) ([]Candidate, error) {
	connection, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IP(network.LocalIP.AsSlice()), Port: 0})
	if err != nil {
		return nil, fmt.Errorf("bind SSDP interface %s: %w", network.Name, err)
	}
	defer connection.Close()
	stopCancellation := context.AfterFunc(ctx, func() {
		_ = connection.SetReadDeadline(time.Now())
	})
	defer stopCancellation()
	deadline := time.Now().Add(discoverer.config.ResponseWindow)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("set SSDP deadline: %w", err)
	}
	request := "M-SEARCH * HTTP/1.1\r\nHOST: " + discoverer.config.SearchAddress + "\r\nMAN: \"ssdp:discover\"\r\nMX: 1\r\nST: " + mediaRendererTarget + "\r\n\r\n"
	if _, err := connection.WriteToUDP([]byte(request), target); err != nil {
		return nil, fmt.Errorf("send SSDP search on %s: %w", network.Name, err)
	}
	result := []Candidate{}
	buffer := make([]byte, maxSSDPPacket)
	for {
		count, source, readErr := connection.ReadFromUDPAddrPort(buffer)
		if readErr != nil {
			if timeout, ok := readErr.(net.Error); ok && timeout.Timeout() {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return result, nil
			}
			return nil, fmt.Errorf("receive SSDP on %s: %w", network.Name, readErr)
		}
		candidate, parseErr := ParseAdvertisement(buffer[:count], source, []Network{network})
		if parseErr == nil {
			result = append(result, candidate)
		}
	}
}

func ParseAdvertisement(data []byte, source netip.AddrPort, networks []Network) (Candidate, error) {
	if len(data) == 0 || len(data) > maxSSDPPacket || !source.Addr().Is4() {
		return Candidate{}, ErrUntrustedAdvertisement
	}
	response, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(data)), &http.Request{Method: http.MethodGet})
	if err != nil {
		return Candidate{}, ErrUntrustedAdvertisement
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	location := response.Header.Get("Location")
	parsed, parseErr := url.Parse(location)
	locationIP, addressErr := netip.ParseAddr(parsed.Hostname())
	trusted := false
	for _, network := range networks {
		trusted = trusted || network.Subnet.Contains(source.Addr())
	}
	if response.StatusCode != http.StatusOK || !strings.EqualFold(response.Header.Get("ST"), mediaRendererTarget) || response.Header.Get("USN") == "" || parseErr != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.Host == "" || parsed.Fragment != "" || addressErr != nil || locationIP != source.Addr() || !trusted {
		return Candidate{}, ErrUntrustedAdvertisement
	}
	return Candidate{Source: source, Location: parsed.String(), USN: response.Header.Get("USN")}, nil
}
