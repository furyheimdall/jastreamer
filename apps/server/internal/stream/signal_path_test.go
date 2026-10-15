package stream

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type inspectedLibrary struct {
	*fixtureLibrary
	inspections int
	err         error
	otherPath   string
	otherTrack  bool
}

func (fixture *inspectedLibrary) OpenAudio(ctx context.Context, id string) (*os.File, library.TrackInfo, error) {
	fixture.inspections++
	if fixture.err != nil {
		return nil, library.TrackInfo{}, fixture.err
	}
	file, details, err := fixture.fixtureLibrary.OpenAudio(ctx, id)
	if err == nil && fixture.otherPath != "" {
		file.Close()
		file, err = os.Open(fixture.otherPath)
	}
	if fixture.otherTrack {
		details.Track.Path = "different-source"
	}
	return file, details, err
}

func signalRequest(service *Service, resource output.Resource, method, suffix, remote, byteRange string, writer http.ResponseWriter) {
	request := httptest.NewRequest(method, resource.URL+suffix, nil)
	request.RemoteAddr = remote
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	service.Handler().ServeHTTP(writer, request)
}

func TestSignalPathRequiresOriginalLosslessDelivery(t *testing.T) {
	for _, test := range []struct {
		name     string
		protocol string
		codec    string
		missing  string
		want     bool
	}{
		{"upnp FLAC", output.ProtocolUPnP, "FLAC", "", true},
		{"cast FLAC", output.ProtocolCast, "FLAC", "", true},
		{"upnp ALAC", output.ProtocolUPnP, "ALAC", "", true},
		{"upnp PCM", output.ProtocolUPnP, "PCM", "", true},
		{"lossy", output.ProtocolUPnP, "MP3", "", false},
		{"unknown codec", output.ProtocolUPnP, "", "", false},
		{"unverified LPCM", output.ProtocolUPnP, "LPCM (unverified)", "", false},
		{"unknown rate", output.ProtocolUPnP, "FLAC", "rate", false},
		{"unknown precision", output.ProtocolUPnP, "FLAC", "bits", false},
		{"unknown channels", output.ProtocolUPnP, "FLAC", "channels", false},
		{"browser", output.ProtocolBrowser, "FLAC", "", false},
		{"airplay", output.ProtocolAirPlay, "FLAC", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, fixture, device, track := newOriginalFixture(t, []byte("original-media"))
			device.Protocol = test.protocol
			fixture.audio.Codec = test.codec
			switch test.missing {
			case "rate":
				fixture.audio.SampleRate = nil
			case "bits":
				fixture.audio.BitsPerSample = nil
			case "channels":
				fixture.audio.Channels = nil
			}
			inspected := &inspectedLibrary{fixtureLibrary: fixture}
			service.library = inspected
			service.browserAuthorized = func(*http.Request) bool { return true }
			resource, err := service.Prepare(t.Context(), device, track, "play")
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := service.SignalPath(device.ID, "play"); ok {
				t.Fatal("Prepare alone established delivery")
			}
			response := httptest.NewRecorder()
			signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", response)
			if response.Code != http.StatusOK || response.Body.String() != "original-media" {
				t.Fatalf("original delivery failed: %d %q", response.Code, response.Body.String())
			}
			path, ok := service.SignalPath(device.ID, "play")
			if ok != test.want {
				t.Fatalf("signal path = %+v, known=%v", path, ok)
			}
			if ok && (path.Scope != "server" || path.Mode != "original" || path.RendererID != device.ID || path.TrackID != track.ID || path.SampleRate != 44100 || path.ValidBits != 16 || path.ContainerBits != 0 || path.Channels != 2) {
				t.Fatalf("wrong delivery evidence: %+v", path)
			}
			calls := inspected.inspections
			service.SignalPath(device.ID, "play")
			service.SignalPath(device.ID, "play")
			if inspected.inspections != calls || ((test.protocol == output.ProtocolUPnP || test.protocol == output.ProtocolCast) && calls != 1) {
				t.Fatalf("metadata inspection count = %d, originally %d", inspected.inspections, calls)
			}
		})
	}
}

func TestSignalPathRejectsUnknownOrMismatchedInspectedSourceWithoutBreakingUPnP(t *testing.T) {
	for _, kind := range []string{"inspection error", "track mismatch", "file mismatch"} {
		t.Run(kind, func(t *testing.T) {
			service, fixture, device, track := newOriginalFixture(t, []byte("original"))
			device.Protocol = output.ProtocolUPnP
			inspected := &inspectedLibrary{fixtureLibrary: fixture}
			switch kind {
			case "inspection error":
				inspected.err = errors.New("metadata unavailable")
			case "track mismatch":
				inspected.otherTrack = true
			case "file mismatch":
				inspected.otherPath = filepath.Join(t.TempDir(), "other.flac")
				if err := os.WriteFile(inspected.otherPath, []byte("original"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			service.library = inspected
			resource, err := service.Prepare(t.Context(), device, track, "play")
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", response)
			if response.Code != http.StatusOK || response.Body.String() != "original" {
				t.Fatalf("unknown metadata broke playable UPnP: %d", response.Code)
			}
			if _, ok := service.SignalPath(device.ID, "play"); ok {
				t.Fatal("unmatched inspection established evidence")
			}
		})
	}
}

type deliveryWriter struct {
	*httptest.ResponseRecorder
	writes int
	before func(int)
	failAt int
}

func (writer *deliveryWriter) Write(data []byte) (int, error) {
	writer.writes++
	if writer.before != nil {
		writer.before(writer.writes)
	}
	if writer.writes == writer.failAt {
		return 0, errors.New("delivery failed")
	}
	return writer.ResponseRecorder.Write(data)
}

func TestSignalPathTracksLiveBodyDeliveryAndGrantLifetime(t *testing.T) {
	service, fixture, device, track := newOriginalFixture(t, bytes.Repeat([]byte("x"), 128*1024))
	device.Protocol = output.ProtocolUPnP
	artwork := filepath.Join(t.TempDir(), "art.jpg")
	if err := os.WriteFile(artwork, []byte("artwork"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.setArtwork("art", artwork, "image/jpeg")
	resource, err := service.Prepare(t.Context(), device, track, "play")
	if err != nil {
		t.Fatal(err)
	}
	assertKnown := func(want bool) {
		t.Helper()
		if _, ok := service.SignalPath(device.ID, "play"); ok != want {
			t.Fatalf("delivery known=%v, want %v", ok, want)
		}
	}
	for _, request := range []struct{ method, suffix, remote, byteRange string }{
		{http.MethodHead, "", "192.0.2.1:5000", ""},
		{http.MethodGet, "/artwork", "192.0.2.1:5000", ""},
		{http.MethodGet, "", "192.0.2.2:5000", ""},
		{http.MethodGet, "", "192.0.2.1:5000", "bytes=999999-"},
	} {
		signalRequest(service, resource, request.method, request.suffix, request.remote, request.byteRange, httptest.NewRecorder())
		assertKnown(false)
	}
	failed := &deliveryWriter{ResponseRecorder: httptest.NewRecorder(), failAt: 1}
	signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", failed)
	assertKnown(false)
	live := &deliveryWriter{ResponseRecorder: httptest.NewRecorder(), before: func(write int) { assertKnown(write > 1) }}
	signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", live)
	assertKnown(true)
	if live.writes < 2 {
		t.Fatal("live delivery boundary was not exercised")
	}
	if _, ok := service.SignalPath("other-renderer", "play"); ok {
		t.Fatal("evidence crossed renderer identity")
	}
	if _, ok := service.SignalPath(device.ID, "other-play"); ok {
		t.Fatal("evidence crossed play identity")
	}
	failed = &deliveryWriter{ResponseRecorder: httptest.NewRecorder(), failAt: 2}
	signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", failed)
	assertKnown(false)
	signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "bytes=0-10", httptest.NewRecorder())
	assertKnown(true)
	replacement, err := service.Prepare(t.Context(), device, track, "play")
	if err != nil {
		t.Fatal(err)
	}
	assertKnown(false)
	signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", httptest.NewRecorder())
	assertKnown(false)
	signalRequest(service, replacement, http.MethodGet, "", "192.0.2.1:5000", "", httptest.NewRecorder())
	assertKnown(true)
	service.Revoke("play")
	assertKnown(false)
	replacement, err = service.Prepare(t.Context(), device, track, "play")
	if err != nil {
		t.Fatal(err)
	}
	signalRequest(service, replacement, http.MethodGet, "", "192.0.2.1:5000", "", httptest.NewRecorder())
	assertKnown(true)
	fixture.mu.Lock()
	fixture.track.ModifiedAt = "changed-catalog-identity"
	fixture.mu.Unlock()
	signalRequest(service, replacement, http.MethodGet, "", "192.0.2.1:5000", "", httptest.NewRecorder())
	assertKnown(false)
	service.Revoke("play")
	assertKnown(false)
}

func TestSignalPathRejectsReplacedFileAndRevocationDuringWrite(t *testing.T) {
	for _, boundary := range []string{"replaced file", "revoked during write"} {
		t.Run(boundary, func(t *testing.T) {
			service, fixture, device, track := newOriginalFixture(t, []byte("original"))
			device.Protocol = output.ProtocolUPnP
			resource, err := service.Prepare(t.Context(), device, track, "play")
			if err != nil {
				t.Fatal(err)
			}
			writer := &deliveryWriter{ResponseRecorder: httptest.NewRecorder()}
			if boundary == "replaced file" {
				signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", httptest.NewRecorder())
				if _, known := service.SignalPath(device.ID, "play"); !known {
					t.Fatal("missing original delivery evidence")
				}
				replaced := filepath.Join(t.TempDir(), "replacement.flac")
				if err := os.WriteFile(replaced, []byte("replaced"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replaced, fixture.path); err != nil {
					t.Fatal(err)
				}
			} else {
				writer.before = func(int) { service.Revoke("play") }
			}
			signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", writer)
			if _, known := service.SignalPath(device.ID, "play"); known {
				t.Fatal("stale delivery survived source/grant invalidation")
			}
		})
	}
}

func TestSignalPathNeverAttestsTransformedDelivery(t *testing.T) {
	service, _, device, track := newOriginalFixture(t, []byte("source"))
	device.Protocol = output.ProtocolUPnP
	device.ProtocolInfo = []string{"http-get:*:audio/L16:DLNA.ORG_PN=LPCM"}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service.ffmpeg = newTranscoder(executable)
	t.Setenv(ffmpegHelperEnvironment, "copy")
	resource, err := service.Prepare(t.Context(), device, track, "play")
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	signalRequest(service, resource, http.MethodGet, "", "192.0.2.1:5000", "", response)
	if resource.Transformed == nil || !*resource.Transformed || response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("transformed path not exercised: %+v status=%d", resource, response.Code)
	}
	if _, ok := service.SignalPath(device.ID, "play"); ok {
		t.Fatal("transformed delivery established bit-preservation evidence")
	}
}
