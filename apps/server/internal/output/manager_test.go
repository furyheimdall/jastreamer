package output

import (
	"context"
	"errors"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/library"
)

type fakeController struct {
	device Device
	calls  []string
}

func (controller *fakeController) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

func (controller *fakeController) Refresh(context.Context) error { return nil }
func (controller *fakeController) Devices() []Device             { return []Device{controller.device} }
func (controller *fakeController) Device(id string) (Device, bool) {
	return controller.device, id == controller.device.ID
}
func (controller *fakeController) SetURI(context.Context, string, Resource) error {
	controller.calls = append(controller.calls, "set_uri")
	return nil
}
func (controller *fakeController) Play(context.Context, string) error {
	controller.calls = append(controller.calls, "play")
	return nil
}
func (controller *fakeController) Pause(context.Context, string) error { return nil }
func (controller *fakeController) Stop(context.Context, string) error  { return nil }
func (controller *fakeController) Seek(context.Context, string, int64) error {
	return nil
}
func (controller *fakeController) Observe(context.Context, string) (Observation, error) {
	return Observation{}, nil
}

type fakeMedia struct {
	prepared int
	revoked  int
}

func (media *fakeMedia) Prepare(_ context.Context, device Device, _ library.Track, _ string) (Resource, error) {
	media.prepared++
	return Resource{URL: device.Protocol + "://prepared"}, nil
}
func (media *fakeMedia) Revoke(string) { media.revoked++ }

type fakePairer struct {
	paired int
}

func (pairer *fakePairer) Pair(context.Context, string, PairingRequest) (PairingStatus, error) {
	pairer.paired++
	return PairingStatus{Required: false}, nil
}

func TestManagerRoutesByBackendIdentityAndProtocol(t *testing.T) {
	upnpController := &fakeController{device: Device{ID: "renderer-upnp", Name: "Living room", Protocol: ProtocolUPnP}}
	airplayController := &fakeController{device: Device{ID: "airplay:renderer", Name: "Living room", Protocol: ProtocolAirPlay}}
	upnpMedia := &fakeMedia{}
	airplayMedia := &fakeMedia{}
	pairer := &fakePairer{}
	manager := NewManager(
		Backend{Protocol: ProtocolUPnP, Controller: upnpController, Media: upnpMedia},
		Backend{Protocol: ProtocolAirPlay, Controller: airplayController, Media: airplayMedia, Pairer: pairer},
	)

	devices := manager.Devices()
	if len(devices) != 2 || devices[0].Protocol == devices[1].Protocol {
		t.Fatalf("aggregated devices lost backend protocol identity: %#v", devices)
	}
	if err := manager.SetURI(context.Background(), airplayController.device.ID, Resource{}); err != nil {
		t.Fatal(err)
	}
	if len(upnpController.calls) != 0 || len(airplayController.calls) != 1 || airplayController.calls[0] != "set_uri" {
		t.Fatalf("command crossed backend boundary: upnp=%v airplay=%v", upnpController.calls, airplayController.calls)
	}
	resource, err := manager.Prepare(context.Background(), devices[0], library.Track{}, "play")
	if err != nil {
		t.Fatal(err)
	}
	if resource.URL != devices[0].Protocol+"://prepared" {
		t.Fatalf("media preparation used wrong backend: %#v", resource)
	}
	manager.Revoke("play")
	if upnpMedia.revoked != 1 || airplayMedia.revoked != 1 {
		t.Fatalf("revocation did not reach every backend: upnp=%d airplay=%d", upnpMedia.revoked, airplayMedia.revoked)
	}
	if _, err := manager.Pair(context.Background(), airplayController.device.ID, PairingRequest{PIN: "1234"}); err != nil || pairer.paired != 1 {
		t.Fatalf("pairing did not reach AirPlay backend: paired=%d err=%v", pairer.paired, err)
	}
	if _, err := manager.Pair(context.Background(), upnpController.device.ID, PairingRequest{}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unpairable backend error=%v", err)
	}
}
