package api

import "testing"

func TestEventBroker_unsubscribe_removes_subscriber(t *testing.T) {
	// Given
	broker := newEventBroker()
	_, unsubscribe := broker.subscribe()
	if broker.subscriberCount() != 1 {
		t.Fatal("subscriber was not registered")
	}

	// When
	unsubscribe()

	// Then
	if broker.subscriberCount() != 0 {
		t.Fatal("subscriber remained after disconnect cleanup")
	}
}
