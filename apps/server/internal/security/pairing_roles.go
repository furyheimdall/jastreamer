package security

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
)

func (manager *Manager) GeneratePairingCodeForRole(
	ctx context.Context,
	token string,
	role Role,
) (PairingCode, error) {
	if err := validPairingRole(role); err != nil {
		return PairingCode{}, err
	}
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
		next.Codes[digest] = storedCode{Digest: digest, ExpiresAt: code.ExpiresAt, Role: role}
		if err := manager.persist(next); err != nil {
			return PairingCode{}, err
		}
		manager.state = next
		return code, nil
	}
	return PairingCode{}, fmt.Errorf("generate pairing code: random source exhausted unique values")
}

func validPairingRole(role Role) error {
	switch role {
	case RoleAdmin, RoleController, RoleRenderer:
		return nil
	default:
		return ErrInvalidRole
	}
}
