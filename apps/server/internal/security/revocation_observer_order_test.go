package security

import (
	"slices"
	"testing"
)

func TestRevocationObserversSnapshot_preserves_registration_order(t *testing.T) {
	// Given: observers registered in the order required by revocation cutpoint barriers.
	manager := &Manager{revocationObservers: make(map[uint64]func(DeviceID))}
	observed := make([]int, 0, 3)
	for sequence := 1; sequence <= 3; sequence++ {
		value := sequence
		manager.ObserveRevocations(func(DeviceID) { observed = append(observed, value) })
	}

	// When: the committed revocation callback snapshot is delivered synchronously.
	for _, observer := range manager.revocationObserversSnapshot() {
		observer("renderer")
	}

	// Then: later barrier observers cannot run before an earlier session observer.
	if !slices.Equal(observed, []int{1, 2, 3}) {
		t.Fatalf("observer order = %v", observed)
	}
}
