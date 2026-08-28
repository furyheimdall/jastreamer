package upnp_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

func TestAdapter_happy_fixture_pulls_signed_original_and_exercises_actions(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol, seek: true})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: "renderer-k17", ZoneID: "living", HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	mediaURL := fixture.server.URL + "/media/v1/signed-fixture"

	// When
	if err := adapter.SetAVTransportURI(context.Background(), fixtureMediaResource(mediaURL)); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Play(context.Background()); err != nil {
		t.Fatal(err)
	}
	response, err := fixture.server.Client().Get(mediaURL)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close media: %v/%v", readErr, closeErr)
	}
	if err := adapter.Pause(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Seek(context.Background(), 7*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Then
	if string(body) != "fixture-media" {
		t.Fatalf("media = %q", body)
	}
	fixture.mu.Lock()
	actions := append([]string(nil), fixture.actions...)
	uri := fixture.mediaURL
	fixture.mu.Unlock()
	if uri != mediaURL || len(actions) != 5 {
		t.Fatalf("uri/actions = %q/%v", uri, actions)
	}
}

func TestAdapter_rejects_unadvertised_action_without_SOAP(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: "renderer-k17", ZoneID: "living", HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = adapter.Seek(context.Background(), time.Second)

	// Then
	if !errors.Is(err, upnp.ErrActionUnavailable) {
		t.Fatalf("error = %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.actions) != 0 {
		t.Fatalf("actions = %v", fixture.actions)
	}
}

func TestAdapter_accepts_private_HTTPS_media_URL_and_sends_nonempty_DIDL(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: "renderer-k17", ZoneID: "living", HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	mediaURL := "https://127.0.0.1:8443/media/v1/signed"

	// When
	err = adapter.SetAVTransportURI(context.Background(), fixtureMediaResource(mediaURL))
	// Then
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.mediaURL != mediaURL || fixture.mediaMetadata == "" {
		t.Fatalf("uri/metadata = %q/%q", fixture.mediaURL, fixture.mediaMetadata)
	}
}

func TestAdapter_rejects_off_subnet_media_URL_without_SOAP(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: "renderer-k17", ZoneID: "living", HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = adapter.SetAVTransportURI(context.Background(), fixtureMediaResource("https://192.0.2.20/media/v1/signed"))

	// Then
	if !errors.Is(err, upnp.ErrOffSubnetURL) {
		t.Fatalf("error = %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.actions) != 0 {
		t.Fatalf("actions = %v", fixture.actions)
	}
}

func TestAdapter_returns_typed_SOAP_fault_without_retrying_non_idempotent_action(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol, soapFaultAction: "Play"})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: "renderer-k17", ZoneID: "living", HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = adapter.Play(context.Background())

	// Then
	var fault *upnp.SOAPFault
	if !errors.As(err, &fault) || fault.Code != 701 || fault.Action != upnp.ActionPlay {
		t.Fatalf("error = %#v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.actions) != 1 {
		t.Fatalf("actions = %v", fixture.actions)
	}
}

func TestAdapter_honors_timeout_and_cancellation_with_typed_diagnostic(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol, blockAction: "Play"})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: "renderer-k17", ZoneID: "living", HTTPClient: fixture.server.Client(), ActionTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = adapter.Play(context.Background())
	select {
	case <-fixture.actionStarted:
	case <-time.After(time.Second):
		t.Fatal("SOAP action did not start")
	}
	close(fixture.releaseAction)

	// Then
	var diagnostic *upnp.DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Kind != upnp.DiagnosticTimeout || diagnostic.Action != upnp.ActionPlay {
		t.Fatalf("error = %#v", err)
	}
}

func TestAdapter_observes_transport_state_and_position(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol})
	device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := upnp.NewK17Adapter(upnp.AdapterConfig{Device: device, RendererID: "renderer-k17", ZoneID: "living", HTTPClient: fixture.server.Client()})
	if err != nil {
		t.Fatal(err)
	}

	// When
	state, err := adapter.Observe(context.Background())
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if state.Transport != upnp.TransportPlaying || state.Position != 7*time.Second || state.RendererID != "renderer-k17" || state.ZoneID != "living" {
		t.Fatalf("state = %+v", state)
	}
}
