package settings_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/settings"
)

func TestStore_patch_rejects_symlink_escape_wildcard_origin_and_invalid_TTL(t *testing.T) {
	config := fixtureConfig(t)
	outside := t.TempDir()
	link := filepath.Join(config.Locks.AllowedCatalogBases[0], "escaped")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("create escape symlink: %v", err)
	}
	tests := []struct {
		name   string
		update settings.Update
		field  string
	}{
		{
			name: "symlink escape",
			update: settings.Update{CatalogRoots: &[]settings.CatalogRoot{{
				ID: "escape", DisplayName: "Escape", Path: link,
			}}},
			field: "catalog_roots[0].path",
		},
		{name: "wildcard origin", update: settings.Update{ControlOrigins: &[]string{"https://*.example.test"}}, field: "control_origins[0]"},
		{name: "short pairing TTL", update: settings.Update{PairingTTLSeconds: intPointer(59)}, field: "pairing_ttl_seconds"},
		{name: "public media listener", update: settings.Update{K17HTTP: &settings.K17HTTP{Enabled: true, ListenerAddress: "0.0.0.0:8080"}}, field: "k17_http.listener_address"},
		{name: "relative FFmpeg path", update: settings.Update{FFmpegPath: stringPointer("bin/ffmpeg")}, field: "ffmpeg_path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := settings.Open(config)
			if err != nil {
				t.Fatalf("open settings: %v", err)
			}

			_, err = store.Patch(context.Background(), settings.Mutation{ExpectedRevision: 0, Update: test.update})

			var validation *settings.ValidationError
			if !errors.As(err, &validation) || validation.Field != test.field {
				t.Fatalf("validation error = %#v, %v", validation, err)
			}
			if store.Snapshot().Revision != 0 {
				t.Fatalf("invalid patch mutated revision: %d", store.Snapshot().Revision)
			}
		})
	}
}

func TestStore_patch_rejects_more_than_32_roots(t *testing.T) {
	config := fixtureConfig(t)
	roots := make([]settings.CatalogRoot, 33)
	for index := range roots {
		roots[index] = settings.CatalogRoot{ID: "root", DisplayName: "Root", Path: config.Defaults.CatalogRoots[0].Path}
	}
	store, err := settings.Open(config)
	if err != nil {
		t.Fatalf("open settings: %v", err)
	}

	_, err = store.Patch(context.Background(), settings.Mutation{ExpectedRevision: 0, Update: settings.Update{CatalogRoots: &roots}})

	var validation *settings.ValidationError
	if !errors.As(err, &validation) || validation.Field != "catalog_roots" {
		t.Fatalf("validation error = %#v, %v", validation, err)
	}
}

func intPointer(value int) *int          { return &value }
func stringPointer(value string) *string { return &value }
