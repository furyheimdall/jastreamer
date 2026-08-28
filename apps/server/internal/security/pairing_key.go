package security

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

var ErrPairingKeyInvalid = errors.New("security: invalid persistent pairing HMAC key")

func (manager *Manager) loadOrCreatePairingKey() (bool, error) {
	if manager.state.PairingKey != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(manager.state.PairingKey)
		if err != nil {
			return false, fmt.Errorf("decode persistent pairing HMAC key: %w", ErrPairingKeyInvalid)
		}
		if len(decoded) != len(manager.pairingKey) {
			return false, fmt.Errorf("persistent pairing HMAC key length: %w", ErrPairingKeyInvalid)
		}
		copy(manager.pairingKey[:], decoded)
		return false, nil
	}
	if _, err := io.ReadFull(manager.config.Random, manager.pairingKey[:]); err != nil {
		return false, fmt.Errorf("generate pairing HMAC key: %w", err)
	}
	manager.state.PairingKey = base64.RawURLEncoding.EncodeToString(manager.pairingKey[:])
	return true, nil
}
