package media

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

const maximumTTL = 10 * time.Minute

type SignerConfig struct {
	KeyID string
	Key   []byte
	Clock Clock
	TTL   time.Duration
}

type Signer struct {
	keyID string
	key   [32]byte
	clock Clock
	ttl   time.Duration
}

func NewSigner(config SignerConfig) (*Signer, error) {
	if config.KeyID == "" || len(config.Key) != sha256.Size || config.Clock == nil || config.TTL <= 0 || config.TTL > maximumTTL {
		return nil, ErrInvalidConfig
	}
	value := &Signer{keyID: config.KeyID, clock: config.Clock, ttl: config.TTL}
	copy(value.key[:], config.Key)
	return value, nil
}

func (signer *Signer) Sign(grant Grant) (string, error) {
	if !validAudience(grant.Audience) || grant.RendererID == "" || grant.ZoneID == "" || grant.PlayID == "" || grant.TrackID == "" || grant.FileSize < 0 || grant.ModifiedNS < 0 || !validRepresentation(grant.Representation) {
		return "", ErrInvalidCapability
	}
	claims := Claims{
		KeyID: signer.keyID, Audience: grant.Audience, RendererID: grant.RendererID, ZoneID: grant.ZoneID, PlayID: grant.PlayID,
		TrackID: grant.TrackID, Representation: grant.Representation, FileSize: grant.FileSize,
		ModifiedNS: grant.ModifiedNS, ExpiresAt: signer.clock.Now().Add(signer.ttl).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode capability: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(signer.signature(encoded)), nil
}

func (signer *Signer) Verify(token string, expectedAudience Audience, expectedRenderer playback.RendererID) (Claims, error) {
	payload, encodedSignature, found := strings.Cut(token, ".")
	if !found || payload == "" || encodedSignature == "" || strings.Contains(encodedSignature, ".") {
		return Claims{}, ErrInvalidCapability
	}
	providedBytes, decodeErr := base64.RawURLEncoding.DecodeString(encodedSignature)
	var provided [sha256.Size]byte
	copy(provided[:], providedBytes)
	validLength := subtle.ConstantTimeEq(int32(len(providedBytes)), sha256.Size)
	validSignature := subtle.ConstantTimeCompare(provided[:], signer.signature(payload))
	if decodeErr != nil || validLength&validSignature != 1 {
		return Claims{}, ErrInvalidCapability
	}
	encodedClaims, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Claims{}, ErrInvalidCapability
	}
	var claims Claims
	if err := json.Unmarshal(encodedClaims, &claims); err != nil || claims.KeyID != signer.keyID || !validClaims(claims) {
		return Claims{}, ErrInvalidCapability
	}
	if !signer.clock.Now().Before(time.Unix(claims.ExpiresAt, 0)) {
		return Claims{}, ErrExpiredCapability
	}
	if claims.Audience != expectedAudience {
		return Claims{}, ErrWrongAudience
	}
	if expectedRenderer != "" && claims.RendererID != expectedRenderer {
		return Claims{}, ErrWrongRenderer
	}
	return claims, nil
}

func (signer *Signer) signature(payload string) []byte {
	mac := hmac.New(sha256.New, signer.key[:])
	_, _ = mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func validClaims(claims Claims) bool {
	return validAudience(claims.Audience) && claims.RendererID != "" && claims.ZoneID != "" && claims.PlayID != "" && claims.TrackID != "" &&
		claims.FileSize >= 0 && claims.ModifiedNS >= 0 && claims.ExpiresAt > 0 && validRepresentation(claims.Representation)
}

func validAudience(value Audience) bool {
	return value == AudienceCustomRenderer || value == AudienceK17Capability
}

func validRepresentation(value Representation) bool { return value == Original || value == L16 }
