package media

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type KeyStore interface {
	ActiveMediaSigningKey(context.Context) (playback.EncryptedMediaSigningKey, bool, error)
	InsertMediaSigningKey(context.Context, playback.EncryptedMediaSigningKey) error
}

type PersistentSignerConfig struct {
	Store         KeyStore
	WrappingKey   []byte
	WrappingKeyID string
	Random        io.Reader
	Clock         Clock
	TTL           time.Duration
}

func LoadOrCreateSigner(ctx context.Context, config PersistentSignerConfig) (*Signer, error) {
	if config.Store == nil || len(config.WrappingKey) != sha256.Size || config.WrappingKeyID == "" || config.Clock == nil {
		return nil, ErrInvalidConfig
	}
	if config.Random == nil {
		config.Random = rand.Reader
	}
	stored, found, err := config.Store.ActiveMediaSigningKey(ctx)
	if err != nil {
		return nil, fmt.Errorf("load media signing key: %w", err)
	}
	if !found {
		stored, err = createStoredKey(config)
		if err != nil {
			return nil, err
		}
		if err := config.Store.InsertMediaSigningKey(ctx, stored); err != nil {
			if !errors.Is(err, playback.ErrMediaSigningKeyConflict) {
				return nil, fmt.Errorf("persist media signing key: %w", err)
			}
			stored, found, err = config.Store.ActiveMediaSigningKey(ctx)
			if err != nil || !found {
				return nil, fmt.Errorf("reload media signing key: %w", err)
			}
		}
	}
	key, err := unwrapKey(config.WrappingKey, config.WrappingKeyID, stored)
	if err != nil {
		return nil, err
	}
	return NewSigner(SignerConfig{KeyID: string(stored.ID), Key: key, Clock: config.Clock, TTL: config.TTL})
}

func createStoredKey(config PersistentSignerConfig) (playback.EncryptedMediaSigningKey, error) {
	key := make([]byte, sha256.Size)
	if _, err := io.ReadFull(config.Random, key); err != nil {
		return playback.EncryptedMediaSigningKey{}, fmt.Errorf("generate media signing key: %w", err)
	}
	digest := sha256.Sum256(key)
	id := hex.EncodeToString(digest[:16])
	block, err := aes.NewCipher(config.WrappingKey)
	if err != nil {
		return playback.EncryptedMediaSigningKey{}, fmt.Errorf("create media key wrapper: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return playback.EncryptedMediaSigningKey{}, fmt.Errorf("create media key envelope: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(config.Random, nonce); err != nil {
		return playback.EncryptedMediaSigningKey{}, fmt.Errorf("generate media key nonce: %w", err)
	}
	aad := []byte(id + "\x00" + config.WrappingKeyID)
	return playback.EncryptedMediaSigningKey{
		ID: playback.SigningKeyID(id), Digest: hex.EncodeToString(digest[:]),
		Ciphertext: gcm.Seal(nil, nonce, key, aad), Nonce: nonce,
		WrappingKeyID: config.WrappingKeyID, CreatedAt: config.Clock.Now().UTC(),
	}, nil
}

func unwrapKey(wrappingKey []byte, wrappingKeyID string, stored playback.EncryptedMediaSigningKey) ([]byte, error) {
	if stored.WrappingKeyID != wrappingKeyID {
		return nil, ErrInvalidConfig
	}
	block, err := aes.NewCipher(wrappingKey)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(stored.Nonce) != gcm.NonceSize() {
		return nil, ErrInvalidConfig
	}
	key, err := gcm.Open(nil, stored.Nonce, stored.Ciphertext, []byte(string(stored.ID)+"\x00"+wrappingKeyID))
	if err != nil || len(key) != sha256.Size {
		return nil, ErrInvalidConfig
	}
	digest := sha256.Sum256(key)
	storedDigest, err := hex.DecodeString(stored.Digest)
	if err != nil || subtle.ConstantTimeCompare(digest[:], storedDigest) != 1 {
		return nil, ErrInvalidConfig
	}
	return key, nil
}

type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
