package security

import (
	"slices"
	"sync"
)

// ObserveRevocations registers a notification after a revocation is durably committed.
// Notifications run synchronously in registration order. The returned function removes the observer
// and is safe to call more than once.
func (manager *Manager) ObserveRevocations(observer func(DeviceID)) func() {
	manager.mu.Lock()
	manager.nextRevocationID++
	observerID := manager.nextRevocationID
	manager.revocationObservers[observerID] = observer
	manager.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			manager.mu.Lock()
			delete(manager.revocationObservers, observerID)
			manager.mu.Unlock()
		})
	}
}

func (manager *Manager) revocationObserversSnapshot() []func(DeviceID) {
	observerIDs := make([]uint64, 0, len(manager.revocationObservers))
	for observerID := range manager.revocationObservers {
		observerIDs = append(observerIDs, observerID)
	}
	slices.Sort(observerIDs)
	observers := make([]func(DeviceID), 0, len(observerIDs))
	for _, observerID := range observerIDs {
		observers = append(observers, manager.revocationObservers[observerID])
	}
	return observers
}
