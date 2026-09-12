package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/auth"
	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/discovery"
	"github.com/jastreamer/jastreamer-server/internal/dlna"
	"github.com/jastreamer/jastreamer-server/internal/events"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/player"
	"github.com/jastreamer/jastreamer-server/internal/stream"
)

type apiFixture struct {
	server   *httptest.Server
	client   *http.Client
	metadata discovery.Metadata
}

func startAPI(t *testing.T, secure bool) apiFixture {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := database.Open(filepath.Join(dir, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	accounts, err := auth.New(db)
	if err != nil {
		t.Fatal(err)
	}
	discoveryService, err := discovery.New(ctx, db, "Test server", "test")
	if err != nil {
		t.Fatal(err)
	}
	hub := events.New()
	catalog, err := library.New(ctx, db, nil, filepath.Join(dir, "artwork"), hub.Publish)
	if err != nil {
		t.Fatal(err)
	}
	devices, err := dlna.New(dlna.Config{DiscoveryInterval: 30 * time.Second, Notify: hub.Publish})
	if err != nil {
		t.Fatal(err)
	}
	media, err := stream.New(catalog, stream.Config{BaseURL: func(output.Device) (string, error) { return "http://127.0.0.1:8080", nil }})
	if err != nil {
		t.Fatal(err)
	}
	outputs := output.NewManager(output.Backend{Protocol: output.ProtocolUPnP, Controller: devices, Media: media})
	playback, err := player.New(ctx, db, catalog, outputs, time.Second, hub.Publish)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.DataDir = dir
	path := filepath.Join(dir, "server.json")
	if err = config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	handler := New(Options{Context: ctx, ConfigPath: path, Config: cfg, Auth: accounts, Discovery: discoveryService, Library: catalog, Devices: outputs, Player: playback, Stream: media, Events: hub, UI: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Sign in</title>")}}})
	var server *httptest.Server
	if secure {
		server = httptest.NewTLSServer(handler)
	} else {
		server = httptest.NewServer(handler)
	}
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 5 * time.Second
	client.Jar, _ = cookiejar.New(nil)
	return apiFixture{server: server, client: client, metadata: discoveryService.Metadata()}
}

func (fixture apiFixture) request(t *testing.T, method, path, body string, change func(*http.Request)) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, fixture.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal("request construction failed")
	}
	if method != "GET" {
		request.Header.Set("Origin", fixture.server.URL)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Jastreamer-Request", "web")
	}
	if change != nil {
		change(request)
	}
	response, err := fixture.client.Do(request)
	if err != nil {
		t.Fatalf("HTTP request failed: %T", err)
	}
	return response
}

func expectStatus(t *testing.T, response *http.Response, status int) {
	t.Helper()
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != status {
		t.Fatalf("HTTP status=%d, want %d", response.StatusCode, status)
	}
}

const credentials = `{"username":"listener","password":"test-only-password-47"}`

func (fixture apiFixture) setup(t *testing.T) *http.Cookie {
	t.Helper()
	response := fixture.request(t, "POST", "/api/v1/setup", credentials, nil)
	cookies := response.Cookies()
	expectStatus(t, response, 201)
	for _, cookie := range cookies {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}
	t.Fatal("setup did not issue a session cookie")
	return nil
}

func TestDiscoveryIsPublicAndReturnsOnlyConnectionMetadata(t *testing.T) {
	fixture := startAPI(t, false)
	response := fixture.request(t, "GET", "/api/v1/discovery", "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("HTTP status=%d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	expectedFields := []string{"product", "protocol", "id", "name", "version"}
	if len(fields) != len(expectedFields) {
		t.Fatalf("discovery response fields=%v", fields)
	}
	for _, field := range expectedFields {
		if _, exists := fields[field]; !exists {
			t.Fatalf("discovery response omitted %q: %v", field, fields)
		}
	}
	var metadata discovery.Metadata
	if err = json.Unmarshal(body, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != fixture.metadata {
		t.Fatalf("discovery metadata=%#v, want %#v", metadata, fixture.metadata)
	}
}

func TestFirstSetupRejectsCrossOriginRebindingAndMissingCSRFHeader(t *testing.T) {
	fixture := startAPI(t, false)
	for _, attack := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"origin", func(request *http.Request) { request.Header.Set("Origin", "http://untrusted.example") }},
		{"host", func(request *http.Request) { request.Host = "untrusted.example"; request.Header.Del("Origin") }},
		{"header", func(request *http.Request) { request.Header.Del("X-Jastreamer-Request") }},
	} {
		t.Run(attack.name, func(t *testing.T) {
			expectStatus(t, fixture.request(t, "POST", "/api/v1/setup", credentials, attack.change), 403)
		})
	}
	response := fixture.request(t, "GET", "/api/v1/setup", "", nil)
	var state struct {
		Required bool `json:"required"`
	}
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !state.Required {
		t.Fatal("rejected requests claimed the first account")
	}
	fixture.setup(t)
	expectStatus(t, fixture.request(t, "POST", "/api/v1/setup", credentials, nil), 409)
}

func TestSessionCookieFollowsHTTPAndHTTPSAndLogoutRevokesItsValue(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP", true: "HTTPS"}[secure], func(t *testing.T) {
			fixture := startAPI(t, secure)
			cookie := fixture.setup(t)
			if !cookie.HttpOnly || cookie.Secure != secure || cookie.Domain != "" || cookie.Path != "/" || cookie.SameSite != http.SameSiteStrictMode {
				t.Fatal("session cookie transport or isolation flags are incorrect")
			}
			expectStatus(t, fixture.request(t, "GET", "/api/v1/queue", "", nil), 200)
			expectStatus(t, fixture.request(t, "POST", "/api/v1/logout", "{}", nil), 204)
			expectStatus(t, fixture.request(t, "GET", "/api/v1/queue", "", func(request *http.Request) { request.AddCookie(cookie) }), 401)
			// An expired/revoked cookie must not prevent a fresh password login.
			expectStatus(t, fixture.request(t, "POST", "/api/v1/login", credentials, func(request *http.Request) { request.AddCookie(cookie) }), 200)
			expectStatus(t, fixture.request(t, "GET", "/api/v1/queue", "", nil), 200)
		})
	}
}

func TestLogoutClosesAnAlreadyAuthenticatedEventStream(t *testing.T) {
	fixture := startAPI(t, false)
	fixture.setup(t)
	response := fixture.request(t, "GET", "/api/v1/events", "", nil)
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("SSE status=%d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("initial SSE failed: %T", err)
		}
		if line == "\n" {
			break
		}
	}
	expectStatus(t, fixture.request(t, "POST", "/api/v1/logout", "{}", nil), 204)
	finished := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, reader); finished <- err }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("revoked stream did not end cleanly: %T", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("logout left an authenticated event stream open")
	}
}

func TestPairingRouteRequiresAuthenticationAndUsesOutputLookup(t *testing.T) {
	fixture := startAPI(t, false)
	path := "/api/v1/renderers/missing/pairing"
	expectStatus(t, fixture.request(t, "POST", path, `{"pin":"1234","password":""}`, nil), http.StatusUnauthorized)
	fixture.setup(t)
	response := fixture.request(t, "POST", path, `{"pin":"1234","password":""}`, nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("pairing status=%d, want %d", response.StatusCode, http.StatusNotFound)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "OUTPUT_NOT_FOUND" {
		t.Fatalf("pairing route error=%q, want OUTPUT_NOT_FOUND", body.Error.Code)
	}
}

func TestIncompleteJSONBodyIsBoundedWithoutBlockingOtherRequests(t *testing.T) {
	fixture := startAPI(t, false)
	address := strings.TrimPrefix(fixture.server.URL, "http://")
	connection, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if err = connection.SetDeadline(time.Now().Add(requestBodyTimeout + 2*time.Second)); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(connection, "POST /api/v1/login HTTP/1.1\r\nHost: %s\r\nContent-Type: application/json\r\nX-Jastreamer-Request: web\r\nContent-Length: 200\r\nConnection: close\r\n\r\n{", address)
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, fixture.request(t, "GET", "/healthz", "", nil), 200)
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: "POST"})
	if err != nil {
		t.Fatalf("incomplete request did not receive a bounded response: %T", err)
	}
	expectStatus(t, response, http.StatusRequestTimeout)
	expectStatus(t, fixture.request(t, "GET", "/api/v1/setup", "", nil), 200)
}
