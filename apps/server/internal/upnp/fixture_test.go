package upnp_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

const fixtureProtocol = "http-get:*:audio/flac:*"

func fixtureMediaResource(rawURL string) playback.MediaResource {
	return playback.MediaResource{
		URL: rawURL, MimeType: "audio/flac", TrackID: "fixture-track",
		Title: "Fixture & Track", Representation: playback.MediaOriginal,
	}
}

type fixtureDevice struct {
	manufacturer        string
	model               string
	firmware            string
	protocolInfo        string
	seek                bool
	rawDescription      string
	redirectDescription string
	soapFaultAction     string
	blockAction         string
	transportState      string
	currentURI          string
}

type fixtureServer struct {
	server        *httptest.Server
	device        fixtureDevice
	mu            sync.Mutex
	actions       []string
	mediaBody     string
	mediaURL      string
	mediaMetadata string
	pulledMedia   string
	pullClient    *http.Client
	actionStarted chan struct{}
	releaseAction chan struct{}
}

func newFixture(t *testing.T, device fixtureDevice) *fixtureServer {
	t.Helper()
	fixture := &fixtureServer{device: device, actionStarted: make(chan struct{}), releaseAction: make(chan struct{})}
	fixture.server = httptest.NewUnstartedServer(http.HandlerFunc(fixture.serveHTTP))
	fixture.server.Listener.Close()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fixture.server.Listener = listener
	fixture.server.Start()
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *fixtureServer) candidate(t *testing.T) upnp.Candidate {
	t.Helper()
	return testCandidate(fixture.server.URL + "/device.xml")
}

func (fixture *fixtureServer) inspector(t *testing.T) *upnp.Inspector {
	t.Helper()
	address := fixture.server.Listener.Addr().(*net.TCPAddr).IP
	network, err := upnp.NewNetwork("fixture", address.String(), netip.PrefixFrom(netip.MustParseAddr(address.String()), 8).String())
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := upnp.NewInspector(upnp.InspectorConfig{Networks: []upnp.Network{network}, HTTPClient: fixture.server.Client(), Policy: upnp.K17Policy{Manufacturer: "FiiO", Model: "FiiO K17", Firmware: []string{"V261"}, ProtocolInfo: []string{fixtureProtocol, "http-get:*:audio/L16:*"}}})
	if err != nil {
		t.Fatal(err)
	}
	return inspector
}

func (fixture *fixtureServer) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/device.xml":
		if fixture.device.redirectDescription != "" {
			http.Redirect(writer, request, fixture.device.redirectDescription, http.StatusFound)
			return
		}
		if fixture.device.rawDescription != "" {
			_, _ = io.WriteString(writer, fixture.device.rawDescription)
			return
		}
		_, _ = fmt.Fprintf(writer, `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0"><device><deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType><friendlyName>Fixture K17</friendlyName><manufacturer>%s</manufacturer><modelName>%s</modelName><modelNumber>%s</modelNumber><UDN>uuid:k17</UDN><serviceList><service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId><SCPDURL>/avtransport.xml</SCPDURL><controlURL>/avtransport/control</controlURL><eventSubURL>/avtransport/event</eventSubURL></service><service><serviceType>urn:schemas-upnp-org:service:ConnectionManager:1</serviceType><serviceId>urn:upnp-org:serviceId:ConnectionManager</serviceId><SCPDURL>/connection.xml</SCPDURL><controlURL>/connection/control</controlURL></service></serviceList></device></root>`, fixture.device.manufacturer, fixture.device.model, fixture.device.firmware)
	case "/avtransport.xml":
		seek := ""
		if fixture.device.seek {
			seek = "<action><name>Seek</name></action>"
		}
		_, _ = fmt.Fprintf(writer, `<?xml version="1.0"?><scpd xmlns="urn:schemas-upnp-org:service-1-0"><actionList><action><name>SetAVTransportURI</name></action><action><name>Play</name></action><action><name>Pause</name></action><action><name>Stop</name></action>%s<action><name>GetTransportInfo</name></action><action><name>GetPositionInfo</name></action></actionList></scpd>`, seek)
	case "/connection.xml":
		_, _ = io.WriteString(writer, `<?xml version="1.0"?><scpd xmlns="urn:schemas-upnp-org:service-1-0"><actionList><action><name>GetProtocolInfo</name></action></actionList></scpd>`)
	case "/connection/control":
		_, _ = fmt.Fprintf(writer, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetProtocolInfoResponse xmlns:u="urn:schemas-upnp-org:service:ConnectionManager:1"><Source></Source><Sink>%s</Sink></u:GetProtocolInfoResponse></s:Body></s:Envelope>`, fixture.device.protocolInfo)
	case "/avtransport/control":
		fixture.serveSOAP(writer, request)
	case "/media/v1/signed-fixture":
		fixture.mu.Lock()
		fixture.mediaBody = "fixture-media"
		fixture.mu.Unlock()
		_, _ = io.WriteString(writer, "fixture-media")
	default:
		http.NotFound(writer, request)
	}
}

func (fixture *fixtureServer) serveSOAP(writer http.ResponseWriter, request *http.Request) {
	action := strings.Trim(request.Header.Get("SOAPAction"), `"`)
	if index := strings.LastIndexByte(action, '#'); index >= 0 {
		action = action[index+1:]
	}
	fixture.mu.Lock()
	fixture.actions = append(fixture.actions, action)
	blockAction := fixture.device.blockAction
	faultAction := fixture.device.soapFaultAction
	fixture.mu.Unlock()
	if action == blockAction {
		close(fixture.actionStarted)
		select {
		case <-request.Context().Done():
		case <-fixture.releaseAction:
		}
		return
	}
	if action == faultAction {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(writer, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>701</errorCode><errorDescription>Transition unavailable</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
		return
	}
	if action == "SetAVTransportURI" {
		body, _ := io.ReadAll(request.Body)
		encoded := string(body)
		start := strings.Index(encoded, "<CurrentURI>")
		end := strings.Index(encoded, "</CurrentURI>")
		metadataStart := strings.Index(encoded, "<CurrentURIMetaData>")
		metadataEnd := strings.Index(encoded, "</CurrentURIMetaData>")
		fixture.mu.Lock()
		if start >= 0 && end > start {
			fixture.mediaURL = strings.ReplaceAll(encoded[start+12:end], "&amp;", "&")
		}
		if metadataStart >= 0 && metadataEnd > metadataStart {
			fixture.mediaMetadata = encoded[metadataStart+len("<CurrentURIMetaData>") : metadataEnd]
		}
		mediaURL := fixture.mediaURL
		pullClient := fixture.pullClient
		fixture.mu.Unlock()
		if pullClient != nil && mediaURL != "" {
			response, err := pullClient.Get(mediaURL)
			if err != nil {
				http.Error(writer, err.Error(), http.StatusBadGateway)
				return
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil || response.StatusCode != http.StatusOK {
				http.Error(writer, "media pull failed", http.StatusBadGateway)
				return
			}
			fixture.mu.Lock()
			fixture.pulledMedia = string(body)
			fixture.mu.Unlock()
		}
	}
	if action == "GetTransportInfo" {
		fixture.mu.Lock()
		state := fixture.device.transportState
		fixture.mu.Unlock()
		if state == "" {
			state = "PLAYING"
		}
		_, _ = fmt.Fprintf(writer, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><CurrentTransportState>%s</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus></u:GetTransportInfoResponse></s:Body></s:Envelope>`, state)
		return
	}
	if action == "GetPositionInfo" {
		fixture.mu.Lock()
		currentURI := fixture.device.currentURI
		fixture.mu.Unlock()
		_, _ = fmt.Fprintf(writer, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetPositionInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><RelTime>00:00:07</RelTime><TrackURI>%s</TrackURI></u:GetPositionInfoResponse></s:Body></s:Envelope>`, currentURI)
		return
	}
	_, _ = fmt.Fprintf(writer, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:%sResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"/></s:Body></s:Envelope>`, action)
}
