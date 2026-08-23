package api_test

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"
)

func TestContractFixture_has_exact_machine_readable_statuses(t *testing.T) {
	data, err := os.ReadFile("../../testdata/api/status-codes.json")
	if err != nil {
		t.Fatalf("read status fixture: %v", err)
	}
	var fixture map[string]struct {
		Status int    `json:"status"`
		Code   string `json:"code"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if fixture["pairing_code_used"].Status != http.StatusConflict || fixture["pairing_code_used"].Code != "PAIRING_CODE_USED" {
		t.Fatalf("pairing reuse fixture = %#v", fixture["pairing_code_used"])
	}
	for _, name := range []string{"certificate_mismatch", "blocked_explicit_head", "similar_no_signal", "similar_exhausted", "automatic_failure_limit"} {
		if fixture[name].Code == "" {
			t.Fatalf("fixture lacks required outcome %q", name)
		}
	}
}
