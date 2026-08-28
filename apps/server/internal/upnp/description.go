package upnp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

const maxXMLBody = 1024 * 1024

const (
	avTransportService       = "urn:schemas-upnp-org:service:AVTransport:1"
	connectionManagerService = "urn:schemas-upnp-org:service:ConnectionManager:1"
)

type InspectorConfig struct {
	Networks   []Network
	HTTPClient *http.Client
	Policy     K17Policy
}

type Inspector struct{ config InspectorConfig }

type deviceDescription struct {
	Device describedDevice `xml:"device"`
}

type describedDevice struct {
	DeviceType   string             `xml:"deviceType"`
	FriendlyName string             `xml:"friendlyName"`
	Manufacturer string             `xml:"manufacturer"`
	ModelName    string             `xml:"modelName"`
	ModelNumber  string             `xml:"modelNumber"`
	UDN          string             `xml:"UDN"`
	Services     []describedService `xml:"serviceList>service"`
}

type describedService struct {
	ServiceType string `xml:"serviceType"`
	SCPDURL     string `xml:"SCPDURL"`
	ControlURL  string `xml:"controlURL"`
	EventURL    string `xml:"eventSubURL"`
}

type serviceDescription struct {
	Actions []struct {
		Name string `xml:"name"`
	} `xml:"actionList>action"`
}

type protocolInfoResponse struct {
	Sink string `xml:"Body>GetProtocolInfoResponse>Sink"`
}

func NewInspector(config InspectorConfig) (*Inspector, error) {
	if len(config.Networks) == 0 || config.HTTPClient == nil || config.Policy.Manufacturer == "" || config.Policy.Model == "" || len(config.Policy.Firmware) != 1 || len(config.Policy.ProtocolInfo) == 0 {
		return nil, ErrInvalidConfig
	}
	if _, ok := parseFirmware(config.Policy.Firmware[0]); !ok {
		return nil, ErrInvalidConfig
	}
	return &Inspector{config: config}, nil
}

func (inspector *Inspector) InspectK17(ctx context.Context, candidate Candidate) (K17Device, error) {
	var description deviceDescription
	if err := inspector.fetchXML(ctx, candidate, candidate.Location, &description); err != nil {
		return K17Device{}, fmt.Errorf("fetch K17 description: %w", err)
	}
	value := description.Device
	if value.DeviceType != mediaRendererTarget || value.Manufacturer != inspector.config.Policy.Manufacturer || value.ModelName != inspector.config.Policy.Model || value.UDN == "" {
		return K17Device{}, ErrIdentityRejected
	}
	if !firmwareAtLeast(value.ModelNumber, inspector.config.Policy.Firmware[0]) {
		return K17Device{}, ErrFirmwareRejected
	}
	avTransport, avFound := findService(value.Services, avTransportService)
	connectionManager, connectionFound := findService(value.Services, connectionManagerService)
	if !avFound || !connectionFound {
		return K17Device{}, ErrInvalidDescription
	}
	avControl, err := trustedReference(candidate.Location, avTransport.ControlURL, candidate.Source.Addr(), inspector.config.Networks)
	if err != nil {
		return K17Device{}, err
	}
	avEvent, err := trustedReference(candidate.Location, avTransport.EventURL, candidate.Source.Addr(), inspector.config.Networks)
	if err != nil {
		return K17Device{}, err
	}
	connectionControl, err := trustedReference(candidate.Location, connectionManager.ControlURL, candidate.Source.Addr(), inspector.config.Networks)
	if err != nil {
		return K17Device{}, err
	}
	avSCPD, err := trustedReference(candidate.Location, avTransport.SCPDURL, candidate.Source.Addr(), inspector.config.Networks)
	if err != nil {
		return K17Device{}, err
	}
	connectionSCPD, err := trustedReference(candidate.Location, connectionManager.SCPDURL, candidate.Source.Addr(), inspector.config.Networks)
	if err != nil {
		return K17Device{}, err
	}
	var avDescription, connectionDescription serviceDescription
	if err := inspector.fetchXML(ctx, candidate, avSCPD, &avDescription); err != nil {
		return K17Device{}, fmt.Errorf("fetch AVTransport SCPD: %w", err)
	}
	if err := inspector.fetchXML(ctx, candidate, connectionSCPD, &connectionDescription); err != nil {
		return K17Device{}, fmt.Errorf("fetch ConnectionManager SCPD: %w", err)
	}
	if !hasNamedAction(connectionDescription, "GetProtocolInfo") {
		return K17Device{}, ErrProtocolRejected
	}
	protocolInfo, err := inspector.getProtocolInfo(ctx, candidate, connectionControl)
	if err != nil {
		return K17Device{}, err
	}
	protocols := compatibleProtocols(protocolInfo, inspector.config.Policy.ProtocolInfo)
	if len(protocols) == 0 {
		return K17Device{}, ErrProtocolRejected
	}
	digest := sha256.Sum256([]byte(value.UDN))
	return K17Device{
		ID: RendererID("k17-" + hex.EncodeToString(digest[:12])), FriendlyName: value.FriendlyName,
		UDN: value.UDN, DescriptionURL: candidate.Location, AVTransportControlURL: avControl,
		AVTransportEventURL: avEvent, ConnectionControlURL: connectionControl,
		Evidence: Evidence{Manufacturer: value.Manufacturer, Model: value.ModelName, Firmware: value.ModelNumber, ProtocolInfo: protocolInfo},
		actions:  allowedActions(avDescription), queryActions: queryActions(avDescription), protocols: protocols,
		networks: append([]Network(nil), inspector.config.Networks...),
	}, nil
}

func (inspector *Inspector) fetchXML(ctx context.Context, candidate Candidate, rawURL string, target any) error {
	if _, err := trustedReference(candidate.Location, rawURL, candidate.Source.Addr(), inspector.config.Networks); err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return ErrInvalidDescription
	}
	response, err := inspector.client(candidate).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ErrInvalidDescription
	}
	return decodeBoundedXML(response.Body, target)
}

func (inspector *Inspector) client(candidate Candidate) *http.Client {
	client := *inspector.config.HTTPClient
	client.CheckRedirect = func(request *http.Request, _ []*http.Request) error {
		_, err := trustedReference(candidate.Location, request.URL.String(), candidate.Source.Addr(), inspector.config.Networks)
		return err
	}
	return &client
}

func decodeBoundedXML(reader io.Reader, target any) error {
	data, err := io.ReadAll(io.LimitReader(reader, maxXMLBody+1))
	if err != nil || len(data) > maxXMLBody || bytes.Contains(bytes.ToUpper(data), []byte("<!DOCTYPE")) || bytes.Contains(bytes.ToUpper(data), []byte("<!ENTITY")) {
		return ErrInvalidDescription
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	if err := decoder.Decode(target); err != nil {
		return ErrInvalidDescription
	}
	return nil
}

func trustedReference(baseRaw, referenceRaw string, source netip.Addr, networks []Network) (string, error) {
	base, baseErr := url.Parse(baseRaw)
	reference, referenceErr := url.Parse(referenceRaw)
	if baseErr != nil || referenceErr != nil {
		return "", ErrOffSubnetURL
	}
	resolved := base.ResolveReference(reference)
	address, err := netip.ParseAddr(resolved.Hostname())
	allowed := false
	for _, network := range networks {
		allowed = allowed || network.Subnet.Contains(address)
	}
	if err != nil || resolved.Scheme != "http" || resolved.User != nil || resolved.Host == "" || resolved.Fragment != "" || address != source || !allowed {
		return "", ErrOffSubnetURL
	}
	return resolved.String(), nil
}

func findService(services []describedService, serviceType string) (describedService, bool) {
	for _, service := range services {
		if service.ServiceType == serviceType {
			return service, true
		}
	}
	return describedService{}, false
}

func hasNamedAction(description serviceDescription, name string) bool {
	for _, action := range description.Actions {
		if action.Name == name {
			return true
		}
	}
	return false
}

func allowedActions(description serviceDescription) map[Action]struct{} {
	allowed := map[Action]struct{}{}
	for _, action := range description.Actions {
		switch Action(strings.TrimSpace(action.Name)) {
		case ActionSetAVTransportURI, ActionPlay, ActionPause, ActionStop, ActionSeek:
			allowed[Action(action.Name)] = struct{}{}
		}
	}
	return allowed
}

func queryActions(description serviceDescription) map[string]struct{} {
	result := map[string]struct{}{}
	for _, action := range description.Actions {
		if action.Name == "GetTransportInfo" || action.Name == "GetPositionInfo" {
			result[action.Name] = struct{}{}
		}
	}
	return result
}
