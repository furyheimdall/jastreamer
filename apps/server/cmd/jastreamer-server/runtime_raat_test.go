package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/player"
	"github.com/jastreamer/jastreamer-server/internal/raat"
	_ "modernc.org/sqlite"
)

// TestRAATCallbacksCannotFireBeforePlaybackAssignment checks the startup
// window in runRuntime. OnControl and State are installed while the player
// pointer is still nil: raat.New runs before player.New, because the player
// is built from the output manager that includes the RAAT backend.
//
// Those callbacks are not reachable in that window. raat.New only stores
// them. output.NewManager does not start backends. Session workers are the
// only callers (controlLoop for OnControl, statusLoop for State), and
// startWorkers runs from Manager.start at the end of Play. Play is issued
// by the player after playback.Run, which runRuntime launches only after
// player.New has returned and the binding has been stored. The nil guard is
// preventive defensive code for that unreachable window, not a reproduced bug.
func TestRAATCallbacksCannotFireBeforePlaybackAssignment(t *testing.T) {
	db := openRuntimeTestDB(t)
	catalog := openRuntimeTestLibrary(t, db)
	binding := &raatPlaybackBinding{}
	var controls, states atomic.Int32
	manager, err := raat.New(raat.Config{
		DiscoveryInterval: time.Second,
		OnControl: func(ctx context.Context, intent output.RemoteControl) error {
			controls.Add(1)
			return binding.onControl(ctx, intent)
		},
		State: func(ctx context.Context) (raat.TransportState, error) {
			states.Add(1)
			return binding.transportState(ctx)
		},
	}, catalog)
	if controls.Load() != 0 || states.Load() != 0 {
		t.Fatalf("raat.New invoked playback callbacks before assignment: control=%d state=%d err=%v", controls.Load(), states.Load(), err)
	}
	if err == nil {
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if refreshErr := manager.Refresh(cancelled); refreshErr == nil {
			t.Fatal("cancelled RAAT refresh succeeded")
		}
		if controls.Load() != 0 || states.Load() != 0 {
			t.Fatalf("RAAT refresh invoked playback callbacks: control=%d state=%d", controls.Load(), states.Load())
		}
	}

	if err := binding.onControl(context.Background(), output.RemoteControl{}); !errors.Is(err, errRAATPlaybackNotReady) {
		t.Fatalf("early OnControl = %v, want %v", err, errRAATPlaybackNotReady)
	}
	if _, err := binding.transportState(context.Background()); !errors.Is(err, errRAATPlaybackNotReady) {
		t.Fatalf("early State = %v, want %v", err, errRAATPlaybackNotReady)
	}
}

func TestRAATPlaybackBindingUsesAssignedService(t *testing.T) {
	db := openRuntimeTestDB(t)
	catalog := openRuntimeTestLibrary(t, db)
	service, err := player.New(context.Background(), db, catalog, output.NewManager(), time.Second, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := &raatPlaybackBinding{}
	binding.service.Store(service)

	got, err := binding.transportState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != player.StateStopped || got.RepeatMode != "off" || got.Shuffle || got.RendererID != "" || got.CanPlay || got.CanPause || got.CanSeek {
		t.Fatalf("transport state = %+v", got)
	}
	err = binding.onControl(context.Background(), output.RemoteControl{})
	var rejected *fault.Error
	if !errors.As(err, &rejected) || rejected.Code != "STALE_OUTPUT_CONTROL" {
		t.Fatalf("OnControl did not reach the player: %v", err)
	}
}

func openRuntimeTestDB(t *testing.T) *sql.DB {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func openRuntimeTestLibrary(t *testing.T, db *sql.DB) *library.Service {
	t.Helper()
	catalog, err := library.New(context.Background(), db, nil, filepath.Join(t.TempDir(), "artwork"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
