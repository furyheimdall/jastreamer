package compatibility_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/jakestreamer/jstreamer-server/internal/compatibility"
)

func TestNegotiate_selects_highest_common_major_without_product_version(t *testing.T) {
	// Given
	peerMajors := []compatibility.Major{compatibility.Major1, compatibility.Major2}

	// When
	session, err := compatibility.Negotiate(peerMajors)

	// Then
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	if session.Major() != compatibility.Major2 {
		t.Fatalf("negotiated major = %d", session.Major())
	}
}

func TestNegotiate_returns_typed_upgrade_error_when_major_is_omitted_or_unsupported(t *testing.T) {
	for _, test := range []struct {
		name   string
		majors []compatibility.Major
	}{
		{name: "omitted", majors: nil},
		{name: "unsupported", majors: []compatibility.Major{9}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, err := compatibility.Negotiate(test.majors)

			// Then
			var protocolError *compatibility.ProtocolError
			if !errors.As(err, &protocolError) {
				t.Fatalf("error type = %T (%v)", err, err)
			}
			if protocolError.HTTPStatus != http.StatusUpgradeRequired || protocolError.Code != "UNSUPPORTED_PROTOCOL_MAJOR" {
				t.Fatalf("protocol error = %#v", protocolError)
			}
		})
	}
}

func TestParseRequest_accepts_additive_fields_unknown_capabilities_and_unknown_response_enums(t *testing.T) {
	for _, test := range []struct {
		name string
		kind compatibility.PeerKind
		file string
	}{
		{name: "control v1", kind: compatibility.PeerControl, file: "control-v1.json"},
		{name: "control v2", kind: compatibility.PeerControl, file: "control-v2.json"},
		{name: "renderer v1", kind: compatibility.PeerRenderer, file: "renderer-v1.json"},
		{name: "renderer v2", kind: compatibility.PeerRenderer, file: "renderer-v2.json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given
			payload, err := os.ReadFile(filepath.Join("testdata", test.file))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}

			// When
			request, err := compatibility.ParseRequest(test.kind, payload)

			// Then
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			if request.ID() == "" || len(request.Capabilities()) == 0 {
				t.Fatalf("parsed request = %#v", request)
			}
		})
	}
}

func TestParseRequest_rejects_missing_known_required_field(t *testing.T) {
	for _, test := range []struct {
		name    string
		kind    compatibility.PeerKind
		payload string
	}{
		{name: "control requestId", kind: compatibility.PeerControl, payload: `{"protocolMajor":1,"capabilities":["queue"]}`},
		{name: "renderer commandId", kind: compatibility.PeerRenderer, payload: `{"protocolMajor":1,"capabilities":["play"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, err := compatibility.ParseRequest(test.kind, []byte(test.payload))

			// Then
			var requestError *compatibility.RequestError
			if !errors.As(err, &requestError) || requestError.Code != "INVALID_REQUEST" {
				t.Fatalf("error = %T (%v)", err, err)
			}
		})
	}
}

func TestAdapter_unknown_behavior_is_per_request_and_does_not_mutate_state(t *testing.T) {
	// Given
	session, err := compatibility.Negotiate([]compatibility.Major{compatibility.Major2})
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	adapter := compatibility.NewAdapter(session)
	payload, err := os.ReadFile(filepath.Join("testdata", "renderer-v2-unknown-command.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	request, err := compatibility.ParseRequest(compatibility.PeerRenderer, payload)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	// When
	_, err = adapter.Handle(request)

	// Then
	var requestError *compatibility.RequestError
	if !errors.As(err, &requestError) || requestError.Code != "UNSUPPORTED_COMMAND" || requestError.RequestID != "renderer-v2-unknown" {
		t.Fatalf("error = %T (%v)", err, err)
	}
	if adapter.AcceptedRequests() != 0 {
		t.Fatalf("accepted requests = %d", adapter.AcceptedRequests())
	}
}

func TestRunFixture_supports_all_server_peer_cells_in_both_start_orders(t *testing.T) {
	for _, order := range []compatibility.StartOrder{compatibility.OldFirst, compatibility.NewFirst} {
		for _, test := range []struct {
			kind  compatibility.PeerKind
			peer  string
			wire  string
			major compatibility.Major
		}{
			{kind: compatibility.PeerControl, peer: "control-old-peer.json", wire: "control-v1.json", major: compatibility.Major1},
			{kind: compatibility.PeerControl, peer: "control-current-peer.json", wire: "control-v2.json", major: compatibility.Major2},
			{kind: compatibility.PeerRenderer, peer: "renderer-old-peer.json", wire: "renderer-v1.json", major: compatibility.Major1},
			{kind: compatibility.PeerRenderer, peer: "renderer-current-peer.json", wire: "renderer-v2.json", major: compatibility.Major2},
		} {
			name := string(order) + "/" + test.peer
			t.Run(name, func(t *testing.T) {
				// Given
				peer, err := os.ReadFile(filepath.Join("testdata", test.peer))
				if err != nil {
					t.Fatalf("read peer: %v", err)
				}
				wire, err := os.ReadFile(filepath.Join("testdata", test.wire))
				if err != nil {
					t.Fatalf("read wire: %v", err)
				}

				// When
				report, err := compatibility.RunFixture(compatibility.FixtureInput{Kind: test.kind, Order: order, Peer: peer, Wire: wire})

				// Then
				if err != nil {
					t.Fatalf("run fixture: %v", err)
				}
				if report.ProtocolMajor != test.major || report.Status != "compatible" {
					t.Fatalf("report = %#v", report)
				}
			})
		}
	}
}

func TestRunFixture_executes_start_steps_in_requested_order(t *testing.T) {
	peer, err := os.ReadFile(filepath.Join("testdata", "renderer-current-peer.json"))
	if err != nil {
		t.Fatalf("read peer: %v", err)
	}
	wire, err := os.ReadFile(filepath.Join("testdata", "renderer-v2.json"))
	if err != nil {
		t.Fatalf("read wire: %v", err)
	}
	for _, test := range []struct {
		order compatibility.StartOrder
		steps []string
	}{
		{order: compatibility.OldFirst, steps: []string{"start-peer", "start-server", "negotiate", "parse-request", "handle-request"}},
		{order: compatibility.NewFirst, steps: []string{"start-server", "start-peer", "negotiate", "parse-request", "handle-request"}},
	} {
		t.Run(string(test.order), func(t *testing.T) {
			// When
			report, err := compatibility.RunFixture(compatibility.FixtureInput{Kind: compatibility.PeerRenderer, Order: test.order, Peer: peer, Wire: wire})
			if err != nil {
				t.Fatalf("run fixture: %v", err)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatalf("encode report: %v", err)
			}
			var trace struct {
				Steps []string `json:"steps"`
			}
			if err := json.Unmarshal(encoded, &trace); err != nil {
				t.Fatalf("decode trace: %v", err)
			}

			// Then
			if !slices.Equal(trace.Steps, test.steps) {
				t.Fatalf("steps = %v, want %v", trace.Steps, test.steps)
			}
		})
	}
}

func TestRunFixture_uses_candidate_server_major_range(t *testing.T) {
	peer, err := os.ReadFile(filepath.Join("testdata", "control-old-peer.json"))
	if err != nil {
		t.Fatalf("read peer: %v", err)
	}
	wire, err := os.ReadFile(filepath.Join("testdata", "control-v1.json"))
	if err != nil {
		t.Fatalf("read wire: %v", err)
	}

	_, err = compatibility.RunFixture(compatibility.FixtureInput{
		Kind:         compatibility.PeerControl,
		Order:        compatibility.OldFirst,
		ServerMajors: []compatibility.Major{compatibility.Major2},
		Peer:         peer,
		Wire:         wire,
	})

	var protocolError *compatibility.ProtocolError
	if !errors.As(err, &protocolError) ||
		protocolError.Code != "UNSUPPORTED_PROTOCOL_MAJOR" {
		t.Fatalf("server without N-1 error = %v", err)
	}
}

func TestReleasedPeerFixtures_have_immutable_digests(t *testing.T) {
	fixtures := map[string]string{
		"control-old-peer.json":      "09c50cf61fe18bdbb131c02a76c9947125f0573222ac10721aee458c47da0637",
		"control-current-peer.json":  "4aa3abfcb8556b71f6b0cd9233297309eb9829f6ac2d560849a0bb68d39e814e",
		"renderer-old-peer.json":     "9493f2a5aac88110b6f2898d94c6fa2b021fccb5051fe6c436e7efa7ebb66015",
		"renderer-current-peer.json": "02cb6cbca34a063e7d2b36cfd5ca387f146291b228aaeccf3bad75ce5bcfea8d",
	}
	for name, expected := range fixtures {
		// Given
		payload, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		// When
		digest := sha256.Sum256(payload)

		// Then
		if hex.EncodeToString(digest[:]) != expected {
			t.Fatalf("%s digest = %s", name, hex.EncodeToString(digest[:]))
		}
	}
}
