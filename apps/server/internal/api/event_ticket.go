package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

const (
	eventTicketTTL             = 30 * time.Second
	eventTicketEncodedSize     = 43
	maxOutstandingEventTickets = 1024
)

var (
	errEventTicketInvalid  = errors.New("event ticket is invalid")
	errEventTicketExpired  = errors.New("event ticket has expired")
	errEventTicketUsed     = errors.New("event ticket was already used")
	errEventTicketCapacity = errors.New("too many outstanding event tickets")
)

type eventTicketRecord struct {
	bearer    string
	deviceID  security.DeviceID
	expiresAt time.Time
	used      bool
}

type eventTicketStore struct {
	mu      sync.Mutex
	now     func() time.Time
	random  io.Reader
	tickets map[[sha256.Size]byte]eventTicketRecord
}

func newDefaultEventTicketStore() *eventTicketStore {
	return newEventTicketStore(time.Now, rand.Reader)
}

func newEventTicketStore(now func() time.Time, random io.Reader) *eventTicketStore {
	return &eventTicketStore{now: now, random: random, tickets: make(map[[sha256.Size]byte]eventTicketRecord)}
}

func (store *eventTicketStore) issue(bearer string, deviceID security.DeviceID) (string, time.Time, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(store.random, raw); err != nil {
		return "", time.Time{}, fmt.Errorf("generate event ticket: %w", err)
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	now := store.now()
	expiresAt := now.Add(eventTicketTTL)
	digest := sha256.Sum256([]byte(ticket))
	store.mu.Lock()
	defer store.mu.Unlock()
	for key, record := range store.tickets {
		if !now.Before(record.expiresAt) {
			delete(store.tickets, key)
		}
	}
	if len(store.tickets) >= maxOutstandingEventTickets {
		return "", time.Time{}, errEventTicketCapacity
	}
	if _, exists := store.tickets[digest]; exists {
		return "", time.Time{}, errEventTicketInvalid
	}
	store.tickets[digest] = eventTicketRecord{bearer: bearer, deviceID: deviceID, expiresAt: expiresAt}
	return ticket, expiresAt, nil
}

func (store *eventTicketStore) consume(
	ctx context.Context,
	ticket string,
	authenticate func(context.Context, string) (security.Device, error),
) (security.Device, error) {
	if len(ticket) != eventTicketEncodedSize {
		return security.Device{}, errEventTicketInvalid
	}
	digest := sha256.Sum256([]byte(ticket))
	store.mu.Lock()
	record, exists := store.tickets[digest]
	if !exists {
		store.mu.Unlock()
		return security.Device{}, errEventTicketInvalid
	}
	if !store.now().Before(record.expiresAt) {
		delete(store.tickets, digest)
		store.mu.Unlock()
		return security.Device{}, errEventTicketExpired
	}
	if record.used {
		store.mu.Unlock()
		return security.Device{}, errEventTicketUsed
	}
	record.used = true
	store.tickets[digest] = record
	store.mu.Unlock()
	device, err := authenticate(ctx, record.bearer)
	if err != nil {
		return security.Device{}, fmt.Errorf("authenticate event ticket: %w", err)
	}
	if device.ID != record.deviceID {
		return security.Device{}, errEventTicketInvalid
	}
	return device, nil
}

func (service *server) authenticateEventRequest(writer http.ResponseWriter, request *http.Request) (security.Device, bool) {
	if ticket := request.URL.Query().Get("ticket"); ticket != "" {
		device, err := service.eventHub.tickets.consume(request.Context(), ticket, service.config.Security.Authenticate)
		if err != nil {
			switch {
			case errors.Is(err, errEventTicketExpired):
				invalid(writer, "EVENT_TICKET_EXPIRED", "event ticket has expired", http.StatusUnauthorized)
			case errors.Is(err, errEventTicketUsed):
				invalid(writer, "EVENT_TICKET_USED", "event ticket was already used", http.StatusUnauthorized)
			default:
				invalid(writer, "EVENT_TICKET_INVALID", "event ticket is invalid", http.StatusUnauthorized)
			}
			return security.Device{}, false
		}
		return device, true
	}
	invalid(writer, "EVENT_TICKET_REQUIRED", "event ticket is required", http.StatusUnauthorized)
	return security.Device{}, false
}

func (service *server) eventTicket(writer http.ResponseWriter, request *http.Request) {
	device, ok := service.authenticate(writer, request)
	if !ok {
		return
	}
	ticket, expiresAt, err := service.eventHub.tickets.issue(bearer(request), device.ID)
	if err != nil {
		invalid(writer, "INTERNAL", "event ticket generation failed", http.StatusInternalServerError)
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"ticket": ticket, "expires_at": expiresAt})
}
