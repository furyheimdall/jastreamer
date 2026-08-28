package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

type transportK17Adapter struct {
	mediaURL  string
	uriCalls  int
	playCalls int
	uriError  error
	playError error
}

func (adapter *transportK17Adapter) RendererID() playback.RendererID { return "transport-renderer" }
func (adapter *transportK17Adapter) ZoneID() playback.ZoneID         { return "transport" }
func (adapter *transportK17Adapter) SetAVTransportURI(_ context.Context, resource playback.MediaResource) error {
	adapter.mediaURL = resource.URL
	adapter.uriCalls++
	return adapter.uriError
}

func (adapter *transportK17Adapter) Play(context.Context) error {
	adapter.playCalls++
	return adapter.playError
}
func (adapter *transportK17Adapter) Pause(context.Context) error               { return nil }
func (adapter *transportK17Adapter) Stop(context.Context) error                { return nil }
func (adapter *transportK17Adapter) Seek(context.Context, time.Duration) error { return nil }

type transportK17UPnP struct{ adapter *transportK17Adapter }

func (provider transportK17UPnP) Scan(context.Context) (upnp.ScanResult, error) {
	return upnp.ScanResult{}, nil
}
func (provider transportK17UPnP) LastScan() upnp.ScanResult { return upnp.ScanResult{} }
func (provider transportK17UPnP) PlaybackAdapter(playback.RendererID, playback.ZoneID) (playback.K17PlaybackAdapter, error) {
	return provider.adapter, nil
}

type transportMediaFixture struct {
	fixture       fixture
	controller    string
	rendererID    playback.RendererID
	rendererToken string
	mediaService  *media.Service
	signer        *media.Signer
	adapter       *transportK17Adapter
}

func newTransportMediaFixture(t *testing.T, kind playback.RendererKind) transportMediaFixture {
	t.Helper()
	value := newFixture(t)
	controller := pairController(t, value)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	path := filepath.Join(root, "track.flac")
	if err := os.WriteFile(path, []byte("transport-media"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	track := catalog.Track{
		RootID: "transport-root", TrackID: "track-a", RelativePath: "track.flac", Format: catalog.FormatFLAC,
		Fingerprint: strings.Repeat("c", 64), FileVersion: catalog.FileVersion{Size: info.Size(), Modified: info.ModTime()}, Available: true,
	}
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "transport-key", Key: []byte(strings.Repeat("k", 32)), Clock: &apiClock{now: now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	mediaService, err := media.NewService(media.ServiceConfig{
		Signer: signer, Authorizer: value.store,
		Snapshot: func(context.Context) catalog.Snapshot {
			return catalog.Snapshot{Tracks: map[catalog.TrackID]catalog.Track{"track-a": track}}
		},
		Roots: func(context.Context) map[catalog.RootID]string {
			return map[catalog.RootID]string{"transport-root": root}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := value.store.CreateZone(context.Background(), playback.ZoneDefinition{ID: "transport", DisplayName: "Transport"}); err != nil {
		t.Fatal(err)
	}
	rendererID := playback.RendererID("transport-renderer")
	var rendererToken string
	switch kind {
	case playback.RendererKindK17:
		_, err = value.store.UpsertK17Renderer(context.Background(), playback.K17Renderer{
			ID: rendererID, DisplayName: "K17", State: playback.RendererAvailable, Model: "FiiO K17",
			ProtocolInfo: "http-get:*:audio/flac:*", LastSeenAt: now,
		})
	case playback.RendererKindCustom:
		credential := pairRole(t, value, security.RoleRenderer, "Transport renderer")
		rendererID = playback.RendererID(credential.Device.ID)
		rendererToken = credential.Token
		_, err = value.store.UpsertCustomRenderer(context.Background(), playback.CustomRenderer{
			ID: rendererID, DisplayName: "Custom", State: playback.RendererConnected,
			ProtocolMajor: 3, Capabilities: []string{"media:audio/flac", "command:play"}, LastSeenAt: now,
		})
	default:
		t.Fatalf("unsupported renderer kind %q", kind)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := value.store.AssignRenderer(context.Background(), playback.AssignmentRequest{ZoneID: "transport", RendererID: rendererID}); err != nil {
		t.Fatal(err)
	}
	if _, err := value.store.Enqueue(context.Background(), playback.EnqueueRequest{
		ZoneID: "transport", IdempotencyKey: "seed", Tracks: []playback.QueueTrack{
			{ID: "track-a", Available: true}, {ID: "track-a", Available: true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return transportMediaFixture{
		fixture: value, controller: controller.Token, rendererID: rendererID, rendererToken: rendererToken,
		mediaService: mediaService, signer: signer, adapter: &transportK17Adapter{},
	}
}

func (value transportMediaFixture) handler(config api.Config) http.Handler {
	config.Security = value.fixture.manager
	config.Queue = value.fixture.store
	config.Catalog = *value.fixture.catalog
	config.Media = value.mediaService
	config.UPnP = transportK17UPnP{adapter: value.adapter}
	return api.New(config)
}

func startTransport(t *testing.T, server *httptest.Server, token string) {
	t.Helper()
	startTransportWithHost(t, server, token, "", "")
}

func startTransportWithHost(t *testing.T, server *httptest.Server, token, host, forwardedHost string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/v1/zones/transport/transport", strings.NewReader(`{"command":"start"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Host = host
	request.Header.Set("X-Forwarded-Host", forwardedHost)
	request.Header.Set("Forwarded", "host="+forwardedHost)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("If-Match", "1")
	request.Header.Set("Idempotency-Key", "start")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("start = %d %s", response.StatusCode, body)
	}
}

func pullTransportMedia(t *testing.T, client *http.Client, mediaURL string) {
	t.Helper()
	parsed, err := url.Parse(mediaURL)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		t.Fatalf("media URL contains bearer URI material: %q (%v)", mediaURL, err)
	}
	response, err := client.Get(mediaURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || string(body) != "transport-media" {
		t.Fatalf("media pull = %d %q", response.StatusCode, body)
	}
}

func Test_Transport_K17_start_uses_bound_private_HTTP_compatibility_origin(t *testing.T) {
	// Given
	value := newTransportMediaFixture(t, playback.RendererKindK17)
	mediaServer := httptest.NewServer(api.MediaOnly(value.mediaService))
	defer mediaServer.Close()
	mediaOrigin, err := url.Parse(mediaServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	server := newTrustedTransportTLSServer(t, value, trustedIPv4TransportServer(api.Config{
		K17HTTPEnabled: true, K17MediaBaseURL: mediaServer.URL, K17MediaListenerAddress: mediaOrigin.Host,
	}))

	// When
	startTransport(t, server, value.controller)

	// Then
	if !strings.HasPrefix(value.adapter.mediaURL, mediaServer.URL+"/media/v1/") {
		t.Fatalf("K17 media URL = %q, want compatibility origin %q", value.adapter.mediaURL, mediaServer.URL)
	}
	pullTransportMedia(t, mediaServer.Client(), value.adapter.mediaURL)
	apiResponse, err := mediaServer.Client().Get(mediaServer.URL + "/api/v1/identity")
	if err != nil {
		t.Fatal(err)
	}
	defer apiResponse.Body.Close()
	if apiResponse.StatusCode != http.StatusNotFound {
		t.Fatalf("plaintext API = %d, want 404", apiResponse.StatusCode)
	}
}

func Test_Transport_K17_start_rejects_empty_malformed_public_redirect_and_mismatched_overrides(t *testing.T) {
	tests := []struct {
		name     string
		enabled  bool
		baseURL  string
		listener string
	}{
		{name: "disabled", baseURL: "http://127.0.0.1:41001", listener: "127.0.0.1:41001"},
		{name: "empty", enabled: true, listener: "127.0.0.1:41001"},
		{name: "malformed", enabled: true, baseURL: "://", listener: "127.0.0.1:41001"},
		{name: "public", enabled: true, baseURL: "http://203.0.113.10:41001", listener: "203.0.113.10:41001"},
		{name: "redirect path", enabled: true, baseURL: "http://127.0.0.1:41001/redirect", listener: "127.0.0.1:41001"},
		{name: "mismatched origin", enabled: true, baseURL: "http://127.0.0.1:41001", listener: "127.0.0.1:41002"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			value := newTransportMediaFixture(t, playback.RendererKindK17)
			server := newTrustedTransportTLSServer(t, value, trustedIPv4TransportServer(api.Config{
				K17HTTPEnabled: test.enabled, K17MediaBaseURL: test.baseURL, K17MediaListenerAddress: test.listener,
			}))

			// When
			startTransport(t, server, value.controller)

			// Then
			if !strings.HasPrefix(value.adapter.mediaURL, server.URL+"/media/v1/") {
				t.Fatalf("K17 media URL = %q, want HTTPS fallback %q", value.adapter.mediaURL, server.URL)
			}
			pullTransportMedia(t, server.Client(), value.adapter.mediaURL)
		})
	}
}
