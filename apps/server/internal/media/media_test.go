package media_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type authorizer struct{ err error }

type keyStore struct {
	key   playback.EncryptedMediaSigningKey
	found bool
}

func (store *keyStore) ActiveMediaSigningKey(context.Context) (playback.EncryptedMediaSigningKey, bool, error) {
	return store.key, store.found, nil
}

func (store *keyStore) InsertMediaSigningKey(_ context.Context, key playback.EncryptedMediaSigningKey) error {
	store.key, store.found = key, true
	return nil
}

func (value authorizer) AuthorizeMedia(context.Context, playback.MediaAuthorization) error {
	return value.err
}

func TestSelect_prefers_original_and_uses_L16_only_as_supported_fallback(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		format       catalog.Format
		capabilities []string
		pcm          bool
		want         media.Representation
		wantErr      error
	}{
		{name: "original protocolInfo wins", format: catalog.FormatFLAC, capabilities: []string{"http-get:*:audio/L16:*", "http-get:*:audio/flac:*"}, pcm: true, want: media.Original},
		{name: "custom media type selects original", format: catalog.FormatMP3, capabilities: []string{"audio/mpeg", "audio/L16"}, pcm: true, want: media.Original},
		{name: "L16 fallback requires provider and sink", format: catalog.FormatOpus, capabilities: []string{"http-get:*:audio/L16:*"}, pcm: true, want: media.L16},
		{name: "provider alone is insufficient", format: catalog.FormatOggVorbis, capabilities: []string{"audio/mpeg"}, pcm: true, wantErr: media.ErrNoRepresentation},
		{name: "sink alone is insufficient", format: catalog.FormatPCMWAV, capabilities: []string{"audio/L16"}, wantErr: media.ErrNoRepresentation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given / When
			got, err := media.Select(test.format, test.capabilities, test.pcm)
			// Then
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("selection = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestPersistentSigner_reuses_encrypted_server_state_key_after_restart(t *testing.T) {
	t.Parallel()
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	store := &keyStore{}
	config := media.PersistentSignerConfig{
		Store: store, WrappingKey: []byte(strings.Repeat("w", 32)), WrappingKeyID: "tls-identity",
		Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64)), Clock: fixedClock{now: now}, TTL: 10 * time.Minute,
	}
	first, err := media.LoadOrCreateSigner(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	token, err := first.Sign(media.Grant{Audience: media.AudienceK17Capability, RendererID: "renderer", ZoneID: "zone", PlayID: "play", TrackID: "track", Representation: media.Original, FileSize: 1, ModifiedNS: 1})
	if err != nil {
		t.Fatal(err)
	}

	// When
	config.Random = bytes.NewReader(nil)
	restarted, err := media.LoadOrCreateSigner(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := restarted.Verify(token, media.AudienceK17Capability, "renderer")

	// Then
	if err != nil || claims.TrackID != "track" || string(store.key.Ciphertext) == strings.Repeat("\a", 32) {
		t.Fatalf("restart verification = %+v, %v", claims, err)
	}
}

func TestSigner_rejects_expiry_tampering_and_wrong_identity(t *testing.T) {
	t.Parallel()
	// Given
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "key-1", Key: []byte(strings.Repeat("k", 32)), Clock: fixedClock{now: now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	grant := media.Grant{Audience: media.AudienceK17Capability, RendererID: "renderer-1", ZoneID: "zone-1", PlayID: "play-1", TrackID: "track-1", Representation: media.Original, FileSize: 10, ModifiedNS: 42}
	token, err := signer.Sign(grant)
	if err != nil {
		t.Fatal(err)
	}

	// When / Then
	claims, err := signer.Verify(token, media.AudienceK17Capability, "renderer-1")
	if err != nil || claims.PlayID != "play-1" {
		t.Fatalf("verify = %+v, %v", claims, err)
	}
	if _, err := signer.Verify(token, media.AudienceK17Capability, "renderer-2"); !errors.Is(err, media.ErrWrongRenderer) {
		t.Fatalf("wrong renderer error = %v", err)
	}
	parts := strings.Split(token, ".")
	parts[1] = strings.Repeat("A", len(parts[1]))
	if _, err := signer.Verify(strings.Join(parts, "."), media.AudienceK17Capability, "renderer-1"); !errors.Is(err, media.ErrInvalidCapability) {
		t.Fatalf("tamper error = %v", err)
	}
	expired, _ := media.NewSigner(media.SignerConfig{KeyID: "key-1", Key: []byte(strings.Repeat("k", 32)), Clock: fixedClock{now: now.Add(10 * time.Minute)}, TTL: 10 * time.Minute})
	if _, err := expired.Verify(token, media.AudienceK17Capability, "renderer-1"); !errors.Is(err, media.ErrExpiredCapability) {
		t.Fatalf("expiry error = %v", err)
	}
}

func TestHandler_serves_original_GET_HEAD_ranges_and_rejects_adversarial_paths(t *testing.T) {
	t.Parallel()
	// Given
	root := t.TempDir()
	content := []byte("0123456789")
	path := filepath.Join(root, "fixture.flac")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	signer, err := media.NewSigner(media.SignerConfig{KeyID: "key-1", Key: []byte(strings.Repeat("s", 32)), Clock: fixedClock{now: now}, TTL: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	track := catalog.Track{RootID: "root-1", TrackID: "track-1", RelativePath: "fixture.flac", Format: catalog.FormatFLAC, Fingerprint: strings.Repeat("a", 64), FileVersion: catalog.FileVersion{Size: info.Size(), Modified: info.ModTime()}, Available: true}
	service, err := media.NewService(media.ServiceConfig{Signer: signer, Authorizer: authorizer{}, Snapshot: func(context.Context) catalog.Snapshot {
		return catalog.Snapshot{Tracks: map[catalog.TrackID]catalog.Track{"track-1": track}}
	}, Roots: func(context.Context) map[catalog.RootID]string { return map[catalog.RootID]string{"root-1": root} }})
	if err != nil {
		t.Fatal(err)
	}
	grant := media.Grant{Audience: media.AudienceK17Capability, RendererID: "renderer-1", ZoneID: "zone-1", PlayID: "play-1", TrackID: "track-1", Representation: media.Original, FileSize: info.Size(), ModifiedNS: info.ModTime().UnixNano()}
	token, err := signer.Sign(grant)
	if err != nil {
		t.Fatal(err)
	}
	handler := service.K17Handler()

	tests := []struct {
		name, method, rangeHeader  string
		status                     int
		body, length, contentRange string
	}{
		{name: "GET", method: http.MethodGet, status: http.StatusOK, body: "0123456789", length: "10"},
		{name: "HEAD", method: http.MethodHead, status: http.StatusOK, length: "10"},
		{name: "single byte", method: http.MethodGet, rangeHeader: "bytes=0-0", status: http.StatusPartialContent, body: "0", length: "1", contentRange: "bytes 0-0/10"},
		{name: "suffix", method: http.MethodGet, rangeHeader: "bytes=-2", status: http.StatusPartialContent, body: "89", length: "2", contentRange: "bytes 8-9/10"},
		{name: "unsatisfiable", method: http.MethodGet, rangeHeader: "bytes=10-", status: http.StatusRequestedRangeNotSatisfiable, contentRange: "bytes */10"},
		{name: "multiple rejected", method: http.MethodGet, rangeHeader: "bytes=0-0,2-2", status: http.StatusRequestedRangeNotSatisfiable, contentRange: "bytes */10"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "/media/v1/"+token, nil)
			request.SetPathValue("token", token)
			request.Header.Set("Range", test.rangeHeader)
			recorder := httptest.NewRecorder()
			// When
			handler.ServeHTTP(recorder, request)
			// Then
			if recorder.Code != test.status || recorder.Body.String() != test.body {
				t.Fatalf("response = %d %q", recorder.Code, recorder.Body.String())
			}
			if test.length != "" && recorder.Header().Get("Content-Length") != test.length {
				t.Fatalf("length = %q", recorder.Header().Get("Content-Length"))
			}
			if recorder.Header().Get("Content-Range") != test.contentRange {
				t.Fatalf("content-range = %q", recorder.Header().Get("Content-Range"))
			}
			if test.status < 400 && (recorder.Header().Get("Content-Type") != "audio/flac" || recorder.Header().Get("ETag") == "" || recorder.Header().Get("Accept-Ranges") != "bytes") {
				t.Fatalf("headers = %#v", recorder.Header())
			}
		})
	}

	// Traversal is rejected before any host file can be opened.
	track.RelativePath = "../secret.flac"
	traversalRequest := httptest.NewRequest(http.MethodGet, "/media/v1/"+token, nil)
	traversalRequest.SetPathValue("token", token)
	traversalRecorder := httptest.NewRecorder()
	handler.ServeHTTP(traversalRecorder, traversalRequest)
	if traversalRecorder.Code != http.StatusForbidden || strings.Contains(traversalRecorder.Body.String(), root) {
		t.Fatalf("traversal response = %d %q", traversalRecorder.Code, traversalRecorder.Body.String())
	}
	track.RelativePath = "fixture.flac"

	// A transformed representation never claims byte-range support.
	l16Token, err := signer.Sign(media.Grant{Audience: media.AudienceK17Capability, RendererID: "renderer-1", ZoneID: "zone-1", PlayID: "play-1", TrackID: "track-1", Representation: media.L16, FileSize: info.Size(), ModifiedNS: info.ModTime().UnixNano()})
	if err != nil {
		t.Fatal(err)
	}
	transformedRange := httptest.NewRequest(http.MethodGet, "/media/v1/"+l16Token, nil)
	transformedRange.SetPathValue("token", l16Token)
	transformedRange.Header.Set("Range", "bytes=0-0")
	transformedRecorder := httptest.NewRecorder()
	handler.ServeHTTP(transformedRecorder, transformedRange)
	if transformedRecorder.Code != http.StatusRequestedRangeNotSatisfiable || transformedRecorder.Header().Get("Accept-Ranges") != "" {
		t.Fatalf("transformed range = %d headers=%#v", transformedRecorder.Code, transformedRecorder.Header())
	}

	// Changed content cannot be served under the signed file identity.
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	staleRequest := httptest.NewRequest(http.MethodGet, "/media/v1/"+token, nil)
	staleRequest.SetPathValue("token", token)
	staleRecorder := httptest.NewRecorder()
	handler.ServeHTTP(staleRecorder, staleRequest)
	if staleRecorder.Code != http.StatusConflict || strings.Contains(staleRecorder.Body.String(), "changed") {
		t.Fatalf("stale response = %d %q", staleRecorder.Code, staleRecorder.Body.String())
	}

	// When the catalog path is replaced with a symlink, authorization must fail without target bytes.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/media/v1/"+token, nil)
	request.SetPathValue("token", token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden || strings.Contains(recorder.Body.String(), "0123456789") || strings.Contains(recorder.Body.String(), outside) {
		t.Fatalf("symlink response = %d %q", recorder.Code, recorder.Body.String())
	}
}
