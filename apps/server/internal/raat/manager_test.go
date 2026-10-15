package raat

import (
	"context"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func testManager() *Manager {
	return &Manager{devices: make(map[string]discoveredDevice), prepared: make(map[string]preparedMedia), selected: make(map[string]preparedMedia), sessions: make(map[string]*session)}
}

func TestPrivateResourcesRejectTamperingCrossDeviceAndRevocation(t *testing.T) {
	manager := testManager()
	resource := output.Resource{URL: "raat://unguessable", TrackID: "catalog-track", PlayID: "play-one", Mime: "audio/flac", Title: "title", Size: 1234, DurationMS: 42000, Seekable: true}
	manager.prepared[resource.URL] = preparedMedia{deviceID: "raat:one", resource: resource}
	for _, mutate := range []func(*output.Resource){func(r *output.Resource) { r.TrackID = "other-track" }, func(r *output.Resource) { r.PlayID = "other-play" }, func(r *output.Resource) { r.URL = "file:///private" }, func(r *output.Resource) { r.Title = "forged" }, func(r *output.Resource) { r.Size++ }, func(r *output.Resource) { r.Seekable = false }} {
		forged := resource
		mutate(&forged)
		if err := manager.SetURI(context.Background(), "raat:one", forged); err == nil {
			t.Fatalf("accepted forged resource: %+v", forged)
		}
	}
	if err := manager.SetURI(context.Background(), "raat:two", resource); err == nil {
		t.Fatal("accepted cross-device resource")
	}
	if err := manager.SetURI(context.Background(), "raat:one", resource); err != nil {
		t.Fatal(err)
	}
	manager.Revoke(resource.PlayID)
	if _, ok := manager.selected["raat:one"]; ok {
		t.Fatal("revocation retained selectable media")
	}
	if err := manager.SetURI(context.Background(), "raat:one", resource); err == nil {
		t.Fatal("revoked resource resurrected")
	}
	if err := manager.Play(context.Background(), "raat:one"); err == nil {
		t.Fatal("revoked media was playable")
	}
}

func TestDiscoverySnapshotsExpireWithoutContactOrAutoplay(t *testing.T) {
	manager := testManager()
	manager.config.DiscoveryInterval = 2 * time.Minute
	manager.devices["raat:b"] = discoveredDevice{device: output.Device{ID: "raat:b", Online: true, LastSeen: time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano), Protocol: output.ProtocolRAAT}}
	manager.devices["raat:a"] = discoveredDevice{device: output.Device{ID: "raat:a", Online: true, LastSeen: time.Now().Add(-7 * time.Minute).UTC().Format(time.RFC3339Nano), Protocol: output.ProtocolRAAT}}
	devices := manager.Devices()
	if len(devices) != 2 || devices[0].ID != "raat:a" || devices[0].Online || !devices[1].Online {
		t.Fatalf("incorrect TTL snapshots: %+v", devices)
	}
	devices[1].Name = "changed snapshot"
	if device, _ := manager.Device("raat:b"); device.Name != "" {
		t.Fatal("snapshot mutated manager record")
	}
	if len(manager.sessions) != 0 {
		t.Fatal("enumeration established control session")
	}
	observed, err := manager.Observe(context.Background(), "raat:b")
	if err != nil || observed.CompletionKnown || observed.Completed || observed.HasURI {
		t.Fatalf("idle device invented playback evidence: %+v, %v", observed, err)
	}
}

func TestLocalGenerationAndOldSnapshotCannotRewriteCompletion(t *testing.T) {
	manager := testManager()
	s := &session{manager: manager, resource: output.Resource{URL: "raat://one", PlayID: "one"}, format: audioFormat{"pcm", 48000, 16, 2}, baseMS: 3000, snapshot: receiverSnapshot{state: 4, position: 48000, written: 48000, samplesOut: 48000, capacity: 96000, epoch: 4}, known: true}
	s.applySnapshot(receiverSnapshot{state: 2, position: 47000, written: 48000, samplesOut: 47000, capacity: 96000, epoch: 4})
	s.applySnapshot(receiverSnapshot{state: 5, epoch: 3})
	observed := s.observation()
	if !observed.Completed || observed.PositionMS != 4000 || observed.PlayID != "one" {
		t.Fatalf("stale snapshot rewrote completion: %+v", observed)
	}
}
