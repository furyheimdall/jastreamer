package dlna

import (
	"bytes"
	"context"
	"encoding/xml"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

const (
	maxXMLBody         = 1 << 20
	maxXMLCacheEntries = 128
)

type deviceDescription struct {
	URLBase string          `xml:"URLBase"`
	Device  describedDevice `xml:"device"`
}

type describedDevice struct {
	DeviceType   string             `xml:"deviceType"`
	FriendlyName string             `xml:"friendlyName"`
	Manufacturer string             `xml:"manufacturer"`
	ModelName    string             `xml:"modelName"`
	UDN          string             `xml:"UDN"`
	Services     []describedService `xml:"serviceList>service"`
	Devices      []describedDevice  `xml:"deviceList>device"`
}

type describedService struct {
	ServiceType string `xml:"serviceType"`
	SCPDURL     string `xml:"SCPDURL"`
	ControlURL  string `xml:"controlURL"`
}

type serviceDescription struct {
	Actions []struct {
		Name string `xml:"name"`
	} `xml:"actionList>action"`
	StateVariables []struct {
		Name          string   `xml:"name"`
		AllowedValues []string `xml:"allowedValueList>allowedValue"`
	} `xml:"serviceStateTable>stateVariable"`
}

type inspectedDevice struct {
	device         output.Device
	udn            string
	descriptionURL string
	controlURL     string
	serviceType    string
	actions        map[string]bool
	queries        map[string]bool
	source         netip.Addr
	network        localNetwork
}

type xmlCacheEntry struct {
	tag      string
	finalURL string
	data     []byte
}

func (manager *Manager) inspect(ctx context.Context, candidate advertisement) (inspectedDevice, error) {
	tag := cacheTag(candidate)
	var root deviceDescription
	descriptionURL, err := manager.fetchXML(ctx, candidate, candidate.location, tag, &root)
	if err != nil {
		return inspectedDevice{}, err
	}
	value, found := findRenderer(root.Device)
	if !found || udnFromUSN(value.UDN) != candidate.udn {
		return inspectedDevice{}, ErrInvalidResponse
	}
	base := descriptionURL
	if strings.TrimSpace(root.URLBase) != "" {
		base, err = trustedReference(descriptionURL, root.URLBase, candidate.source.Addr(), candidate.network)
		if err != nil {
			return inspectedDevice{}, err
		}
	}
	avService, found := findService(value.Services, avTransportPrefix)
	if !found {
		return inspectedDevice{}, ErrUnsupported
	}
	controlURL, err := trustedReference(base, avService.ControlURL, candidate.source.Addr(), candidate.network)
	if err != nil {
		return inspectedDevice{}, err
	}
	scpdURL, err := trustedReference(base, avService.SCPDURL, candidate.source.Addr(), candidate.network)
	if err != nil {
		return inspectedDevice{}, err
	}
	var avDescription serviceDescription
	if _, err := manager.fetchXML(ctx, candidate, scpdURL, tag, &avDescription); err != nil {
		return inspectedDevice{}, err
	}
	actions := namedActions(avDescription)
	if !actions["SetAVTransportURI"] || !actions["Play"] || !actions["Stop"] || !actions["GetTransportInfo"] {
		return inspectedDevice{}, ErrUnsupported
	}
	protocols := make([]string, 0)
	if connection, ok := findService(value.Services, connectionManagerPrefix); ok {
		connectionSCPD, scpdErr := trustedReference(base, connection.SCPDURL, candidate.source.Addr(), candidate.network)
		connectionControl, controlErr := trustedReference(base, connection.ControlURL, candidate.source.Addr(), candidate.network)
		if scpdErr == nil && controlErr == nil {
			var connectionDescription serviceDescription
			if _, fetchErr := manager.fetchXML(ctx, candidate, connectionSCPD, tag, &connectionDescription); fetchErr == nil && namedActions(connectionDescription)["GetProtocolInfo"] {
				if values, queryErr := manager.getProtocolInfo(ctx, candidate, connectionControl, connection.ServiceType); queryErr == nil {
					protocols = values
				}
			}
		}
	}
	name := strings.TrimSpace(value.FriendlyName)
	if name == "" {
		name = "Media Renderer"
	}
	capabilities := output.Capabilities{
		Play: true, Pause: actions["Pause"], Stop: true,
		Seek: supportsRelativeSeek(avDescription, actions),
	}
	return inspectedDevice{
		device: output.Device{
			ID: rendererID(candidate.udn), Name: name,
			Manufacturer: strings.TrimSpace(value.Manufacturer), Model: strings.TrimSpace(value.ModelName),
			Address: candidate.source.Addr().String(), Online: true,
			LocalAddress: candidate.network.local.String(),
			Capabilities: capabilities, ProtocolInfo: protocols, Protocol: output.ProtocolUPnP,
		},
		udn: candidate.udn, descriptionURL: candidate.location, controlURL: controlURL,
		serviceType: avService.ServiceType, actions: actions,
		queries: map[string]bool{
			"GetTransportInfo": true,
			"GetPositionInfo":  actions["GetPositionInfo"],
			"GetMediaInfo":     actions["GetMediaInfo"],
		},
		source: candidate.source.Addr(), network: candidate.network,
	}, nil
}

func findRenderer(value describedDevice) (describedDevice, bool) {
	if hasTypePrefix(value.DeviceType, mediaRendererPrefix) {
		return value, true
	}
	for _, child := range value.Devices {
		if result, ok := findRenderer(child); ok {
			return result, true
		}
	}
	return describedDevice{}, false
}

func findService(services []describedService, prefix string) (describedService, bool) {
	for _, service := range services {
		if hasTypePrefix(strings.TrimSpace(service.ServiceType), prefix) {
			return service, true
		}
	}
	return describedService{}, false
}

func hasTypePrefix(value, prefix string) bool {
	if len(value) <= len(prefix) || !strings.EqualFold(value[:len(prefix)], prefix) {
		return false
	}
	version := value[len(prefix):]
	for _, char := range version {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func namedActions(description serviceDescription) map[string]bool {
	result := make(map[string]bool, len(description.Actions))
	for _, value := range description.Actions {
		name := strings.TrimSpace(value.Name)
		if name != "" {
			result[name] = true
		}
	}
	return result
}

func supportsRelativeSeek(description serviceDescription, actions map[string]bool) bool {
	if !actions["Seek"] {
		return false
	}
	for _, variable := range description.StateVariables {
		if strings.TrimSpace(variable.Name) != "A_ARG_TYPE_SeekMode" {
			continue
		}
		if len(variable.AllowedValues) == 0 {
			return true
		}
		for _, value := range variable.AllowedValues {
			if strings.EqualFold(strings.TrimSpace(value), "REL_TIME") {
				return true
			}
		}
		return false
	}
	return true
}

func cacheTag(candidate advertisement) string {
	return candidate.source.Addr().String() + "\x00" + candidate.location + "\x00" + candidate.bootID + "\x00" + candidate.configID + "\x00" + candidate.nextBoot
}

func (manager *Manager) fetchXML(ctx context.Context, candidate advertisement, rawURL, tag string, target any) (string, error) {
	trusted, err := trustedReference(candidate.location, rawURL, candidate.source.Addr(), candidate.network)
	if err != nil {
		return "", err
	}
	manager.cacheMu.RLock()
	entry, cached := manager.xmlCache[trusted]
	manager.cacheMu.RUnlock()
	if cached && entry.tag == tag {
		return entry.finalURL, decodeSafeXML(bytes.NewReader(entry.data), target)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, trusted, nil)
	if err != nil {
		return "", ErrUnavailable
	}
	client := manager.clientFor(candidate.source.Addr(), candidate.network)
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", ErrUnavailable
	}
	finalURL := response.Request.URL.String()
	if _, err := trustedAbsoluteURL(finalURL, candidate.source.Addr(), candidate.network); err != nil {
		return "", err
	}
	data, err := readBoundedXML(response.Body)
	if err != nil {
		return "", err
	}
	if err := decodeSafeXML(bytes.NewReader(data), target); err != nil {
		return "", err
	}
	manager.cacheMu.Lock()
	if _, exists := manager.xmlCache[trusted]; !exists && len(manager.xmlCache) >= maxXMLCacheEntries {
		for key := range manager.xmlCache {
			delete(manager.xmlCache, key)
			break
		}
	}
	manager.xmlCache[trusted] = xmlCacheEntry{tag: tag, finalURL: finalURL, data: data}
	manager.cacheMu.Unlock()
	return finalURL, nil
}

func (manager *Manager) clientFor(source netip.Addr, network localNetwork) *http.Client {
	client := *manager.httpClient
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return ErrUnavailable
		}
		_, err := trustedAbsoluteURL(request.URL.String(), source, network)
		return err
	}
	return &client
}

func (manager *Manager) soapClientFor(source netip.Addr, network localNetwork) *http.Client {
	client := manager.clientFor(source, network)
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return client
}

func readBoundedXML(reader io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxXMLBody+1))
	if err != nil || len(data) > maxXMLBody {
		return nil, ErrInvalidResponse
	}
	upper := bytes.ToUpper(data)
	if bytes.Contains(upper, []byte("<!DOCTYPE")) || bytes.Contains(upper, []byte("<!ENTITY")) {
		return nil, ErrInvalidResponse
	}
	return data, nil
}

func decodeSafeXML(reader io.Reader, target any) error {
	data, err := readBoundedXML(reader)
	if err != nil {
		return err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidResponse
	}
	return nil
}

func trustedReference(baseRaw, referenceRaw string, source netip.Addr, network localNetwork) (string, error) {
	base, baseErr := url.Parse(strings.TrimSpace(baseRaw))
	reference, referenceErr := url.Parse(strings.TrimSpace(referenceRaw))
	if baseErr != nil || referenceErr != nil {
		return "", ErrUnavailable
	}
	resolved := base.ResolveReference(reference)
	return trustedAbsoluteURL(resolved.String(), source, network)
}

func (manager *Manager) getProtocolInfo(ctx context.Context, candidate advertisement, controlURL, serviceType string) ([]string, error) {
	data, err := manager.executeSOAP(ctx, soapCall{
		candidate: candidate, url: controlURL, service: serviceType, action: "GetProtocolInfo",
	})
	if err != nil {
		return nil, err
	}
	var response struct {
		Sink string `xml:"Body>GetProtocolInfoResponse>Sink"`
	}
	if err := decodeSafeXML(bytes.NewReader(data), &response); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	values := make([]string, 0)
	for _, raw := range strings.Split(response.Sink, ",") {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values, nil
}

func httpClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext
	transport.ResponseHeaderTimeout = 4 * time.Second
	transport.TLSHandshakeTimeout = 4 * time.Second
	transport.ExpectContinueTimeout = time.Second
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}
