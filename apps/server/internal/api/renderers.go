package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

type RendererZoneAPI struct {
	security         *security.Manager
	store            *playback.Store
	media            *media.Service
	resources        rendererResources
	sessions         *rendererSessionRegistry
	onChanged        func(string, playback.Revision)
	operationMu      sync.Mutex
	recoveryComplete bool
	recoveryErr      error
	operationHook    func(rendererOperationStage) error
}

func NewRendererZoneAPI(manager *security.Manager, store *playback.Store) *RendererZoneAPI {
	return newRendererZoneAPI(context.Background(), manager, store, nil)
}

func newRendererZoneAPI(
	ctx context.Context,
	manager *security.Manager,
	store *playback.Store,
	onChanged func(string, playback.Revision),
) *RendererZoneAPI {
	handler := &RendererZoneAPI{
		security: manager, store: store, resources: newRendererResources(),
		sessions: newRendererSessionRegistry(ctx), onChanged: onChanged,
	}
	stopObserving := manager.ObserveRevocations(func(id security.DeviceID) {
		handler.sessions.revoke(playback.RendererID(id))
	})
	if done := ctx.Done(); done != nil {
		go func() {
			<-done
			stopObserving()
		}()
	}
	handler.operationMu.Lock()
	handler.recoveryErr = handler.ensureRendererRecoveryLocked(ctx)
	handler.operationMu.Unlock()
	return handler
}

func (handler *RendererZoneAPI) CreateZone(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRole(writer, request, security.RoleAdmin); !ok {
		return
	}
	var body struct {
		ZoneID string `json:"zone_id"`
		Name   string `json:"name"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	zone, err := handler.store.CreateZone(request.Context(), playback.ZoneDefinition{
		ID: playback.ZoneID(body.ZoneID), DisplayName: body.Name,
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	if handler.onChanged != nil {
		handler.onChanged("zones", zone.Revision)
	}
	writeJSON(writer, http.StatusCreated, zoneResponse(zone))
}

type zonePayload struct {
	ZoneID     playback.ZoneID      `json:"zone_id"`
	Name       string               `json:"name"`
	Revision   playback.Revision    `json:"revision"`
	RendererID *playback.RendererID `json:"renderer_id"`
	Transport  playback.Transport   `json:"transport"`
}

type rendererPayload struct {
	RendererID   playback.RendererID    `json:"renderer_id"`
	Name         string                 `json:"name"`
	Kind         string                 `json:"kind"`
	Status       playback.RendererState `json:"status"`
	Capabilities []string               `json:"capabilities"`
	LastSeenAt   time.Time              `json:"last_seen_at"`
}

type zonesPayload struct {
	Zones     []zonePayload     `json:"zones"`
	Renderers []rendererPayload `json:"renderers"`
}

func (handler *RendererZoneAPI) ListZones(writer http.ResponseWriter, request *http.Request) {
	if err := handler.ensureRendererRecovery(request.Context()); err != nil {
		writeError(writer, err)
		return
	}
	if !handler.authenticateControl(writer, request) {
		return
	}
	snapshot, err := handler.store.Zones(request.Context())
	if err != nil {
		writeError(writer, err)
		return
	}
	zones := make([]zonePayload, 0, len(snapshot.Zones))
	for _, zone := range snapshot.Zones {
		zones = append(zones, zoneResponse(zone))
	}
	renderers := make([]rendererPayload, 0, len(snapshot.Renderers))
	for _, renderer := range snapshot.Renderers {
		kind := "custom"
		if renderer.Kind == playback.RendererKindK17 {
			kind = "k17"
		}
		capabilities := append(
			make([]string, 0, len(renderer.Capabilities)),
			renderer.Capabilities...,
		)
		renderers = append(renderers, rendererPayload{
			RendererID: renderer.ID, Name: renderer.DisplayName, Kind: kind,
			Status: renderer.State, Capabilities: capabilities, LastSeenAt: renderer.LastSeenAt,
		})
	}
	writeJSON(writer, http.StatusOK, zonesPayload{Zones: zones, Renderers: renderers})
}

func zoneResponse(zone playback.Zone) zonePayload {
	var rendererID *playback.RendererID
	if zone.RendererID != "" {
		value := zone.RendererID
		rendererID = &value
	}
	return zonePayload{
		ZoneID: zone.ID, Name: zone.DisplayName, Revision: zone.Revision,
		RendererID: rendererID, Transport: zone.Transport,
	}
}

func (handler *RendererZoneAPI) AssignRenderer(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRole(writer, request, security.RoleAdmin); !ok {
		return
	}
	if strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
		invalid(writer, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", http.StatusPreconditionRequired)
		return
	}
	expected, ok := revisionHeader(writer, request)
	if !ok {
		return
	}
	var body struct {
		RendererID string `json:"renderer_id"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	result, err := handler.store.AssignRenderer(request.Context(), playback.AssignmentRequest{
		ZoneID: playback.ZoneID(request.PathValue("zoneID")), RendererID: playback.RendererID(body.RendererID),
		ExpectedRevision: playback.Revision(expected),
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	if handler.onChanged != nil {
		handler.onChanged("zones", result.Revision)
	}
	writer.Header().Set("ETag", `"`+revisionString(result.Revision)+`"`)
	writeJSON(writer, http.StatusOK, struct {
		ZoneID     playback.ZoneID     `json:"zone_id"`
		RendererID playback.RendererID `json:"renderer_id"`
		Revision   playback.Revision   `json:"revision"`
	}{ZoneID: result.ZoneID, RendererID: result.RendererID, Revision: result.Revision})
}

func revisionString(revision playback.Revision) string {
	return strconv.FormatInt(int64(revision), 10)
}

func (handler *RendererZoneAPI) RevokeDevice(writer http.ResponseWriter, request *http.Request) {
	if _, ok := handler.authenticateRole(writer, request, security.RoleAdmin); !ok {
		return
	}
	id := security.DeviceID(request.PathValue("deviceID"))
	device, err := handler.revokeCredential(request.Context(), bearer(request), id)
	if err != nil {
		writeError(writer, err)
		return
	}
	if device.Role == security.RoleRenderer && handler.onChanged != nil {
		renderer, rendererErr := handler.store.Renderer(request.Context(), playback.RendererID(device.ID))
		if rendererErr != nil {
			writeError(writer, rendererErr)
			return
		}
		handler.onChanged("renderers", renderer.Revision)
	}
	writer.WriteHeader(http.StatusNoContent)
}
