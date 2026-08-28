package security

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

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
	if stored.Revoked || stored.State == credentialRevoked {
		return Device{}, ErrTokenRevoked
	}
	if stored.State == credentialPending {
		operation, exists := manager.state.RendererOperations[string(stored.ID)]
		if !exists || !operation.InventoryReady {
			return Device{}, ErrCredentialPending
		}
		next := cloneState(manager.state)
		stored.State = credentialActive
		next.Devices[stored.TokenDigest] = stored
		delete(next.RendererOperations, string(stored.ID))
		if err := manager.persist(next); err != nil {
			return Device{}, fmt.Errorf("activate delivered renderer credential: %w", err)
		}
		manager.state = next
		delete(manager.freshPairs, string(stored.ID))
	}
	if role != "" && stored.Role != role {
		return Device{}, ErrForbidden
	}
	return stored.Device, nil
}

func (manager *Manager) codeDigest(value string) string {
	digest := hmac.New(sha256.New, manager.pairingKey[:])
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
	stored := storedDevice{Device: device, TokenDigest: tokenDigest(token), State: credentialActive}
	return Credential{Token: token, Device: device}, stored, nil
}
