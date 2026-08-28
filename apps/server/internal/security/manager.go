package security

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

type credentialState string

const (
	credentialActive  credentialState = "active"
	credentialPending credentialState = "pending"
	credentialRevoked credentialState = "revoked"
)

type storedDevice struct {
	Device
	TokenDigest string          `json:"token_digest"`
	State       credentialState `json:"credential_state,omitempty"`
}

type storedCode struct {
	Digest    string    `json:"code_digest"`
	ExpiresAt time.Time `json:"expires_at"`
	Role      Role      `json:"role"`
	Used      bool      `json:"used"`
}

type failures struct {
	Count int       `json:"count"`
	Since time.Time `json:"since"`
}

type persistedState struct {
	BootstrapComplete  bool                               `json:"bootstrap_complete"`
	Sequence           uint64                             `json:"sequence"`
	PairingKey         string                             `json:"pairing_hmac_key"`
	Devices            map[string]storedDevice            `json:"devices"`
	Codes              map[string]storedCode              `json:"pairing_codes"`
	Failures           map[string]failures                `json:"pairing_failures"`
	RendererOperations map[string]storedRendererOperation `json:"renderer_operations,omitempty"`
}

type Manager struct {
	mu                  sync.Mutex
	config              Config
	setupDigest         [32]byte
	pairingKey          [32]byte
	state               persistedState
	freshPairs          map[string]struct{}
	nextRevocationID    uint64
	revocationObservers map[uint64]func(DeviceID)
}

func NewManager(config Config) (*Manager, error) {
	if config.StatePath == "" {
		return nil, fmt.Errorf("security config: state path is required")
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
	manager := &Manager{
		config: config, setupDigest: sha256.Sum256([]byte(config.SetupSecret)),
		freshPairs: map[string]struct{}{}, revocationObservers: map[uint64]func(DeviceID){},
	}
	manager.state = persistedState{Devices: map[string]storedDevice{}, Codes: map[string]storedCode{}, Failures: map[string]failures{}, RendererOperations: map[string]storedRendererOperation{}}
	data, err := os.ReadFile(config.StatePath)
	stateExists := err == nil
	if stateExists {
		if err := json.Unmarshal(data, &manager.state); err != nil {
			return nil, fmt.Errorf("decode security state: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read security state: %w", err)
	}
	if !manager.state.BootstrapComplete && config.SetupSecret == "" {
		return nil, fmt.Errorf("security config: setup secret is required before bootstrap")
	}
	manager.ensureMaps()
	generated, err := manager.loadOrCreatePairingKey()
	if err != nil {
		return nil, err
	}
	if stateExists && generated {
		if err := manager.persist(manager.state); err != nil {
			return nil, fmt.Errorf("persist pairing HMAC key: %w", err)
		}
	}
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
	return manager.GeneratePairingCodeForRole(ctx, token, RoleController)
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
	role := storedCode.Role
	if role == "" {
		role = RoleController
	}
	if role == RoleRenderer && manager.config.OperationHook != nil {
		if err := manager.config.OperationHook(OperationBeforePair); err != nil {
			return Credential{}, fmt.Errorf("before renderer pairing commit: %w", err)
		}
	}
	credential, stored, err := manager.newCredential(&next, registration, role)
	if err != nil {
		return Credential{}, err
	}
	storedCode.Used = true
	next.Codes[digest] = storedCode
	if role == RoleRenderer {
		stored.State = credentialPending
		next.RendererOperations[string(stored.ID)] = storedRendererOperation{Kind: RendererOperationPair, Device: stored.Device}
	}
	next.Devices[stored.TokenDigest] = stored
	delete(next.Failures, requester)
	if err := manager.persist(next); err != nil {
		return Credential{}, err
	}
	manager.state = next
	if role == RoleRenderer {
		manager.freshPairs[string(stored.ID)] = struct{}{}
	}
	if role == RoleRenderer && manager.config.OperationHook != nil {
		if err := manager.config.OperationHook(OperationAfterPair); err != nil {
			return Credential{}, fmt.Errorf("after renderer pairing commit: %w", err)
		}
	}
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
