package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

type rendererOperationStage string

const (
	rendererBeforeInventory      rendererOperationStage = "before_inventory"
	rendererAfterInventory       rendererOperationStage = "after_inventory"
	rendererBeforePlaybackRevoke rendererOperationStage = "before_playback_revoke"
	rendererAfterPlaybackRevoke  rendererOperationStage = "after_playback_revoke"
	rendererBeforeResourceClose  rendererOperationStage = "before_resource_close"
	rendererAfterResourceClose   rendererOperationStage = "after_resource_close"
	rendererBeforePairResponse   rendererOperationStage = "before_pair_response"
)

type rendererPairRequest struct {
	Code         string
	Registration security.Registration
	Requester    string
}

func (handler *RendererZoneAPI) ensureRendererRecovery(ctx context.Context) error {
	handler.operationMu.Lock()
	defer handler.operationMu.Unlock()
	return handler.ensureRendererRecoveryLocked(ctx)
}

func (handler *RendererZoneAPI) ensureRendererRecoveryLocked(ctx context.Context) error {
	if handler.recoveryComplete {
		return nil
	}
	if err := handler.recoverRendererOperations(ctx); err != nil {
		handler.recoveryErr = err
		return err
	}
	handler.recoveryComplete = true
	handler.recoveryErr = nil
	return nil
}

func (handler *RendererZoneAPI) pairCredential(ctx context.Context, request rendererPairRequest) (security.Credential, error) {
	handler.operationMu.Lock()
	defer handler.operationMu.Unlock()
	if err := handler.ensureRendererRecoveryLocked(ctx); err != nil {
		return security.Credential{}, err
	}
	credential, err := handler.security.Pair(ctx, request.Code, request.Registration, request.Requester)
	if err != nil {
		return security.Credential{}, err
	}
	if credential.Device.Role != security.RoleRenderer {
		return credential, nil
	}
	if err := handler.prepareRendererPair(ctx, credential); err != nil {
		return security.Credential{}, err
	}
	return credential, nil
}

func (handler *RendererZoneAPI) revokeCredential(ctx context.Context, adminToken string, id security.DeviceID) (security.Device, error) {
	handler.operationMu.Lock()
	defer handler.operationMu.Unlock()
	if err := handler.ensureRendererRecoveryLocked(ctx); err != nil {
		return security.Device{}, err
	}
	device, err := handler.security.Device(ctx, adminToken, id)
	if err != nil {
		return security.Device{}, err
	}
	if err := handler.security.Revoke(ctx, adminToken, id); err != nil {
		return security.Device{}, err
	}
	if device.Role != security.RoleRenderer {
		return device, nil
	}
	if err := handler.finishRendererRevocation(ctx, playback.RendererID(id)); err != nil {
		return security.Device{}, err
	}
	return device, nil
}

func (handler *RendererZoneAPI) beforePairResponse() error {
	handler.operationMu.Lock()
	defer handler.operationMu.Unlock()
	if handler.operationHook == nil {
		return nil
	}
	if err := handler.operationHook(rendererBeforePairResponse); err != nil {
		return fmt.Errorf("%w: before pairing response: %w", security.ErrRendererOperationPending, err)
	}
	return nil
}

func (handler *RendererZoneAPI) recoverRendererOperations(ctx context.Context) error {
	operations, err := handler.security.RecoverableRendererOperations(ctx)
	if err != nil {
		return fmt.Errorf("load renderer operations: %w", err)
	}
	for _, operation := range operations {
		if operation.Kind == security.RendererOperationPair {
			if err := handler.security.AbortRendererPair(ctx, operation.Device.ID); err != nil {
				return fmt.Errorf("%w: abort undelivered renderer pairing: %w", security.ErrRendererOperationPending, err)
			}
		}
		if err := handler.finishRendererRevocation(ctx, playback.RendererID(operation.Device.ID)); err != nil {
			return err
		}
	}
	return nil
}

func (handler *RendererZoneAPI) finishRendererRevocation(ctx context.Context, id playback.RendererID) error {
	if handler.operationHook != nil {
		if err := handler.operationHook(rendererBeforePlaybackRevoke); err != nil {
			return fmt.Errorf("%w: before playback revocation: %w", security.ErrRendererOperationPending, err)
		}
	}
	playbackErr := handler.store.RevokeRenderer(ctx, id)
	if playbackErr != nil && !errors.Is(playbackErr, playback.ErrRendererNotFound) {
		return fmt.Errorf("%w: reconcile renderer playback state: %w", security.ErrRendererOperationPending, playbackErr)
	}
	if handler.media != nil {
		if err := handler.media.CancelRenderer(id); err != nil {
			return fmt.Errorf("%w: cancel renderer media: %w", security.ErrRendererOperationPending, err)
		}
	}
	if handler.operationHook != nil {
		if err := handler.operationHook(rendererAfterPlaybackRevoke); err != nil {
			return fmt.Errorf("%w: after playback revocation: %w", security.ErrRendererOperationPending, err)
		}
		if err := handler.operationHook(rendererBeforeResourceClose); err != nil {
			return fmt.Errorf("%w: before resource close: %w", security.ErrRendererOperationPending, err)
		}
	}
	if err := handler.closeRendererResources(id); err != nil {
		return fmt.Errorf("%w: close renderer resources: %w", security.ErrRendererOperationPending, err)
	}
	if handler.operationHook != nil {
		if err := handler.operationHook(rendererAfterResourceClose); err != nil {
			return fmt.Errorf("%w: after resource close: %w", security.ErrRendererOperationPending, err)
		}
	}
	if err := handler.security.CompleteRendererOperation(ctx, security.DeviceID(id)); err != nil {
		return fmt.Errorf("%w: complete renderer revocation: %w", security.ErrRendererOperationPending, err)
	}
	return nil
}

func (handler *RendererZoneAPI) prepareRendererPair(ctx context.Context, credential security.Credential) error {
	if handler.operationHook != nil {
		if err := handler.operationHook(rendererBeforeInventory); err != nil {
			abortErr := handler.security.AbortRendererPair(ctx, credential.Device.ID)
			return errors.Join(fmt.Errorf("%w: before renderer inventory: %w", security.ErrRendererOperationPending, err), abortErr)
		}
	}
	_, inventoryErr := handler.store.UpsertCustomRenderer(ctx, playback.CustomRenderer{
		ID: playback.RendererID(credential.Device.ID), DisplayName: credential.Device.Name,
		State: playback.RendererUnavailable, LastSeenAt: credential.Device.CreatedAt,
	})
	if inventoryErr == nil {
		if handler.operationHook != nil {
			if err := handler.operationHook(rendererAfterInventory); err != nil {
				return fmt.Errorf("%w: after renderer inventory: %w", security.ErrRendererOperationPending, err)
			}
		}
		if err := handler.security.MarkRendererInventoryReady(ctx, credential.Device.ID); err == nil {
			return nil
		} else {
			inventoryErr = fmt.Errorf("mark renderer inventory ready: %w", err)
		}
	}
	abortErr := handler.security.AbortRendererPair(ctx, credential.Device.ID)
	if abortErr == nil {
		abortErr = handler.finishRendererRevocation(ctx, playback.RendererID(credential.Device.ID))
	}
	return errors.Join(
		fmt.Errorf("%w: prepare renderer inventory: %w", security.ErrRendererOperationPending, inventoryErr),
		abortErr,
	)
}
