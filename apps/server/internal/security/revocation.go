package security

import "context"

func (manager *Manager) Device(ctx context.Context, token string, id DeviceID) (Device, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, err := manager.authorize(ctx, token, RoleAdmin); err != nil {
		return Device{}, err
	}
	for _, stored := range manager.state.Devices {
		if stored.ID == id {
			return stored.Device, nil
		}
	}
	return Device{}, ErrDeviceNotFound
}

func (manager *Manager) Revoke(ctx context.Context, token string, id DeviceID) error {
	observers, err := manager.revoke(ctx, token, id)
	for _, observer := range observers {
		observer(id)
	}
	return err
}

func (manager *Manager) revoke(ctx context.Context, token string, id DeviceID) ([]func(DeviceID), error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, err := manager.authorize(ctx, token, RoleAdmin); err != nil {
		return nil, err
	}
	for digest, stored := range manager.state.Devices {
		if stored.ID != id {
			continue
		}
		if stored.Revoked {
			return nil, nil
		}
		if stored.Role == RoleAdmin && manager.activeAdminCount() == 1 {
			return nil, ErrLastAdmin
		}
		if stored.Role == RoleRenderer && manager.config.OperationHook != nil {
			if err := manager.config.OperationHook(OperationBeforeRevoke); err != nil {
				return nil, err
			}
		}
		next := cloneState(manager.state)
		stored.Revoked = true
		stored.State = credentialRevoked
		next.Devices[digest] = stored
		if stored.Role == RoleRenderer {
			next.RendererOperations[string(id)] = storedRendererOperation{
				Kind: RendererOperationRevoke, Device: stored.Device,
			}
		}
		if err := manager.persist(next); err != nil {
			return nil, err
		}
		manager.state = next
		observers := manager.revocationObserversSnapshot()
		if stored.Role == RoleRenderer && manager.config.OperationHook != nil {
			if err := manager.config.OperationHook(OperationAfterRevoke); err != nil {
				return observers, err
			}
		}
		return observers, nil
	}
	return nil, ErrDeviceNotFound
}

func (manager *Manager) activeAdminCount() int {
	count := 0
	for _, stored := range manager.state.Devices {
		if stored.Role == RoleAdmin && !stored.Revoked && stored.State != credentialRevoked {
			count++
		}
	}
	return count
}
