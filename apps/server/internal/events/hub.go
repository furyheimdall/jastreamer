package events

import (
	"sync"
)

type Event struct {
	ID   uint64 `json:"id"`
	Type string `json:"type"`
}

type Hub struct {
	mu      sync.Mutex
	next    uint64
	clients map[chan Event]struct{}
}

func New() *Hub { return &Hub{clients: make(map[chan Event]struct{})} }

// Publish disconnects slow consumers; reconnecting clients fetch current state.
func (hub *Hub) Publish(topic string) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	hub.next++
	event := Event{ID: hub.next, Type: topic}
	for client := range hub.clients {
		select {
		case client <- event:
		default:
			delete(hub.clients, client)
			close(client)
		}
	}
}

func (hub *Hub) Subscribe() (<-chan Event, func()) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	client := make(chan Event, 16)
	hub.clients[client] = struct{}{}
	client <- Event{ID: hub.next, Type: "resync"}
	return client, func() {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		if _, exists := hub.clients[client]; exists {
			delete(hub.clients, client)
			close(client)
		}
	}
}
