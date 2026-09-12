package dlna

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestEndpointChangeRefreshesStableRenderer(t *testing.T) {
	const udn = "uuid:renderer-fixture"
	firstServer, firstPlays := rendererServer(t, udn, "first")
	defer firstServer.Close()
	secondServer, secondPlays := rendererServer(t, udn, "second")
	defer secondServer.Close()

	manager := fixtureManager(firstServer.Client())
	first := fixtureAdvertisement(firstServer.URL+"/description.xml", udn, "1")
	manager.acceptAdvertisement(context.Background(), first)
	device := waitForDevice(t, manager, func(value output.Device) bool { return value.Online && value.Model == "first" })
	if device.Protocol != output.ProtocolUPnP {
		t.Fatalf("DLNA renderer protocol=%q, want %q", device.Protocol, output.ProtocolUPnP)
	}

	second := fixtureAdvertisement(secondServer.URL+"/description.xml", udn, "2")
	manager.acceptAdvertisement(context.Background(), second)
	updated := waitForDevice(t, manager, func(value output.Device) bool { return value.Online && value.Model == "second" })
	if updated.ID != device.ID {
		t.Fatalf("stable UDN changed renderer ID: %q != %q", updated.ID, device.ID)
	}
	if err := manager.Play(context.Background(), updated.ID); err != nil {
		t.Fatal(err)
	}
	if firstPlays.Load() != 0 || secondPlays.Load() != 1 {
		t.Fatalf("Play used stale control endpoint: first=%d second=%d", firstPlays.Load(), secondPlays.Load())
	}
}

func TestDuplicateAdvertisementsShareOneAcceptedInspection(t *testing.T) {
	const udn = "uuid:duplicate-fixture"
	release := make(chan struct{})
	var descriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/description.xml":
			descriptions.Add(1)
			<-release
			_, _ = io.WriteString(writer, rendererDescription(udn, "duplicate"))
		case "/avtransport.xml":
			_, _ = io.WriteString(writer, avTransportDescription())
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	manager := fixtureManager(server.Client())
	candidate := fixtureAdvertisement(server.URL+"/description.xml", udn, "1")
	for index := 0; index < maxConcurrentInspections+1; index++ {
		manager.acceptAdvertisement(context.Background(), candidate)
	}
	close(release)
	waitForDevice(t, manager, func(value output.Device) bool { return value.Online && value.Model == "duplicate" })
	if descriptions.Load() != 1 {
		t.Fatalf("identical advertisements launched %d description inspections", descriptions.Load())
	}
}

func TestNewEndpointInspectionCannotBeOverwrittenByStaleResult(t *testing.T) {
	const udn = "uuid:concurrent-refresh-fixture"
	releaseFirst := make(chan struct{})
	released := false
	firstServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/description.xml":
			<-releaseFirst
			_, _ = io.WriteString(writer, rendererDescription(udn, "stale"))
		case "/avtransport.xml":
			_, _ = io.WriteString(writer, avTransportDescription())
		default:
			http.NotFound(writer, request)
		}
	}))
	defer firstServer.Close()
	secondServer, _ := rendererServer(t, udn, "current")
	defer secondServer.Close()
	defer func() {
		if !released {
			close(releaseFirst)
		}
	}()

	manager := fixtureManager(firstServer.Client())
	manager.acceptAdvertisement(context.Background(), fixtureAdvertisement(firstServer.URL+"/description.xml", udn, "1"))
	manager.acceptAdvertisement(context.Background(), fixtureAdvertisement(secondServer.URL+"/description.xml", udn, "2"))
	current := waitForDevice(t, manager, func(value output.Device) bool { return value.Online && value.Model == "current" })
	close(releaseFirst)
	released = true
	waitForInspections(t, manager)
	stillCurrent, ok := manager.Device(current.ID)
	if !ok || !stillCurrent.Online || stillCurrent.Model != "current" {
		t.Fatalf("stale inspection overwrote current endpoint: %+v, found=%v", stillCurrent, ok)
	}
}

func TestDecodeSafeXMLRejectsEntitiesAndOversize(t *testing.T) {
	var target struct {
		Value string `xml:",chardata"`
	}
	entity := `<?xml version="1.0"?><!DOCTYPE value [<!ENTITY secret SYSTEM "file:///etc/passwd">]><value>&secret;</value>`
	if err := decodeSafeXML(strings.NewReader(entity), &target); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("entity document error = %v", err)
	}
	oversize := strings.NewReader("<value>" + strings.Repeat("x", maxXMLBody) + "</value>")
	if err := decodeSafeXML(oversize, &target); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("oversized document error = %v", err)
	}
}

func TestSOAPOnlyRetriesReadQueriesAfterStaleConnection(t *testing.T) {
	const service = "urn:schemas-upnp-org:service:AVTransport:1"
	tests := []struct {
		name         string
		action       string
		wantSuccess  bool
		wantAccepted int32
	}{
		{name: "read query", action: "GetPositionInfo", wantSuccess: true, wantAccepted: 2},
		{name: "command", action: "Stop", wantAccepted: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, endpoint, accepted := staleSOAPEndpoint(t, service)
			manager := fixtureManager(client)
			candidate := fixtureAdvertisement(endpoint, "uuid:stale-soap-fixture", "1")

			if _, err := manager.executeSOAP(context.Background(), soapCall{
				candidate: candidate, url: endpoint, service: service, action: "GetTransportInfo",
			}); err != nil {
				t.Fatalf("warm-up query failed: %v", err)
			}
			_, err := manager.executeSOAP(context.Background(), soapCall{
				candidate: candidate, url: endpoint, service: service, action: test.action,
			})
			if test.wantSuccess {
				if err != nil {
					t.Fatalf("%s was not retried on a fresh connection: %v", test.action, err)
				}
			} else {
				var actionErr *output.ActionError
				if !errors.As(err, &actionErr) || actionErr.Kind != output.ErrorTransport {
					t.Fatalf("%s error = %v, want transport ActionError", test.action, err)
				}
				if cause := errors.Unwrap(actionErr); cause == nil || errors.Is(cause, ErrUnavailable) {
					t.Fatalf("%s discarded transport cause: %v", test.action, cause)
				}
			}
			if got := accepted.Load(); got != test.wantAccepted {
				t.Fatalf("%s accepted connections = %d, want %d", test.action, got, test.wantAccepted)
			}
		})
	}
}

func staleSOAPEndpoint(t *testing.T, service string) (*http.Client, string, *atomic.Int32) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{}
	client := &http.Client{Transport: transport}
	accepted := new(atomic.Int32)
	t.Cleanup(func() {
		transport.CloseIdleConnections()
		_ = listener.Close()
	})
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			number := accepted.Add(1)
			func() {
				defer connection.Close()
				reader := bufio.NewReader(connection)
				action, requestErr := readSOAPAction(reader)
				if requestErr != nil {
					return
				}
				if writeSOAPResponse(connection, service, action) != nil || number != 1 {
					return
				}
				// Consume the next request on the reused connection, then close
				// without a response like a stale renderer connection.
				_, _ = readSOAPAction(reader)
			}()
			if number >= 2 {
				return
			}
		}
	}()
	return client, "http://" + listener.Addr().String(), accepted
}

func readSOAPAction(reader *bufio.Reader) (string, error) {
	request, err := http.ReadRequest(reader)
	if err != nil {
		return "", err
	}
	defer request.Body.Close()
	if _, err := io.Copy(io.Discard, request.Body); err != nil {
		return "", err
	}
	header := strings.Trim(request.Header.Get("SOAPAction"), `"`)
	index := strings.LastIndexByte(header, '#')
	if index < 0 || index == len(header)-1 {
		return "", errors.New("missing SOAP action")
	}
	return header[index+1:], nil
}

func writeSOAPResponse(writer io.Writer, service, action string) error {
	body := soapResponse(service, action)
	_, err := fmt.Fprintf(writer, "HTTP/1.1 200 OK\r\nContent-Type: text/xml\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

func TestStopRequiresValidMatchingSOAPResponse(t *testing.T) {
	const service = "urn:schemas-upnp-org:service:AVTransport:1"
	tests := []struct {
		name     string
		status   int
		body     string
		wantKind output.ErrorKind
		wantCode int
	}{
		{name: "valid response", status: http.StatusOK, body: soapResponse(service, "Stop")},
		{name: "empty success", status: http.StatusOK, wantKind: output.ErrorResponse},
		{name: "garbage success", status: http.StatusOK, body: "renderer error", wantKind: output.ErrorResponse},
		{name: "missing SOAP namespace", status: http.StatusOK, body: "<Envelope><Body><StopResponse/></Body></Envelope>", wantKind: output.ErrorResponse},
		{name: "wrong action", status: http.StatusOK, body: soapResponse(service, "Play"), wantKind: output.ErrorResponse},
		{name: "wrong service", status: http.StatusOK, body: soapResponse("urn:schemas-upnp-org:service:RenderingControl:1", "Stop"), wantKind: output.ErrorResponse},
		{name: "fault with success status", status: http.StatusOK, body: soapFault(701), wantKind: output.ErrorFault, wantCode: 701},
		{name: "unsupported fault", status: http.StatusInternalServerError, body: soapFault(401), wantKind: output.ErrorUnsupported, wantCode: 401},
		{name: "success envelope with error status", status: http.StatusInternalServerError, body: soapResponse(service, "Stop"), wantKind: output.ErrorResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				writer.Header().Set("Content-Type", "text/xml")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()

			manager := fixtureManager(server.Client())
			deviceID := rendererID("uuid:soap-stop-fixture")
			record := fixtureRecord(deviceID, server.URL)
			record.udn = "uuid:soap-stop-fixture"
			manager.devices[deviceID] = record
			err := manager.Stop(context.Background(), deviceID)
			if test.wantKind == "" {
				if err != nil {
					t.Fatalf("valid Stop response returned %v", err)
				}
				return
			}
			var actionErr *output.ActionError
			if !errors.As(err, &actionErr) {
				t.Fatalf("Stop error = %v, want classified action error", err)
			}
			if actionErr.Kind != test.wantKind || actionErr.Code != test.wantCode {
				t.Fatalf("Stop error = (%s, %d), want (%s, %d)", actionErr.Kind, actionErr.Code, test.wantKind, test.wantCode)
			}
		})
	}
}

func TestObservePreservesUnknownStateAndFractionalPosition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		action := request.Header.Get("SOAPAction")
		writer.Header().Set("Content-Type", "text/xml")
		if strings.Contains(action, "GetTransportInfo") {
			_, _ = io.WriteString(writer, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetTransportInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><CurrentTransportState>RECORDING</CurrentTransportState><CurrentTransportStatus>ERROR_OCCURRED</CurrentTransportStatus></u:GetTransportInfoResponse></s:Body></s:Envelope>`)
			return
		}
		_, _ = io.WriteString(writer, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetPositionInfoResponse xmlns:u="urn:schemas-upnp-org:service:AVTransport:1"><TrackDuration>NOT_IMPLEMENTED</TrackDuration><RelTime>00:00:07.250</RelTime><TrackURI>http://127.0.0.1/media/private</TrackURI></u:GetPositionInfoResponse></s:Body></s:Envelope>`)
	}))
	defer server.Close()

	manager := fixtureManager(server.Client())
	deviceID := rendererID("uuid:observation-fixture")
	manager.devices[deviceID] = fixtureRecord(deviceID, server.URL+"/control")
	observation, err := manager.Observe(context.Background(), deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != "unknown" || observation.PositionMS != 7250 || !observation.HasPosition || observation.DurationMS != 0 || !observation.HasURI {
		t.Fatalf("unexpected observation: %+v", observation)
	}
	if observation.TransportStatus != "ERROR_OCCURRED" {
		t.Fatalf("transport status = %q", observation.TransportStatus)
	}
}

func TestObserveTransportOnlyLeavesOptionalValuesUnknown(t *testing.T) {
	const service = "urn:schemas-upnp-org:service:AVTransport:1"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.Header().Set("Content-Type", "text/xml")
		if !strings.Contains(request.Header.Get("SOAPAction"), "#GetTransportInfo") {
			http.Error(writer, "unexpected action", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(writer, soapResponseWithBody(service, "GetTransportInfo", "<CurrentTransportState>PLAYING</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"))
	}))
	defer server.Close()

	manager := fixtureManager(server.Client())
	deviceID := rendererID("uuid:transport-only-fixture")
	record := fixtureRecord(deviceID, server.URL)
	record.udn = "uuid:transport-only-fixture"
	record.queries = map[string]bool{"GetTransportInfo": true}
	manager.devices[deviceID] = record
	observation, err := manager.Observe(context.Background(), deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != "playing" || observation.HasURI || observation.URI != "" ||
		observation.HasPosition || observation.PositionMS != 0 || observation.DurationMS != 0 {
		t.Fatalf("transport-only observation invented optional values: %+v", observation)
	}
}

func TestObserveFallsBackFromUnsupportedPositionToKnownEmptyMediaURI(t *testing.T) {
	const service = "urn:schemas-upnp-org:service:AVTransport:1"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.Header().Set("Content-Type", "text/xml")
		switch {
		case strings.Contains(request.Header.Get("SOAPAction"), "#GetTransportInfo"):
			_, _ = io.WriteString(writer, soapResponseWithBody(service, "GetTransportInfo", "<CurrentTransportState>STOPPED</CurrentTransportState><CurrentTransportStatus>OK</CurrentTransportStatus>"))
		case strings.Contains(request.Header.Get("SOAPAction"), "#GetPositionInfo"):
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(writer, soapFault(401))
		case strings.Contains(request.Header.Get("SOAPAction"), "#GetMediaInfo"):
			_, _ = io.WriteString(writer, soapResponseWithBody(service, "GetMediaInfo", "<MediaDuration>00:02:03</MediaDuration><CurrentURI></CurrentURI>"))
		default:
			http.Error(writer, "unexpected action", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	manager := fixtureManager(server.Client())
	deviceID := rendererID("uuid:media-info-fixture")
	record := fixtureRecord(deviceID, server.URL)
	record.udn = "uuid:media-info-fixture"
	record.queries = map[string]bool{
		"GetTransportInfo": true,
		"GetPositionInfo":  true,
		"GetMediaInfo":     true,
	}
	manager.devices[deviceID] = record
	observation, err := manager.Observe(context.Background(), deviceID)
	if err != nil {
		t.Fatal(err)
	}
	if observation.State != "stopped" || !observation.HasURI || observation.URI != "" ||
		observation.HasPosition || observation.PositionMS != 0 || observation.DurationMS != 123000 {
		t.Fatalf("media fallback observation = %+v", observation)
	}
}

func rendererServer(t *testing.T, udn, model string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	plays := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/description.xml":
			_, _ = io.WriteString(writer, rendererDescription(udn, model))
		case "/avtransport.xml":
			_, _ = io.WriteString(writer, avTransportDescription())
		case "/control":
			if strings.Contains(request.Header.Get("SOAPAction"), "#Play") {
				plays.Add(1)
			}
			_, _ = io.Copy(io.Discard, request.Body)
			_, _ = io.WriteString(writer, soapResponse("urn:schemas-upnp-org:service:AVTransport:1", "Play"))
		default:
			http.NotFound(writer, request)
		}
	}))
	return server, plays
}

func rendererDescription(udn, model string) string {
	return `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0"><device><deviceType>urn:schemas-upnp-org:device:MediaRenderer:1</deviceType><friendlyName>Fixture Renderer</friendlyName><manufacturer>Fixture</manufacturer><modelName>` + model + `</modelName><UDN>` + udn + `</UDN><serviceList><service><serviceType>urn:schemas-upnp-org:service:AVTransport:1</serviceType><serviceId>urn:upnp-org:serviceId:AVTransport</serviceId><SCPDURL>/avtransport.xml</SCPDURL><controlURL>/control</controlURL><eventSubURL>/events</eventSubURL></service></serviceList></device></root>`
}

func avTransportDescription() string {
	return `<?xml version="1.0"?><scpd xmlns="urn:schemas-upnp-org:service-1-0"><actionList><action><name>SetAVTransportURI</name></action><action><name>Play</name></action><action><name>Stop</name></action><action><name>GetTransportInfo</name></action><action><name>GetPositionInfo</name></action><action><name>GetMediaInfo</name></action></actionList></scpd>`
}

func soapResponse(service, action string) string {
	return soapResponseWithBody(service, action, "")
}

func soapResponseWithBody(service, action, body string) string {
	return `<?xml version="1.0"?><SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"><SOAP-ENV:Body><response:` + action + `Response xmlns:response="` + service + `">` + body + `</response:` + action + `Response></SOAP-ENV:Body></SOAP-ENV:Envelope>`
}

func soapFault(code int) string {
	return `<?xml version="1.0"?><SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/"><SOAP-ENV:Body><SOAP-ENV:Fault><faultcode>SOAP-ENV:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>` + strconv.Itoa(code) + `</errorCode><errorDescription>Rejected</errorDescription></UPnPError></detail></SOAP-ENV:Fault></SOAP-ENV:Body></SOAP-ENV:Envelope>`
}

func fixtureManager(client *http.Client) *Manager {
	network := localNetwork{name: "loopback", index: 1, local: netip.MustParseAddr("127.0.0.1"), prefix: netip.MustParsePrefix("127.0.0.0/8")}
	return &Manager{
		config: Config{DiscoveryInterval: time.Minute}, networks: []localNetwork{network},
		httpClient: client, now: time.Now, notify: func(string) {},
		devices: make(map[string]deviceRecord), byUDN: make(map[string]string), pending: make(map[string]pendingInspection),
		xmlCache: make(map[string]xmlCacheEntry), inspectionSlots: make(chan struct{}, maxConcurrentInspections),
	}
}

func fixtureAdvertisement(location, udn, bootID string) advertisement {
	network := localNetwork{name: "loopback", index: 1, local: netip.MustParseAddr("127.0.0.1"), prefix: netip.MustParsePrefix("127.0.0.0/8")}
	return advertisement{
		source: netip.MustParseAddrPort("127.0.0.1:1900"), network: network,
		location: location, udn: udn, bootID: bootID, maxAge: time.Minute, kind: "alive",
	}
}

func fixtureRecord(id, controlURL string) deviceRecord {
	network := localNetwork{name: "loopback", index: 1, local: netip.MustParseAddr("127.0.0.1"), prefix: netip.MustParsePrefix("127.0.0.0/8")}
	return deviceRecord{
		device: output.Device{ID: id, Online: true, Protocol: output.ProtocolUPnP}, udn: "uuid:observation-fixture",
		descriptionURL: controlURL, controlURL: controlURL,
		serviceType: "urn:schemas-upnp-org:service:AVTransport:1",
		actions: map[string]bool{
			"SetAVTransportURI": true, "Play": true, "Pause": true, "Stop": true,
			"Seek": true, "GetTransportInfo": true, "GetPositionInfo": true, "GetMediaInfo": true,
		},
		queries: map[string]bool{"GetPositionInfo": true},
		source:  netip.MustParseAddr("127.0.0.1"), network: network, expiresAt: time.Now().Add(time.Minute),
	}
}

func waitForDevice(t *testing.T, manager *Manager, predicate func(output.Device) bool) output.Device {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, value := range manager.Devices() {
			if predicate(value) {
				return value
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("renderer did not reach expected state")
	return output.Device{}
}

func waitForInspections(t *testing.T, manager *Manager) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(manager.inspectionSlots) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("renderer inspections did not finish")
}
