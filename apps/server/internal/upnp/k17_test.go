package upnp_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

func TestInspector_rejects_wrong_identity_firmware_and_protocolInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		device fixtureDevice
		want   error
	}{
		{name: "manufacturer", device: fixtureDevice{manufacturer: "Other", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol}, want: upnp.ErrIdentityRejected},
		{name: "model", device: fixtureDevice{manufacturer: "FiiO", model: "K19", firmware: "V261", protocolInfo: fixtureProtocol}, want: upnp.ErrIdentityRejected},
		{name: "firmware V260", device: fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V260", protocolInfo: fixtureProtocol}, want: upnp.ErrFirmwareRejected},
		{name: "malformed firmware suffix", device: fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261beta", protocolInfo: fixtureProtocol}, want: upnp.ErrFirmwareRejected},
		{name: "malformed firmware width", device: fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V2610000", protocolInfo: fixtureProtocol}, want: upnp.ErrFirmwareRejected},
		{name: "protocol info", device: fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: "http-get:*:video/mp4:*"}, want: upnp.ErrProtocolRejected},
		{name: "malformed protocol info", device: fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: "http-get:audio/flac"}, want: upnp.ErrProtocolRejected},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture := newFixture(t, test.device)
			candidate := fixture.candidate(t)
			inspector := fixture.inspector(t)
			// When
			_, err := inspector.InspectK17(context.Background(), candidate)
			// Then
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestInspector_rejects_description_redirect_off_subnet(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: "V261", protocolInfo: fixtureProtocol, redirectDescription: "http://192.0.2.20/device.xml"})
	inspector := fixture.inspector(t)

	// When
	_, err := inspector.InspectK17(context.Background(), fixture.candidate(t))

	// Then
	if !errors.Is(err, upnp.ErrOffSubnetURL) {
		t.Fatalf("error = %v", err)
	}
}

func TestInspector_rejects_unbounded_XML_entities(t *testing.T) {
	// Given
	fixture := newFixture(t, fixtureDevice{rawDescription: `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY x "boom">]><root>&x;</root>`})

	// When
	_, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))

	// Then
	if !errors.Is(err, upnp.ErrInvalidDescription) {
		t.Fatalf("error = %v", err)
	}
}

func TestInspector_accepts_minimum_or_newer_firmware_and_compatible_protocolInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, firmware, protocolInfo, protocol string
	}{
		{name: "minimum V261", firmware: "V261", protocolInfo: fixtureProtocol, protocol: "audio/flac"},
		{name: "newer V262", firmware: "V262", protocolInfo: fixtureProtocol, protocol: "audio/flac"},
		{name: "compatible among unrelated entries", firmware: "V262", protocolInfo: "http-get:*:video/mp4:*, http-get:*:audio/flac:DLNA.ORG_OP=01", protocol: "audio/flac"},
		{name: "protocol wildcard", firmware: "V262", protocolInfo: "*:*:audio/*:*", protocol: "audio/*"},
		{name: "L16", firmware: "V262", protocolInfo: "http-get:*:audio/L16;rate=44100;channels=2:*", protocol: "audio/L16"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture := newFixture(t, fixtureDevice{manufacturer: "FiiO", model: "FiiO K17", firmware: test.firmware, protocolInfo: test.protocolInfo, seek: true})

			// When
			device, err := fixture.inspector(t).InspectK17(context.Background(), fixture.candidate(t))
			// Then
			if err != nil {
				t.Fatal(err)
			}
			if device.Evidence.Firmware != test.firmware || device.Evidence.ProtocolInfo != test.protocolInfo {
				t.Fatalf("evidence = %+v", device.Evidence)
			}
			protocols := device.Protocols()
			if len(protocols) != 1 || protocols[0] != test.protocol {
				t.Fatalf("compatible protocols = %v", protocols)
			}
			if !device.Supports(upnp.ActionSeek) || device.ID == "" {
				t.Fatalf("device = %+v", device)
			}
		})
	}
}

func testCandidate(rawURL string) upnp.Candidate {
	return upnp.Candidate{Source: netip.MustParseAddrPort("127.0.0.1:1900"), Location: rawURL, USN: "uuid:k17"}
}
