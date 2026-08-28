package upnp

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

var (
	ErrInvalidConfig          = errors.New("upnp: invalid configuration")
	ErrUntrustedAdvertisement = errors.New("upnp: untrusted advertisement")
	ErrOffSubnetURL           = errors.New("upnp: URL is outside configured subnets")
	ErrInvalidDescription     = errors.New("upnp: invalid description")
	ErrIdentityRejected       = errors.New("upnp: K17 identity rejected")
	ErrFirmwareRejected       = errors.New("upnp: K17 firmware rejected")
	ErrProtocolRejected       = errors.New("upnp: K17 protocolInfo rejected")
	ErrActionUnavailable      = errors.New("upnp: action is not advertised")
)

const mediaRendererTarget = "urn:schemas-upnp-org:device:MediaRenderer:1"

type (
	RendererID = playback.RendererID
	ZoneID     = playback.ZoneID
	Action     string
)

const (
	ActionSetAVTransportURI Action = "SetAVTransportURI"
	ActionPlay              Action = "Play"
	ActionPause             Action = "Pause"
	ActionStop              Action = "Stop"
	ActionSeek              Action = "Seek"
)

type Network struct {
	Name    string
	LocalIP netip.Addr
	Subnet  netip.Prefix
}

func NewNetwork(name, localIP, subnet string) (Network, error) {
	address, addressErr := netip.ParseAddr(localIP)
	prefix, prefixErr := netip.ParsePrefix(subnet)
	if name == "" || addressErr != nil || prefixErr != nil || !address.Is4() || !prefix.Contains(address) {
		return Network{}, ErrInvalidConfig
	}
	masked := prefix.Masked()
	if !allowedDiscoveryAddress(address) || !allowedDiscoverySubnet(masked) {
		return Network{}, ErrInvalidConfig
	}
	return Network{Name: name, LocalIP: address, Subnet: masked}, nil
}

func allowedDiscoveryAddress(address netip.Addr) bool {
	return address.Is4() && (address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLoopback())
}

func allowedDiscoverySubnet(subnet netip.Prefix) bool {
	allowed := [...]netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"), netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("127.0.0.0/8"),
	}
	for _, scope := range allowed {
		if scope.Bits() <= subnet.Bits() && scope.Contains(subnet.Addr()) {
			return true
		}
	}
	return false
}

type Candidate struct {
	Source   netip.AddrPort
	Location string
	USN      string
}

type Evidence struct {
	Manufacturer string `json:"manufacturer"`
	Model        string `json:"model"`
	Firmware     string `json:"firmware"`
	ProtocolInfo string `json:"protocol_info"`
}

type K17Device struct {
	ID                    RendererID
	FriendlyName          string
	UDN                   string
	DescriptionURL        string
	AVTransportControlURL string
	AVTransportEventURL   string
	ConnectionControlURL  string
	Evidence              Evidence
	actions               map[Action]struct{}
	queryActions          map[string]struct{}
	protocols             []string
	networks              []Network
}

func (device K17Device) Supports(action Action) bool {
	_, ok := device.actions[action]
	return ok
}

func (device K17Device) Protocols() []string {
	return append([]string(nil), device.protocols...)
}

func (device K17Device) Actions() []Action {
	result := make([]Action, 0, len(device.actions))
	for action := range device.actions {
		result = append(result, action)
	}
	sort.Slice(result, func(left, right int) bool { return result[left] < result[right] })
	return result
}

type K17Policy struct {
	Manufacturer string
	Model        string
	Firmware     []string
	ProtocolInfo []string
}

type TransportState string

const (
	TransportStopped    TransportState = "STOPPED"
	TransportPlaying    TransportState = "PLAYING"
	TransportPaused     TransportState = "PAUSED_PLAYBACK"
	TransportTransition TransportState = "TRANSITIONING"
	TransportUnknown    TransportState = "UNKNOWN"
)

type ObservedState struct {
	RendererID RendererID
	ZoneID     ZoneID
	Transport  TransportState
	Position   time.Duration
	CurrentURI string
	Owned      bool
	ObservedAt time.Time
}

type DiagnosticKind string

const (
	DiagnosticTimeout   DiagnosticKind = "timeout"
	DiagnosticCancelled DiagnosticKind = "cancelled"
	DiagnosticTransport DiagnosticKind = "transport"
	DiagnosticResponse  DiagnosticKind = "response"
)

type DiagnosticError struct {
	Kind   DiagnosticKind
	Action Action
	Cause  error
}

func (value *DiagnosticError) Error() string {
	return fmt.Sprintf("upnp: %s during %s: %v", value.Kind, value.Action, value.Cause)
}

func (value *DiagnosticError) Unwrap() error { return value.Cause }

type SOAPFault struct {
	Action      Action
	Code        int
	Description string
}

func (value *SOAPFault) Error() string {
	return fmt.Sprintf("upnp: SOAP fault for %s: %d %s", value.Action, value.Code, value.Description)
}
