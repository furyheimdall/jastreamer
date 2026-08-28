package api_test

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/catalog"
)

func TestConfigHandler_PATCH_reconcile_failure_keeps_durable_desired_roots_for_recovery(t *testing.T) {
	// Given
	fixture := newConfigFixture(t)
	base := fixture.store.Snapshot().Locks.AllowedCatalogBases[0]
	left := filepath.Join(base, "left")
	right := filepath.Join(base, "right")
	for _, path := range []string{left, right} {
		if err := os.Mkdir(path, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	statePath := filepath.Join(fixture.store.Snapshot().Locks.DataDirectory, "catalog", "coordinator.json")
	coordinator, err := catalog.OpenCoordinator(t.Context(), catalog.CoordinatorConfig{
		StatePath: statePath, AllowedBases: []string{base}, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReconcileRoots(t.Context(), []catalog.DesiredRoot{{ID: "left", DisplayName: "Left", Path: left}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(statePath, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture.handler = api.New(api.Config{Security: fixture.manager, Settings: fixture.store, CatalogCoordinator: coordinator, Context: t.Context()})
	body := fmt.Sprintf(`{"catalog_roots":[{"id":"right","display_name":"Right","path":%q}]}`, right)

	// When
	response := configRequest(t, fixture, http.MethodPatch, fixture.admin.Token, body, map[string]string{
		"If-Match": `"0"`, "Idempotency-Key": "recoverable-roots",
	})
	persisted := fixture.store.Snapshot()
	rootsBeforeRecovery := coordinator.Roots()
	if err := coordinator.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	recovered, err := catalog.OpenCoordinator(t.Context(), catalog.CoordinatorConfig{
		StatePath: statePath, AllowedBases: []string{base}, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = recovered.Close() })
	reconcileErr := recovered.ReconcileRoots(t.Context(), []catalog.DesiredRoot{{ID: "right", DisplayName: "Right", Path: right}})

	// Then
	if response.Code != http.StatusInternalServerError || persisted.Revision != 1 || len(persisted.Settings.CatalogRoots) != 1 || persisted.Settings.CatalogRoots[0].ID != "right" {
		t.Fatalf("failure response=%d %s persisted=%+v", response.Code, response.Body.String(), persisted)
	}
	if len(rootsBeforeRecovery) != 1 || rootsBeforeRecovery[0].ID != "left" || reconcileErr != nil || len(recovered.Roots()) != 1 || recovered.Roots()[0].ID != "right" {
		t.Fatalf("recovery prior=%+v err=%v recovered=%+v", rootsBeforeRecovery, reconcileErr, recovered.Roots())
	}
}
