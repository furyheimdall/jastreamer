package security

import (
	"errors"
	"io"
	"time"
)

var (
	ErrBootstrapComplete        = errors.New("security: bootstrap already complete")
	ErrBootstrapSecret          = errors.New("security: invalid bootstrap secret")
	ErrUnauthorized             = errors.New("security: unauthorized")
	ErrTokenRevoked             = errors.New("security: token revoked")
	ErrForbidden                = errors.New("security: admin role required")
	ErrPairingCodeInvalid       = errors.New("security: invalid pairing code")
	ErrPairingCodeExpired       = errors.New("security: pairing code expired")
	ErrPairingCodeUsed          = errors.New("security: pairing code already used")
	ErrRateLimited              = errors.New("security: pairing attempts rate limited")
	ErrDeviceNotFound           = errors.New("security: device not found")
	ErrInvalidRegistration      = errors.New("security: invalid device registration")
	ErrInvalidRole              = errors.New("security: invalid pairing role")
	ErrLastAdmin                = errors.New("security: cannot revoke the last active administrator")
	ErrCredentialPending        = errors.New("security: renderer credential is pending delivery")
	ErrRendererStoreUnavailable = errors.New("security: renderer inventory is unavailable")
	ErrRendererOperationPending = errors.New("security: renderer operation is pending recovery")
)

type Role string

const (
	RoleAdmin      Role = "admin"
	RoleController Role = "controller"
	RoleRenderer   Role = "renderer"
)

type DeviceID string

type Clock interface{ Now() time.Time }

type OperationStage string

const (
	OperationBeforePair   OperationStage = "before_pair"
	OperationAfterPair    OperationStage = "after_pair"
	OperationBeforeRevoke OperationStage = "before_revoke"
	OperationAfterRevoke  OperationStage = "after_revoke"
)

type Config struct {
	SetupSecret   string
	StatePath     string
	Clock         Clock
	Random        io.Reader
	PairingTTL    time.Duration
	MaxFailures   int
	OperationHook func(OperationStage) error
}

type Registration struct {
	Name string `json:"name"`
}

type Device struct {
	ID        DeviceID  `json:"id"`
	Name      string    `json:"name"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
	Revoked   bool      `json:"revoked"`
}

type Credential struct {
	Token  string `json:"token"`
	Device Device `json:"device"`
}

type PairingCode struct {
	Value     string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }
