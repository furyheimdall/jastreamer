package api

import (
	"encoding/json"
	"sync"
)

type eventBroker struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[uint64]chan []byte
}

func newEventBroker() *eventBroker {
	return &eventBroker{subscribers: make(map[uint64]chan []byte)}
}

func (broker *eventBroker) subscribe() (<-chan []byte, func()) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.nextID++
	id := broker.nextID
	events := make(chan []byte, 8)
	broker.subscribers[id] = events
	return events, func() {
		broker.mu.Lock()
		delete(broker.subscribers, id)
		broker.mu.Unlock()
	}
}

func (broker *eventBroker) subscriberCount() int {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	return len(broker.subscribers)
}

func (broker *eventBroker) publish(event map[string]any) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	for _, subscriber := range broker.subscribers {
		select {
		case subscriber <- payload:
		default:
		}
	}
}

func (service *server) publishState(resource string, revision any) {
	service.eventHub.publish(map[string]any{
		"type": "state", "resource": resource, "revision": revision,
		"contract_revision": contractRevision,
	})
}
