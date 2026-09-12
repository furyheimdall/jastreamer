package discovery_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/discovery"
)

func TestIdentitySurvivesRestartAndChangesWithDataReset(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "server.sqlite")

	firstDB, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := discovery.New(ctx, firstDB, "Living room", "test")
	if err != nil {
		t.Fatal(err)
	}
	if err = firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	reopenedDB, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := discovery.New(ctx, reopenedDB, "Living room", "test")
	if err != nil {
		t.Fatal(err)
	}
	if first.Metadata().ID != second.Metadata().ID {
		t.Fatalf("identity changed across restart: %q != %q", first.Metadata().ID, second.Metadata().ID)
	}
	if err = reopenedDB.Close(); err != nil {
		t.Fatal(err)
	}

	resetDB, err := database.Open(filepath.Join(t.TempDir(), "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resetDB.Close() })
	reset, err := discovery.New(ctx, resetDB, "Living room", "test")
	if err != nil {
		t.Fatal(err)
	}
	if first.Metadata().ID == reset.Metadata().ID {
		t.Fatal("fresh data directory reused the previous server identity")
	}
}
