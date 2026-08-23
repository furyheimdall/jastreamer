package security

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type storedDevice struct {
	Device
	TokenDigest string `json:"token_digest"`
}

type storedCode struct {
	Digest    string    `json:"code_digest"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
}

type failures struct {
	Count int       `json:"count"`
	Since time.Time `json:"since"`
}

type persistedState struct {
	BootstrapComplete bool                    `json:"bootstrap_complete"`
	Sequence          uint64                  `json:"sequence"`
	Devices           map[string]storedDevice `json:"devices"`
	Codes             map[string]storedCode   `json:"pairing_codes"`
	Failures          map[string]failures     `json:"pairing_failures"`
}

type Manager struct {
	mu          sync.Mutex
	config      Config
	setupDigest [32]byte
	state       persistedState
}

func NewManager(config Config) (*Manager, error) {
	if config.SetupSecret == "" || config.StatePath == "" {
		return nil, fmt.Errorf("security config: setup secret and state path are required")
	}
	if config.Clock == nil {
		config.Clock = realClock{}
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	if config.PairingTTL == 0 {
		config.PairingTTL = 5 * time.Minute
	}
	if config.MaxFailures == 0 {
		config.MaxFailures = 5
	}
	manager := &Manager{config: config, setupDigest: sha256.Sum256([]byte(config.SetupSecret))}
	manager.state = persistedState{Devices: map[string]storedDevice{}, Codes: map[string]storedCode{}, Failures: map[string]failures{}}
	data, err := os.ReadFile(config.StatePath)
	if err == nil {
		if err := json.Unmarshal(data, &manager.state); err != nil {
			return nil, fmt.Errorf("decode security state: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read security state: %w", err)
	}
	manager.ensureMaps()
	return manager, nil
}

func (manager *Manager) Bootstrap(ctx context.Context, secret string, registration Registration) (Credential, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return Credential{}, err
	}
	if manager.state.BootstrapComplete {
		return Credential{}, ErrBootstrapComplete
	}
	digest := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(digest[:], manager.setupDigest[:]) != 1 {
		return Credential{}, ErrBootstrapSecret
	}
	next := cloneState(manager.state)
	credential, stored, err := manager.newCredential(&next, registration, RoleAdmin)
	if err != nil {
		return Credential{}, err
	}
	next.BootstrapComplete = true
	next.Devices[stored.TokenDigest] = stored
	if err := manager.persist(next); err != nil {
		return Credential{}, err
	}
	manager.state = next
	return credential, nil
}

func (manager *Manager) GeneratePairingCode(ctx context.Context, token string) (PairingCode, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, err := manager.authorize(ctx, token, RoleAdmin); err != nil {
		return PairingCode{}, err
	}
	now := manager.config.Clock.Now()
	next := cloneState(manager.state)
	for range 10 {
		var raw [4]byte
		if _, err := io.ReadFull(manager.config.Random, raw[:]); err != nil {
			return PairingCode{}, fmt.Errorf("generate pairing code: %w", err)
		}
		value := fmt.Sprintf("%06d", binary.BigEndian.Uint32(raw[:])%1_000_000)
		digest := manager.codeDigest(value)
		if _, exists := next.Codes[digest]; exists {
			continue
		}
		code := PairingCode{Value: value, ExpiresAt: now.Add(manager.config.PairingTTL)}
		next.Codes[digest] = storedCode{Digest: digest, ExpiresAt: code.ExpiresAt}
		if err := manager.persist(next); err != nil {
			return PairingCode{}, err
		}
		manager.state = next
		return code, nil
	}
	return PairingCode{}, fmt.Errorf("generate pairing code: random source exhausted unique values")
}

func (manager *Manager) Pair(ctx context.Context, code string, registration Registration, requester string) (Credential, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return Credential{}, err
	}
	now := manager.config.Clock.Now()
	if manager.isLimited(requester, now) {
		return Credential{}, ErrRateLimited
	}
	digest := manager.codeDigest(code)
	storedCode, exists := manager.state.Codes[digest]
	if !exists {
		next := cloneState(manager.state)
		recordFailure(&next, requester, now, manager.config.PairingTTL)
		if err := manager.persist(next); err != nil {
			return Credential{}, fmt.Errorf("persist pairing failure: %w", err)
		}
		manager.state = next
		return Credential{}, ErrPairingCodeInvalid
	}
	if storedCode.Used {
		return Credential{}, ErrPairingCodeUsed
	}
	if !now.Before(storedCode.ExpiresAt) {
		return Credential{}, ErrPairingCodeExpired
	}
	next := cloneState(manager.state)
	credential, stored, err := manager.newCredential(&next, registration, RoleController)
	if err != nil {
		return Credential{}, err
	}
	storedCode.Used = true
	next.Codes[digest] = storedCode
	next.Devices[stored.TokenDigest] = stored
	delete(next.Failures, requester)
	if err := manager.persist(next); err != nil {
		return Credential{}, err
	}
	manager.state = next
	return credential, nil
}

func (manager *Manager) Authenticate(ctx context.Context, token string) (Device, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.authorize(ctx, token, "")
}

func (manager *Manager) Devices(ctx context.Context, token string) ([]Device, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, err := manager.authorize(ctx, token, RoleAdmin); err != nil {
		return nil, err
	}
	devices := make([]Device, 0, len(manager.state.Devices))
	for _, stored := range manager.state.Devices {
		devices = append(devices, stored.Device)
	}
	sort.Slice(devices, func(left, right int) bool { return devices[left].ID < devices[right].ID })
	return devices, nil
}

func (manager *Manager) Revoke(ctx context.Context, token string, id DeviceID) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, err := manager.authorize(ctx, token, RoleAdmin); err != nil {
		return err
	}
	for digest, stored := range manager.state.Devices {
		if stored.ID != id {
			continue
		}
		next := cloneState(manager.state)
		stored.Revoked = true
		next.Devices[digest] = stored
		if err := manager.persist(next); err != nil {
			return err
		}
		manager.state = next
		return nil
	}
	return ErrDeviceNotFound
}

func (manager *Manager) authorize(ctx context.Context, token string, role Role) (Device, error) {
	if err := context.Cause(ctx); err != nil {
		return Device{}, err
	}
	if token == "" {
		return Device{}, ErrUnauthorized
	}
	stored, exists := manager.state.Devices[tokenDigest(token)]
	if !exists {
		return Device{}, ErrUnauthorized
	}
	if stored.Revoked {
		return Device{}, ErrTokenRevoked
	}
	if role != "" && stored.Role != role {
		return Device{}, ErrForbidden
	}
	return stored.Device, nil
}

func (manager *Manager) codeDigest(value string) string {
	digest := hmac.New(sha256.New, manager.setupDigest[:])
	_, _ = digest.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(digest.Sum(nil))
}

func (manager *Manager) newCredential(state *persistedState, registration Registration, role Role) (Credential, storedDevice, error) {
	name := strings.TrimSpace(registration.Name)
	if name == "" || len(name) > 80 {
		return Credential{}, storedDevice{}, ErrInvalidRegistration
	}
	raw := make([]byte, 32)
	if _, err := io.ReadFull(manager.config.Random, raw); err != nil {
		return Credential{}, storedDevice{}, fmt.Errorf("generate device token: %w", err)
	}
	state.Sequence++
	device := Device{ID: DeviceID(fmt.Sprintf("device-%06d", state.Sequence)), Name: name, Role: role, CreatedAt: manager.config.Clock.Now()}
	token := base64.RawURLEncoding.EncodeToString(raw)
	stored := storedDevice{Device: device, TokenDigest: tokenDigest(token)}
	return Credential{Token: token, Device: device}, stored, nil
}
