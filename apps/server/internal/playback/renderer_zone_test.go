package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRendererInventory_persists_K17_and_custom_identity_after_restart(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	now := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	_, err := store.UpsertK17Renderer(context.Background(), K17Renderer{
		ID: "k17-one", DisplayName: "Living room K17", State: RendererAvailable,
		UDN: "uuid:k17", Model: "FiiO K17", FirmwareVersion: "V261",
		DescriptionURL:              "http://192.0.2.1/description.xml",
		AVTransportControlURL:       "http://192.0.2.1/avtransport",
		ConnectionManagerControlURL: "http://192.0.2.1/connection",
		ProtocolInfo:                "http-get:*:audio/flac:*", LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("upsert K17: %v", err)
	}
	_, err = store.UpsertCustomRenderer(context.Background(), CustomRenderer{
		ID: "custom-one", DisplayName: "Windows harness", State: RendererConnected,
		ProtocolMajor: 3, EndpointFingerprint: "endpoint-digest",
		Capabilities: []string{"audio/flac", "seek"}, LastSeenAt: now,
	})
	if err != nil {
		t.Fatalf("upsert custom renderer: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// When
	restarted, err := Open(context.Background(), config)
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	renderers, err := restarted.Renderers(context.Background())
	// Then
	if err != nil {
		t.Fatalf("renderers: %v", err)
	}
	if len(renderers) != 2 {
		t.Fatalf("renderer count = %d", len(renderers))
	}
	if renderers[0].ID != "custom-one" || renderers[0].EndpointFingerprint != "endpoint-digest" || len(renderers[0].Capabilities) != 2 {
		t.Fatalf("custom renderer = %+v", renderers[0])
	}
	if renderers[1].K17 == nil || renderers[1].K17.UDN != "uuid:k17" || renderers[1].K17.ProtocolInfo != "http-get:*:audio/flac:*" {
		t.Fatalf("K17 renderer = %+v", renderers[1])
	}
}

func TestAssignRenderer_concurrent_mutation_preserves_one_to_one_invariants(t *testing.T) {
	tests := []struct {
		name     string
		requests []AssignmentRequest
	}{
		{name: "one renderer cannot win two zones", requests: []AssignmentRequest{
			{ZoneID: "zone-a", RendererID: "renderer-a", ExpectedRevision: 0},
			{ZoneID: "zone-b", RendererID: "renderer-a", ExpectedRevision: 0},
		}},
		{name: "one zone cannot accept two renderers", requests: []AssignmentRequest{
			{ZoneID: "zone-a", RendererID: "renderer-a", ExpectedRevision: 0},
			{ZoneID: "zone-a", RendererID: "renderer-b", ExpectedRevision: 0},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			store := openTestStore(t, testConfig(t))
			ctx := context.Background()
			for _, zone := range []ZoneDefinition{{ID: "zone-a", DisplayName: "A"}, {ID: "zone-b", DisplayName: "B"}} {
				if _, err := store.CreateZone(ctx, zone); err != nil {
					t.Fatalf("create zone: %v", err)
				}
			}
			for _, renderer := range []CustomRenderer{
				{ID: "renderer-a", DisplayName: "A", State: RendererConnected, ProtocolMajor: 3},
				{ID: "renderer-b", DisplayName: "B", State: RendererConnected, ProtocolMajor: 3},
			} {
				if _, err := store.UpsertCustomRenderer(ctx, renderer); err != nil {
					t.Fatalf("create renderer: %v", err)
				}
			}
			start := make(chan struct{})
			results := make(chan error, len(test.requests))
			var group sync.WaitGroup
			for _, request := range test.requests {
				group.Add(1)
				go func() {
					defer group.Done()
					<-start
					_, err := store.AssignRenderer(ctx, request)
					results <- err
				}()
			}

			// When
			close(start)
			group.Wait()
			close(results)

			// Then
			successes := 0
			for err := range results {
				if err == nil {
					successes++
					continue
				}
				if !errors.Is(err, ErrRevisionConflict) && !errors.Is(err, ErrRendererAssigned) {
					t.Fatalf("assignment error = %v", err)
				}
			}
			if successes != 1 {
				t.Fatalf("successful assignments = %d", successes)
			}
			snapshot, err := store.Zones(context.Background())
			if err != nil {
				t.Fatalf("zones: %v", err)
			}
			assignedZones, assignedRenderers := 0, map[RendererID]bool{}
			for _, zone := range snapshot.Zones {
				if zone.RendererID == "" {
					continue
				}
				assignedZones++
				if assignedRenderers[zone.RendererID] {
					t.Fatalf("renderer %q assigned twice", zone.RendererID)
				}
				assignedRenderers[zone.RendererID] = true
			}
			if assignedZones != 1 {
				t.Fatalf("assigned zones = %d", assignedZones)
			}
		})
	}
}

func TestAssignRenderer_rejects_active_zone_without_mutation(t *testing.T) {
	// Given
	store := openTestStore(t, testConfig(t))
	ctx := context.Background()
	if _, err := store.CreateZone(ctx, ZoneDefinition{ID: "active", DisplayName: "Active"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	for _, renderer := range []CustomRenderer{{ID: "one", DisplayName: "One", State: RendererConnected}, {ID: "two", DisplayName: "Two", State: RendererConnected}} {
		if _, err := store.UpsertCustomRenderer(ctx, renderer); err != nil {
			t.Fatalf("create renderer: %v", err)
		}
	}
	assigned, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: "active", RendererID: "one", ExpectedRevision: 0})
	if err != nil {
		t.Fatalf("assign initial renderer: %v", err)
	}
	if err := store.db.exec("UPDATE playback_zones SET transport='playing' WHERE zone_id='active'"); err != nil {
		t.Fatalf("activate zone: %v", err)
	}

	// When
	_, err = store.AssignRenderer(ctx, AssignmentRequest{ZoneID: "active", RendererID: "two", ExpectedRevision: assigned.Revision})

	// Then
	if !errors.Is(err, ErrZoneActive) {
		t.Fatalf("assignment error = %v", err)
	}
	snapshot, snapshotErr := store.Zones(ctx)
	if snapshotErr != nil {
		t.Fatalf("zones: %v", snapshotErr)
	}
	if snapshot.Zones[0].RendererID != "one" || snapshot.Zones[0].Revision != assigned.Revision {
		t.Fatalf("zone mutated = %+v", snapshot.Zones[0])
	}
}

func TestRevokeRenderer_closes_assignment_and_suspends_zone_across_restart(t *testing.T) {
	// Given
	config := testConfig(t)
	store := openTestStore(t, config)
	ctx := context.Background()
	if _, err := store.CreateZone(ctx, ZoneDefinition{ID: "zone", DisplayName: "Zone"}); err != nil {
		t.Fatalf("create zone: %v", err)
	}
	if _, err := store.UpsertCustomRenderer(ctx, CustomRenderer{ID: "renderer", DisplayName: "Renderer", State: RendererConnected}); err != nil {
		t.Fatalf("create renderer: %v", err)
	}
	if _, err := store.AssignRenderer(ctx, AssignmentRequest{ZoneID: "zone", RendererID: "renderer", ExpectedRevision: 0}); err != nil {
		t.Fatalf("assign renderer: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close before revoke: %v", err)
	}
	restarted, err := Open(ctx, config)
	if err != nil {
		t.Fatalf("restart before revoke: %v", err)
	}
	persisted, err := restarted.Zones(ctx)
	if err != nil || persisted.Zones[0].RendererID != "renderer" {
		t.Fatalf("persisted assignment = %+v, %v", persisted.Zones, err)
	}

	// When
	if err := restarted.RevokeRenderer(ctx, "renderer"); err != nil {
		t.Fatalf("revoke renderer: %v", err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("close after revoke: %v", err)
	}
	final, err := Open(ctx, config)
	if err != nil {
		t.Fatalf("restart after revoke: %v", err)
	}
	t.Cleanup(func() { _ = final.Close() })
	snapshot, err := final.Zones(ctx)
	// Then
	if err != nil {
		t.Fatalf("zones: %v", err)
	}
	if snapshot.Zones[0].RendererID != "" || snapshot.Zones[0].Transport != TransportSuspended {
		t.Fatalf("zone after revoke = %+v", snapshot.Zones[0])
	}
	if snapshot.Renderers[0].State != RendererRevoked {
		t.Fatalf("renderer after revoke = %+v", snapshot.Renderers[0])
	}
}
