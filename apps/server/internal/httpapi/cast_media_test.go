package httpapi

import (
	"context"
	"encoding/binary"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/config"
	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/stream"
)

func TestCastMediaCrossOriginDoesNotOpenAdministrativeAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	directory := t.TempDir()
	music := filepath.Join(directory, "music")
	if err := os.Mkdir(music, 0700); err != nil {
		t.Fatal(err)
	}
	wave := make([]byte, 44+176400)
	copy(wave, "RIFF")
	binary.LittleEndian.PutUint32(wave[4:], uint32(len(wave)-8))
	copy(wave[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wave[16:], 16)
	binary.LittleEndian.PutUint16(wave[20:], 1)
	binary.LittleEndian.PutUint16(wave[22:], 2)
	binary.LittleEndian.PutUint32(wave[24:], 44100)
	binary.LittleEndian.PutUint32(wave[28:], 176400)
	binary.LittleEndian.PutUint16(wave[32:], 4)
	binary.LittleEndian.PutUint16(wave[34:], 16)
	copy(wave[36:], "data")
	binary.LittleEndian.PutUint32(wave[40:], uint32(len(wave)-44))
	if err := os.WriteFile(filepath.Join(music, "track.wav"), wave, 0600); err != nil {
		t.Fatal(err)
	}
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	catalog, err := library.New(ctx, db, []library.Root{{ID: "music", Name: "Music", Path: music}}, filepath.Join(directory, "artwork"), nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := catalog.StartScan(ctx)
	if err != nil {
		t.Fatal(err)
	}
scan:
	for {
		jobs, err := catalog.Scans(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, current := range jobs {
			if current.ID == job.ID && current.Status == "complete" {
				break scan
			}
			if current.ID == job.ID && (current.Status == "failed" || current.Status == "cancelled") {
				t.Fatalf("fixture scan failed: %#v", current)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	var trackID string
	if err := db.QueryRowContext(ctx, "SELECT id FROM library_tracks").Scan(&trackID); err != nil {
		t.Fatal(err)
	}
	track, err := catalog.Track(ctx, trackID)
	if err != nil {
		t.Fatal(err)
	}
	media, err := stream.New(catalog, stream.Config{BaseURL: func(output.Device) (string, error) { return "http://127.0.0.1:18080", nil }})
	if err != nil {
		t.Fatal(err)
	}
	resource, err := media.Prepare(ctx, output.Device{Protocol: output.ProtocolCast, Address: "127.0.0.1:8009"}, track, "cast-origin")
	if err != nil {
		t.Fatal(err)
	}
	defer media.Revoke("cast-origin")
	handler := New(Options{Config: config.Default(), Stream: media, UI: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}})
	origin := "https://www.gstatic.com"
	for _, test := range []struct {
		name, method, url, peer, host string
		status                        int
	}{
		{"receiver range", "GET", resource.URL, "127.0.0.1:44000", "", 206},
		{"receiver preflight", "OPTIONS", resource.URL, "127.0.0.1:44000", "", 204},
		{"administrative read", "GET", "http://127.0.0.1:18080/api/v1/config", "127.0.0.1:44000", "", 403},
		{"administrative preflight", "OPTIONS", "http://127.0.0.1:18080/api/v1/config", "127.0.0.1:44000", "", 403},
		{"untrusted host", "GET", resource.URL, "127.0.0.1:44000", "attacker.invalid", 403},
		{"public network peer", "GET", resource.URL, "8.8.8.8:44000", "", 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.url, nil)
			request.RemoteAddr = test.peer
			if test.host != "" {
				request.Host = test.host
			}
			request.Header.Set("Origin", origin)
			request.Header.Set("Sec-Fetch-Site", "cross-site")
			request.Header.Set("Range", "bytes=0-3")
			if test.method == "OPTIONS" {
				request.Header.Set("Access-Control-Request-Method", "GET")
				request.Header.Set("Access-Control-Request-Headers", "Range")
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status %d, want %d: %s", response.Code, test.status, response.Body.String())
			}
			if test.status < 300 && response.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Fatal("authorized receiver cannot read the media response")
			}
			if test.status >= 400 && response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("rejected request received cross-origin authorization")
			}
			if test.status == 206 && response.Body.String() != "RIFF" {
				t.Fatal("receiver did not receive the actual selected file range")
			}
		})
	}
}
