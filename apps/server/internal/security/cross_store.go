package security

import (
	"context"
	"fmt"
	"sort"
)

type RendererOperationKind string

const (
	RendererOperationPair   RendererOperationKind = "pair"
	RendererOperationRevoke RendererOperationKind = "revoke"
)

type storedRendererOperation struct {
	Kind           RendererOperationKind `json:"kind"`
	Device         Device                `json:"device"`
	InventoryReady bool                  `json:"inventory_ready,omitempty"`
}

type RendererOperation struct {
	Kind           RendererOperationKind
	Device         Device
	InventoryReady bool
}

func (manager *Manager) MarkRendererInventoryReady(ctx context.Context, id DeviceID) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	operation, exists := manager.state.RendererOperations[string(id)]
	if !exists || operation.Kind != RendererOperationPair {
		return ErrDeviceNotFound
	}
	if operation.InventoryReady {
		return nil
	}
	next := cloneState(manager.state)
	operation.InventoryReady = true
	next.RendererOperations[string(id)] = operation
	if err := manager.persist(next); err != nil {
		return fmt.Errorf("persist renderer inventory readiness: %w", err)
	}
	manager.state = next
	return nil
}

func (manager *Manager) PendingRendererOperations(ctx context.Context) ([]RendererOperation, error) {
	return manager.rendererOperations(ctx, false)
}

func (manager *Manager) RecoverableRendererOperations(ctx context.Context) ([]RendererOperation, error) {
	return manager.rendererOperations(ctx, true)
}

func (manager *Manager) rendererOperations(ctx context.Context, recoverableOnly bool) ([]RendererOperation, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	operations := make([]RendererOperation, 0, len(manager.state.RendererOperations))
	for _, operation := range manager.state.RendererOperations {
		_, fresh := manager.freshPairs[string(operation.Device.ID)]
		if recoverableOnly && operation.Kind == RendererOperationPair && fresh {
			continue
		}
		operations = append(operations, RendererOperation{
			Kind: operation.Kind, Device: operation.Device, InventoryReady: operation.InventoryReady,
		})
	}
	sort.Slice(operations, func(left, right int) bool { return operations[left].Device.ID < operations[right].Device.ID })
	return operations, nil
}

func (manager *Manager) AbortRendererPair(ctx context.Context, id DeviceID) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	operation, exists := manager.state.RendererOperations[string(id)]
	if !exists {
		return nil
	}
	if operation.Kind == RendererOperationRevoke {
		return nil
	}
	next := cloneState(manager.state)
	for digest, stored := range next.Devices {
		if stored.ID == id {
			stored.Revoked = true
			stored.State = credentialRevoked
			next.Devices[digest] = stored
			break
		}
	}
	operation.Kind = RendererOperationRevoke
	operation.Device.Revoked = true
	next.RendererOperations[string(id)] = operation
	if err := manager.persist(next); err != nil {
		return fmt.Errorf("persist renderer pairing compensation: %w", err)
	}
	manager.state = next
	delete(manager.freshPairs, string(id))
	return nil
}

func (manager *Manager) CompleteRendererOperation(ctx context.Context, id DeviceID) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if _, exists := manager.state.RendererOperations[string(id)]; !exists {
		delete(manager.freshPairs, string(id))
		return nil
	}
	next := cloneState(manager.state)
	delete(next.RendererOperations, string(id))
	if err := manager.persist(next); err != nil {
		return fmt.Errorf("complete renderer operation: %w", err)
	}
	manager.state = next
	delete(manager.freshPairs, string(id))
	return nil
}
