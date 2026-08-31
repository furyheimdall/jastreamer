package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type transportMutationBody struct {
	Command    playback.TransportCommand `json:"command"`
	PositionMS *int64                    `json:"position_ms"`
}

func (service *server) mutateTransport(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	key := request.Header.Get("Idempotency-Key")
	if key == "" {
		invalid(writer, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", http.StatusPreconditionRequired)
		return
	}
	if key != strings.TrimSpace(key) || len(key) > 128 {
		invalid(writer, "INVALID_IDEMPOTENCY_KEY", "Idempotency-Key must be exact and at most 128 characters", http.StatusBadRequest)
		return
	}
	expected, ok := revisionHeader(writer, request)
	if !ok {
		return
	}
	var body transportMutationBody
	if !decodeJSON(writer, request, &body) {
		return
	}
	position := int64(0)
	if body.PositionMS != nil {
		position = *body.PositionMS
	}
	result, err := service.config.Queue.MutateTransport(request.Context(), playback.TransportMutationRequest{
		ZoneID: playback.ZoneID(request.PathValue("zoneID")), IdempotencyKey: key,
		ExpectedRevision: playback.Revision(expected), Command: body.Command, PositionMS: position,
	})
	if err != nil {
		writeError(writer, err)
		return
	}
	publishedRevision := result.Revision
	if !result.Replayed {
		dispatch := transportDispatch{request: request, command: body.Command, positionMS: position, result: result}
		if err := service.dispatchTransport(request.Context(), dispatch); err != nil {
			failedRevision, failErr := service.config.Queue.FailTransportDispatch(request.Context(), result.CommandID)
			if failErr != nil {
				writeError(writer, failErr)
				return
			}
			publishedRevision = failedRevision
		}
	}
	writer.Header().Set("ETag", `"`+revisionString(result.Revision)+`"`)
	writeJSON(writer, http.StatusAccepted, struct {
		Revision  playback.Revision                `json:"revision"`
		CommandID string                           `json:"command_id"`
		Status    playback.TransportMutationStatus `json:"status"`
	}{Revision: result.Revision, CommandID: result.CommandID, Status: result.Status})
	if !result.Replayed {
		service.publishZoneState("transport", request.PathValue("zoneID"), publishedRevision)
	}
}

type transportDispatch struct {
	request    *http.Request
	command    playback.TransportCommand
	positionMS int64
	result     playback.TransportMutationResult
}

func (service *server) dispatchTransport(ctx context.Context, dispatch transportDispatch) error {
	request, requested, result := dispatch.request, dispatch.command, dispatch.result
	command := result.PhysicalCommand
	if command == "" {
		command = requested
	}
	renderer, err := service.config.Queue.AssignedRenderer(ctx, playback.ZoneID(request.PathValue("zoneID")))
	if err != nil {
		return err
	}
	zoneID := playback.ZoneID(request.PathValue("zoneID"))
	if renderer.Kind == playback.RendererKindK17 && (command == playback.TransportStart || command == playback.TransportPlay) {
		provider, ok := service.config.UPnP.(K17AdapterProvider)
		if !ok {
			return playback.ErrRendererOffline
		}
		baseURL := service.config.ServerHTTPSOrigin.value
		if compatibilityBaseURL := k17CompatibilityBaseURL(service.config); compatibilityBaseURL != "" {
			baseURL = compatibilityBaseURL
		}
		if baseURL == "" {
			return media.ErrInvalidConfig
		}
		dispatcher := NewK17LifecycleDispatcher(K17LifecycleDispatcherConfig{
			Queue: service.config.Queue, Media: service.config.Media, Provider: provider,
		})
		return dispatcher.DispatchPlay(ctx, K17PlayDispatch{
			ZoneID: zoneID, BaseURL: baseURL,
			Decision: playback.Decision{
				ID: result.CommandID, Kind: playback.DecisionPlay, PlayID: result.PlayID, TrackID: result.TrackID,
			},
		})
	}
	if command == playback.TransportStart || command == playback.TransportPlay {
		if service.config.Media == nil {
			return service.config.Queue.MarkTransportMediaReady(ctx, result.CommandID)
		}
		if service.config.ServerHTTPSOrigin.value == "" {
			return media.ErrInvalidConfig
		}
		issued, err := service.config.Media.IssueMedia(ctx, media.IssueRequest{
			BaseURL: service.config.ServerHTTPSOrigin.value, Audience: media.AudienceCustomRenderer,
			RendererID: renderer.ID, ZoneID: zoneID, PlayID: result.PlayID,
			TrackID: catalog.TrackID(result.TrackID), Capabilities: mediaCapabilities(renderer),
		})
		if err != nil {
			return err
		}
		return service.config.Queue.AttachTransportMedia(ctx, result.CommandID, playback.TransportMedia{
			URL: issued.URL, MimeType: issued.MimeType,
		})
	}
	if renderer.Kind != playback.RendererKindK17 {
		return nil
	}
	provider, ok := service.config.UPnP.(k17AdapterProvider)
	if !ok {
		return playback.ErrRendererOffline
	}
	adapter, err := provider.PlaybackAdapter(renderer.ID, playback.ZoneID(request.PathValue("zoneID")))
	if err != nil {
		return err
	}
	switch command {
	case playback.TransportPause:
		return adapter.Pause(ctx)
	case playback.TransportResume:
		return adapter.Play(ctx)
	case playback.TransportStop:
		return adapter.Stop(ctx)
	case playback.TransportSkip:
		if err := adapter.Stop(ctx); err != nil {
			return err
		}
		_, err := service.config.Queue.CompleteAcknowledgedSkip(ctx, result.CommandID, "skip:"+result.CommandID)
		return err
	case playback.TransportSeek:
		positionMS := dispatch.positionMS
		if requested == playback.TransportPrevious {
			positionMS = 0
		}
		return adapter.Seek(ctx, time.Duration(positionMS)*time.Millisecond)
	case playback.TransportPrevious:
		return playback.ErrInvalidRequest
	default:
		return playback.ErrInvalidRequest
	}
}

func k17CompatibilityBaseURL(config Config) string {
	if !config.K17HTTPEnabled || config.K17MediaBaseURL == "" || config.K17MediaListenerAddress == "" {
		return ""
	}
	parsed, err := url.Parse(config.K17MediaBaseURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host != config.K17MediaListenerAddress || parsed.User != nil || parsed.Path != "" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return ""
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return ""
	}
	address, err := netip.ParseAddr(host)
	if err != nil || (!address.IsPrivate() && !address.IsLoopback()) {
		return ""
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return ""
	}
	return parsed.String()
}

func mediaCapabilities(renderer playback.RendererInventory) []string {
	if renderer.Kind == playback.RendererKindK17 {
		return renderer.Capabilities
	}
	capabilities := make([]string, 0, len(renderer.Capabilities))
	for _, capability := range renderer.Capabilities {
		if mediaType, found := strings.CutPrefix(capability, "media:"); found {
			capabilities = append(capabilities, mediaType)
		}
	}
	return capabilities
}
