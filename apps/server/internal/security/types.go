package security

import (
	"errors"
	"io"
	"time"
)

var (
	ErrBootstrapComplete   = errors.New("security: bootstrap already complete")
	ErrBootstrapSecret     = errors.New("security: invalid bootstrap secret")
	ErrUnauthorized        = errors.New("security: unauthorized")
	ErrTokenRevoked        = errors.New("security: token revoked")
	ErrForbidden           = errors.New("security: admin role required")
	ErrPairingCodeInvalid  = errors.New("security: invalid pairing code")
	ErrPairingCodeExpired  = errors.New("security: pairing code expired")
	ErrPairingCodeUsed     = errors.New("security: pairing code already used")
	ErrRateLimited         = errors.New("security: pairing attempts rate limited")
	ErrDeviceNotFound      = errors.New("security: device not found")
	ErrInvalidRegistration = errors.New("security: invalid device registration")
)

type Role string

const (
	RoleAdmin      Role = "admin"
	RoleController Role = "controller"
)

type DeviceID string

type Clock interface{ Now() time.Time }

type Config struct {
	SetupSecret string
	StatePath   string
	Clock       Clock
	Random      io.Reader
	PairingTTL  time.Duration
	MaxFailures int
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
