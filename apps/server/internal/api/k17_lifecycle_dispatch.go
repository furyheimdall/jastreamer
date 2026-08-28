package api

import (
	"context"
	"errors"

	"github.com/jastreamer/jastreamer-server/internal/catalog"
	"github.com/jastreamer/jastreamer-server/internal/media"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type K17AdapterProvider interface {
	PlaybackAdapter(playback.RendererID, playback.ZoneID) (playback.K17PlaybackAdapter, error)
}

type K17LifecycleDispatcherConfig struct {
	Queue    *playback.Store
	Media    *media.Service
	Provider K17AdapterProvider
	BaseURL  string
}

type K17LifecycleDispatcher struct {
	config K17LifecycleDispatcherConfig
}

type K17PlayDispatch struct {
	ZoneID   playback.ZoneID
	Decision playback.Decision
	BaseURL  string
}

func NewK17LifecycleDispatcher(config K17LifecycleDispatcherConfig) *K17LifecycleDispatcher {
	return &K17LifecycleDispatcher{config: config}
}

func (dispatcher *K17LifecycleDispatcher) RecoverPending(ctx context.Context) error {
	if dispatcher.config.Queue == nil {
		return media.ErrInvalidConfig
	}
	if err := dispatcher.config.Queue.ReconcileInterruptedK17Dispatches(ctx); err != nil {
		return err
	}
	pending, err := dispatcher.config.Queue.PendingK17LifecycleDispatches(ctx)
	if err != nil {
		return err
	}
	for _, result := range pending {
		if err := dispatcher.Dispatch(ctx, result); err != nil {
			return err
		}
	}
	return nil
}

func (dispatcher *K17LifecycleDispatcher) Dispatch(ctx context.Context, result playback.K17LifecycleResult) error {
	switch result.Action {
	case playback.K17LifecycleIgnored, playback.K17LifecycleReconciled, playback.K17LifecycleSuspended:
		return nil
	case playback.K17LifecycleNaturalEnd:
		switch result.Decision.Kind {
		case playback.DecisionPlay:
			return dispatcher.DispatchPlay(ctx, K17PlayDispatch{
				ZoneID: result.ZoneID, Decision: result.Decision, BaseURL: dispatcher.config.BaseURL,
			})
		case playback.DecisionBlock, playback.DecisionStop:
			return nil
		default:
			return playback.ErrInvalidObservation
		}
	default:
		return playback.ErrInvalidObservation
	}
}

func (dispatcher *K17LifecycleDispatcher) DispatchPlay(ctx context.Context, dispatch K17PlayDispatch) error {
	zoneID, decision, baseURL := dispatch.ZoneID, dispatch.Decision, dispatch.BaseURL
	if dispatcher.config.Queue == nil || dispatcher.config.Media == nil || dispatcher.config.Provider == nil ||
		zoneID == "" || decision.ID == "" || decision.Kind != playback.DecisionPlay || decision.PlayID == "" || decision.TrackID == "" || baseURL == "" {
		return media.ErrInvalidConfig
	}
	identity := playback.K17DispatchIdentity{ZoneID: zoneID, CommandID: decision.ID, PlayID: decision.PlayID}
	claim, err := dispatcher.config.Queue.ClaimK17TransportDispatch(ctx, identity)
	if err != nil {
		return err
	}
	switch claim {
	case playback.K17DispatchCompleted, playback.K17DispatchInFlight:
		return nil
	case playback.K17DispatchClaimed:
	default:
		return playback.ErrCommandDeliveryConflict
	}
	renderer, err := dispatcher.config.Queue.AssignedRenderer(ctx, zoneID)
	if err != nil {
		return dispatcher.fail(ctx, identity, err)
	}
	if renderer.Kind != playback.RendererKindK17 {
		return dispatcher.fail(ctx, identity, playback.ErrInvalidObservation)
	}
	issued, err := dispatcher.config.Media.IssueMedia(ctx, media.IssueRequest{
		BaseURL: baseURL, Audience: media.AudienceK17Capability, RendererID: renderer.ID,
		ZoneID: zoneID, PlayID: decision.PlayID, TrackID: catalog.TrackID(decision.TrackID),
		Capabilities: mediaCapabilities(renderer),
	})
	if err != nil {
		return dispatcher.fail(ctx, identity, err)
	}
	representation := playback.MediaOriginal
	if issued.Representation == media.L16 {
		representation = playback.MediaL16
	}
	adapter, err := dispatcher.config.Provider.PlaybackAdapter(renderer.ID, zoneID)
	if err != nil {
		return dispatcher.fail(ctx, identity, err)
	}
	resource := playback.MediaResource{
		URL: issued.URL, MimeType: issued.MimeType, TrackID: playback.TrackID(issued.TrackID),
		Title: issued.Title, Representation: representation,
	}
	if err := adapter.SetAVTransportURI(ctx, resource); err != nil {
		return dispatcher.fail(ctx, identity, err)
	}
	if err := adapter.Play(ctx); err != nil {
		return dispatcher.fail(ctx, identity, err)
	}
	_, err = dispatcher.config.Queue.CompleteK17TransportDispatch(ctx, identity, playback.K17DispatchSucceeded)
	return err
}

func (dispatcher *K17LifecycleDispatcher) fail(ctx context.Context, identity playback.K17DispatchIdentity, cause error) error {
	_, completionErr := dispatcher.config.Queue.CompleteK17TransportDispatch(ctx, identity, playback.K17DispatchAdapterFailed)
	return errors.Join(cause, completionErr)
}
