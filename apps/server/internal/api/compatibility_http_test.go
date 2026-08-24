package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestDiscovery_negotiates_highest_common_major_and_rejects_omission(t *testing.T) {
	// Given
	value := newFixture(t)

	// When
	omitted := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", nil)
	negotiated := request(t, value.handler, http.MethodGet, "/api/v1/discovery", value.admin.Token, "", map[string]string{"X-Jake-Protocol-Major": "1, 2"})

	// Then
	if omitted.Code != http.StatusUpgradeRequired || responseCode(t, omitted) != "UNSUPPORTED_PROTOCOL_MAJOR" {
		t.Fatalf("omitted negotiation = %d %s", omitted.Code, omitted.Body.String())
	}
	if negotiated.Code != http.StatusOK {
		t.Fatalf("negotiated discovery = %d %s", negotiated.Code, negotiated.Body.String())
	}
	var body struct {
		ProtocolMajor           int      `json:"protocol_major"`
		SupportedProtocolMajors []int    `json:"supported_protocol_majors"`
		Capabilities            []string `json:"capabilities"`
		ProductVersion          string   `json:"product_version"`
		SourceRevision          string   `json:"source_revision"`
	}
	if err := json.Unmarshal(negotiated.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode discovery: %v", err)
	}
	if body.ProtocolMajor != 2 || len(body.SupportedProtocolMajors) != 2 ||
		body.SupportedProtocolMajors[0] != 2 || body.SupportedProtocolMajors[1] != 1 || len(body.Capabilities) == 0 ||
		body.ProductVersion != "2.7.4" || body.SourceRevision != "fixture-revision" {
		t.Fatalf("discovery metadata = %#v", body)
	}
}
