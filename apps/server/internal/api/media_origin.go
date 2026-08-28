package api

import (
	"crypto/x509"
	"net"
	"net/netip"
	"net/url"
	"strconv"

	"github.com/jastreamer/jastreamer-server/internal/media"
)

// ServerHTTPSOrigin is a trusted, certificate-bound private HTTPS listener origin.
type ServerHTTPSOrigin struct {
	value string
}

func (origin ServerHTTPSOrigin) String() string { return origin.value }

func NewServerHTTPSOrigin(baseURL, listenerAddress string, certificate *x509.Certificate) (ServerHTTPSOrigin, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return ServerHTTPSOrigin{}, media.ErrInvalidConfig
	}
	originHost, originPort, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return ServerHTTPSOrigin{}, media.ErrInvalidConfig
	}
	originAddress, err := netip.ParseAddr(originHost)
	if err != nil || parsed.Host != net.JoinHostPort(originAddress.String(), originPort) || (!originAddress.IsPrivate() && !originAddress.IsLoopback()) {
		return ServerHTTPSOrigin{}, media.ErrInvalidConfig
	}
	port, err := strconv.ParseUint(originPort, 10, 16)
	if err != nil || port == 0 {
		return ServerHTTPSOrigin{}, media.ErrInvalidConfig
	}
	listenerHost, listenerPort, err := net.SplitHostPort(listenerAddress)
	if err != nil || listenerPort != originPort {
		return ServerHTTPSOrigin{}, media.ErrInvalidConfig
	}
	listenerAddressIP, err := netip.ParseAddr(listenerHost)
	if err != nil || (!listenerAddressIP.IsUnspecified() && listenerAddressIP != originAddress) {
		return ServerHTTPSOrigin{}, media.ErrInvalidConfig
	}
	if certificate == nil || certificate.VerifyHostname(originAddress.String()) != nil {
		return ServerHTTPSOrigin{}, media.ErrInvalidConfig
	}
	return ServerHTTPSOrigin{value: parsed.String()}, nil
}
