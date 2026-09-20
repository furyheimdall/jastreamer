package browseroutput

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func captureDiagnosticLogs(t *testing.T, run func()) string {
	t.Helper()
	var buffer bytes.Buffer
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&buffer)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(writer)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	}()
	run()
	return buffer.String()
}

func TestOwnerRejectionLogsAreRedactedAndRateLimited(t *testing.T) {
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	value := &registration{
		id:        "browser:server-generated",
		token:     "registered-owner-secret",
		address:   "192.0.2.10",
		expiresAt: now.Add(time.Hour),
	}
	service := &Service{
		now:     func() time.Time { return now },
		devices: map[string]*registration{value.id: value},
	}

	logs := captureDiagnosticLogs(t, func() {
		for range 3 {
			_, _ = service.ownerLocked(value.id, "attacker-supplied-secret", value.address)
		}
		now = now.Add(diagnosticSummaryInterval)
		_, _ = service.ownerLocked(value.id, value.token, "198.51.100.99")
		_, _ = service.ownerLocked("browser:client-supplied-unknown", "unknown-secret", value.address)
	})

	if count := strings.Count(logs, "event=owner_rejected"); count != 2 {
		t.Fatalf("owner rejection log count=%d, logs=%q", count, logs)
	}
	for _, expected := range []string{`reason="owner_token_mismatch"`, `reason="address_mismatch"`, "repeats=2", "rejection_count=4"} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("owner rejection logs missing %q: %q", expected, logs)
		}
	}
	for _, secret := range []string{"registered-owner-secret", "attacker-supplied-secret", "192.0.2.10", "198.51.100.99", "browser:client-supplied-unknown", "unknown-secret"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("owner rejection logs exposed %q: %q", secret, logs)
		}
	}
}

func TestLeaseExpiryLogIdentifiesStoppedHeartbeatAndLostPlayback(t *testing.T) {
	lastSeen := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	now := lastSeen.Add(20 * time.Second)
	value := &registration{
		id:        "browser:server-generated",
		expiresAt: lastSeen.Add(leaseDuration),
		lastSeen:  lastSeen,
		pending: &pendingCommand{
			command: Command{Sequence: 7, Action: "play", PlayID: "server-play-id"},
			result:  make(chan error, 1),
		},
		resource: &output.Resource{PlayID: "server-play-id"},
		observation: output.Observation{
			State: "playing", TransportStatus: "OK", PositionMS: 42000, PlayID: "server-play-id", CommandSequence: 6,
		},
		diagnostics: registrationDiagnostics{
			lastPoll:                 lastSeen.Add(2 * time.Second),
			lastRenew:                lastSeen.Add(3 * time.Second),
			lastReport:               lastSeen.Add(4 * time.Second),
			lastDeliveredSequence:    7,
			lastDeliveryAt:           lastSeen.Add(2 * time.Second),
			lastAcknowledgedSequence: 6,
			lastAcknowledgementAt:    lastSeen.Add(4 * time.Second),
		},
	}
	service := &Service{devices: map[string]*registration{value.id: value}}

	logs := captureDiagnosticLogs(t, func() {
		if !service.removeExpiredLocked(now) {
			t.Fatal("expired registration was not removed")
		}
	})

	for _, expected := range []string{
		"event=lease_expired",
		"lease_expired_age_ms=5000",
		"last_seen_age_ms=20000",
		"last_poll_age_ms=18000",
		"last_renew_age_ms=17000",
		"last_report_age_ms=16000",
		`play_id="server-play-id"`,
		"pending_sequence=7",
		`pending_action="play"`,
		"last_delivered_sequence=7",
		"last_acknowledged_sequence=6",
		`state="playing"`,
		"position_ms=42000",
		`event=command_cancelled renderer_id="browser:server-generated"`,
		`reason="lease_expired"`,
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("lease expiry logs missing %q: %q", expected, logs)
		}
	}
}
