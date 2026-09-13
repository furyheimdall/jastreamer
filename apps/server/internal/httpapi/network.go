package httpapi

import (
	"net"
	"net/http"
	"net/netip"

	"github.com/jastreamer/jastreamer-server/internal/dlna"
)

type networkAddress struct {
	Address      string `json:"address"`
	PrefixLength int    `json:"prefix_length"`
	UPnPUsable   bool   `json:"upnp_usable"`
}

type networkInterface struct {
	Name      string           `json:"name"`
	Index     int              `json:"index"`
	Up        bool             `json:"up"`
	Multicast bool             `json:"multicast"`
	Loopback  bool             `json:"loopback"`
	Addresses []networkAddress `json:"addresses"`
}

func (service *server) networkInterfaces(w http.ResponseWriter, _ *http.Request) {
	interfaces, err := enumerateNetworkInterfaces()
	if err != nil {
		writeError(w, err)
		return
	}
	reply(w, http.StatusOK, struct {
		Interfaces []networkInterface `json:"interfaces"`
	}{Interfaces: interfaces})
}

func enumerateNetworkInterfaces() ([]networkInterface, error) {
	values, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	interfaces := make([]networkInterface, 0, len(values))
	for _, value := range values {
		up := value.Flags&net.FlagUp != 0
		multicast := value.Flags&net.FlagMulticast != 0
		loopback := value.Flags&net.FlagLoopback != 0
		item := networkInterface{
			Name:      value.Name,
			Index:     value.Index,
			Up:        up,
			Multicast: multicast,
			Loopback:  loopback,
			Addresses: make([]networkAddress, 0),
		}
		addresses, addressErr := value.Addrs()
		if addressErr == nil {
			for _, raw := range addresses {
				prefix, ok := networkPrefix(raw)
				if !ok {
					continue
				}
				item.Addresses = append(item.Addresses, networkAddress{
					Address:      prefix.Addr().String(),
					PrefixLength: prefix.Bits(),
					UPnPUsable:   upnpUsableAddress(up, multicast, loopback, prefix),
				})
			}
		}
		interfaces = append(interfaces, item)
	}
	return interfaces, nil
}

func networkPrefix(address net.Addr) (netip.Prefix, bool) {
	if network, ok := address.(*net.IPNet); ok {
		ip, valid := netip.AddrFromSlice(network.IP)
		ones, bits := network.Mask.Size()
		if !valid || ones < 0 {
			return netip.Prefix{}, false
		}
		if ip.Is4In6() {
			switch bits {
			case 128:
				if ones < 96 {
					return netip.Prefix{}, false
				}
				ones -= 96
			case 32:
			default:
				return netip.Prefix{}, false
			}
			ip = ip.Unmap()
		}
		prefix := netip.PrefixFrom(ip, ones)
		return prefix, prefix.IsValid()
	}
	prefix, err := netip.ParsePrefix(address.String())
	if err != nil {
		return netip.Prefix{}, false
	}
	if prefix.Addr().Is4In6() {
		if prefix.Bits() < 96 {
			return netip.Prefix{}, false
		}
		prefix = netip.PrefixFrom(prefix.Addr().Unmap(), prefix.Bits()-96)
	}
	return prefix, prefix.IsValid()
}

func upnpUsableAddress(up, multicast, loopback bool, prefix netip.Prefix) bool {
	return up && multicast && !loopback && dlna.SupportsNetworkAddress(prefix)
}
