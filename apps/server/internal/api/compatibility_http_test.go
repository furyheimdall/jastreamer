package api_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/compatibility"
)

type discoveryResponse struct {
	ProtocolMajor           int      `json:"protocol_major"`
	SupportedProtocolMajors []int    `json:"supported_protocol_majors"`
	Capabilities            []string `json:"capabilities"`
	ProductVersion          string   `json:"product_version"`
	SourceRevision          string   `json:"source_revision"`
}

func TestDiscovery_selects_v3_and_emits_negotiation_headers(t *testing.T) {
	// Given
	value := newFixture(t)
	headers := map[string]string{compatibility.SupportedMajorsHeader: "3,2"}

	// When
	response := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", headers)

	// Then
	if response.Code != http.StatusOK {
		t.Fatalf("discovery = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get(compatibility.SupportedMajorsHeader) != "3,2" ||
		response.Header().Get(compatibility.SelectedMajorHeader) != "3" ||
		response.Header().Get("X-Jake-Protocol-Major") != "" {
		t.Fatalf("negotiation headers = %#v", response.Header())
	}
	var body discoveryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode discovery: %v", err)
	}
	if body.ProtocolMajor != 3 || !slices.Equal(body.SupportedProtocolMajors, []int{3, 2}) ||
		!slices.Contains(body.Capabilities, "catalog-browse") || body.ProductVersion != "2.7.4" ||
		body.SourceRevision != "fixture-revision" {
		t.Fatalf("discovery metadata = %#v", body)
	}
}

func TestDiscovery_falls_back_to_v2_without_v3_capabilities(t *testing.T) {
	// Given
	value := newFixture(t)
	headers := map[string]string{compatibility.SupportedMajorsHeader: "2"}

	// When
	response := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", headers)

	// Then
	if response.Code != http.StatusOK {
		t.Fatalf("discovery = %d %s", response.Code, response.Body.String())
	}
	if response.Header().Get(compatibility.SupportedMajorsHeader) != "3,2" ||
		response.Header().Get(compatibility.SelectedMajorHeader) != "2" ||
		response.Header().Get("X-Jake-Protocol-Major") != "" {
		t.Fatalf("negotiation headers = %#v", response.Header())
	}
	var body discoveryResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode discovery: %v", err)
	}
	if body.ProtocolMajor != 2 || slices.Contains(body.Capabilities, "catalog-browse") || slices.Contains(body.Capabilities, "renderer-session") {
		t.Fatalf("discovery metadata = %#v", body)
	}
}

func TestDiscovery_returns_426_when_no_major_is_common(t *testing.T) {
	// Given
	value := newFixture(t)
	headers := map[string]string{compatibility.SupportedMajorsHeader: "1"}

	// When
	response := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", headers)

	// Then
	if response.Code != http.StatusUpgradeRequired || responseCode(t, response) != "UNSUPPORTED_PROTOCOL_MAJOR" {
		t.Fatalf("no-common response = %d %s", response.Code, response.Body.String())
	}
}

func TestDiscovery_returns_426_when_supported_majors_are_omitted(t *testing.T) {
	// Given
	value := newFixture(t)

	// When
	response := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", nil)

	// Then
	if response.Code != http.StatusUpgradeRequired || responseCode(t, response) != "UNSUPPORTED_PROTOCOL_MAJOR" {
		t.Fatalf("omitted response = %d %s", response.Code, response.Body.String())
	}
}

func TestDiscovery_returns_426_when_supported_majors_are_malformed(t *testing.T) {
	// Given
	value := newFixture(t)
	headers := map[string]string{compatibility.SupportedMajorsHeader: "3,nope"}

	// When
	response := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", headers)

	// Then
	if response.Code != http.StatusUpgradeRequired || responseCode(t, response) != "UNSUPPORTED_PROTOCOL_MAJOR" {
		t.Fatalf("malformed response = %d %s", response.Code, response.Body.String())
	}
}

func TestDiscovery_ignores_the_legacy_protocol_major_header(t *testing.T) {
	// Given
	value := newFixture(t)
	headers := map[string]string{"X-Jake-Protocol-Major": "3"}

	// When
	response := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", headers)

	// Then
	if response.Code != http.StatusUpgradeRequired || responseCode(t, response) != "UNSUPPORTED_PROTOCOL_MAJOR" {
		t.Fatalf("legacy-header response = %d %s", response.Code, response.Body.String())
	}
}
