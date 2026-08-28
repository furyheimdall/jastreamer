package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

type eventTicketClock struct{ now time.Time }

func (clock eventTicketClock) Now() time.Time { return clock.now }

func TestEventTicket_is_256_bit_single_use(t *testing.T) {
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	store := newEventTicketStore(func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{0x5a}, 64)))
	ticket, expiresAt, issueErr := store.issue("bearer-secret", "device-1")
	if issueErr != nil {
		t.Fatalf("issue: %v", issueErr)
	}
	authenticate := func(context.Context, string) (security.Device, error) {
		return security.Device{ID: "device-1"}, nil
	}

	// When
	device, firstErr := store.consume(context.Background(), ticket, authenticate)
	_, replayErr := store.consume(context.Background(), ticket, authenticate)

	// Then
	if len(ticket) != eventTicketEncodedSize || expiresAt.Sub(now) != eventTicketTTL || device.ID != "device-1" || firstErr != nil {
		t.Fatalf("ticket length/TTL/device/error = %d/%v/%q/%v", len(ticket), expiresAt.Sub(now), device.ID, firstErr)
	}
	if !errors.Is(replayErr, errEventTicketUsed) {
		t.Fatalf("replay error = %v", replayErr)
	}
}

func TestEventTicket_rejects_authenticated_device_that_differs_from_bound_device(t *testing.T) {
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	store := newEventTicketStore(func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{0x21}, 32)))
	ticket, _, issueErr := store.issue("bearer-secret", "device-bound")
	if issueErr != nil {
		t.Fatalf("issue: %v", issueErr)
	}

	// When
	_, err := store.consume(context.Background(), ticket, func(context.Context, string) (security.Device, error) {
		return security.Device{ID: "device-other"}, nil
	})

	// Then
	if !errors.Is(err, errEventTicketInvalid) {
		t.Fatalf("device binding error = %v", err)
	}
}

func TestEventTicket_rejects_expiry_without_waiting(t *testing.T) {
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	store := newEventTicketStore(func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{0x31}, 32)))
	ticket, expiresAt, issueErr := store.issue("bearer-secret", "device-1")
	if issueErr != nil {
		t.Fatalf("issue: %v", issueErr)
	}
	now = expiresAt

	// When
	_, err := store.consume(context.Background(), ticket, func(context.Context, string) (security.Device, error) {
		return security.Device{}, nil
	})

	// Then
	if !errors.Is(err, errEventTicketExpired) {
		t.Fatalf("expiry error = %v", err)
	}
}

func TestEventTicketHandler_returns_ticket_without_leaking_bearer(t *testing.T) {
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	manager, managerErr := security.NewManager(security.Config{
		SetupSecret: "setup", StatePath: filepath.Join(t.TempDir(), "security.json"), Clock: eventTicketClock{now: now},
	})
	if managerErr != nil {
		t.Fatalf("manager: %v", managerErr)
	}
	admin, bootstrapErr := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
	if bootstrapErr != nil {
		t.Fatalf("bootstrap: %v", bootstrapErr)
	}
	service := &server{config: Config{Security: manager}, eventHub: newEventBroker()}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/event-tickets", nil)
	request.Header.Set("Authorization", "Bearer "+admin.Token)
	response := httptest.NewRecorder()

	// When
	service.eventTicket(response, request)

	// Then
	var body struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != http.StatusCreated || len(body.Ticket) != eventTicketEncodedSize || strings.Contains(response.Body.String(), admin.Token) {
		t.Fatalf("status/ticket length/body = %d/%d/%q", response.Code, len(body.Ticket), response.Body.String())
	}
}

func TestEvents_rejects_long_lived_bearer_instead_of_treating_it_as_a_ticket(t *testing.T) {
	// Given
	manager, managerErr := security.NewManager(security.Config{
		SetupSecret: "setup", StatePath: filepath.Join(t.TempDir(), "security.json"),
	})
	if managerErr != nil {
		t.Fatalf("manager: %v", managerErr)
	}
	admin, bootstrapErr := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
	if bootstrapErr != nil {
		t.Fatalf("bootstrap: %v", bootstrapErr)
	}
	service := &server{config: Config{Security: manager}, eventHub: newEventBroker()}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+admin.Token)
	response := httptest.NewRecorder()

	// When
	service.events(response, request)

	// Then
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "EVENT_TICKET_REQUIRED") {
		t.Fatalf("bearer event request = %d %s", response.Code, response.Body.String())
	}
}

func TestEventTicket_reauthenticates_to_reject_revoked_device(t *testing.T) {
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	manager, managerErr := security.NewManager(security.Config{
		SetupSecret: "setup", StatePath: filepath.Join(t.TempDir(), "security.json"), Clock: eventTicketClock{now: now},
	})
	if managerErr != nil {
		t.Fatalf("manager: %v", managerErr)
	}
	admin, bootstrapErr := manager.Bootstrap(context.Background(), "setup", security.Registration{Name: "Admin"})
	if bootstrapErr != nil {
		t.Fatalf("bootstrap: %v", bootstrapErr)
	}
	code, codeErr := manager.GeneratePairingCode(context.Background(), admin.Token)
	if codeErr != nil {
		t.Fatalf("pairing code: %v", codeErr)
	}
	controller, pairErr := manager.Pair(context.Background(), code.Value, security.Registration{Name: "Controller"}, "test")
	if pairErr != nil {
		t.Fatalf("pair: %v", pairErr)
	}
	store := newEventTicketStore(func() time.Time { return now }, bytes.NewReader(bytes.Repeat([]byte{0x42}, 32)))
	ticket, _, issueErr := store.issue(controller.Token, controller.Device.ID)
	if issueErr != nil {
		t.Fatalf("issue: %v", issueErr)
	}
	if revokeErr := manager.Revoke(context.Background(), admin.Token, controller.Device.ID); revokeErr != nil {
		t.Fatalf("revoke: %v", revokeErr)
	}

	// When
	_, err := store.consume(context.Background(), ticket, manager.Authenticate)

	// Then
	if !errors.Is(err, security.ErrTokenRevoked) {
		t.Fatalf("revocation error = %v", err)
	}
}
