package api

import (
	"context"
	"sync"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func newEventBroker() *eventBroker { return newEventBrokerContext(context.Background()) }

func newEventBrokerContext(ctx context.Context) *eventBroker {
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	return &eventBroker{
		done: done, epoch: 1, revisions: make(map[string]uint64), subscribers: make(map[uint64]*eventSubscriber),
		tickets: newDefaultEventTicketStore(),
	}
}

type eventSubscriber struct {
	mu         sync.Mutex
	deviceID   security.DeviceID
	events     chan eventEnvelope
	resync     chan eventEnvelope
	revoked    chan struct{}
	active     bool
	overflowed bool
}

type eventSubscription struct {
	subscriber  *eventSubscriber
	events      <-chan eventEnvelope
	resync      <-chan eventEnvelope
	revoked     <-chan struct{}
	snapshot    eventEnvelope
	unsubscribe func()
}

type eventBroker struct {
	mu          sync.Mutex
	done        <-chan struct{}
	epoch       uint64
	sequence    uint64
	nextID      uint64
	revisions   map[string]uint64
	subscribers map[uint64]*eventSubscriber
	tickets     *eventTicketStore
}

func (broker *eventBroker) subscribe(deviceID security.DeviceID) eventSubscription {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	broker.nextID++
	id := broker.nextID
	subscriber := &eventSubscriber{
		deviceID: deviceID, events: make(chan eventEnvelope, eventBufferSize),
		resync: make(chan eventEnvelope, 1), revoked: make(chan struct{}), active: true,
	}
	broker.subscribers[id] = subscriber
	resources := make([]resourceRevision, 0, len(broker.revisions))
	for resource, revision := range broker.revisions {
		resources = append(resources, resourceRevision{Resource: resource, Revision: revision})
	}
	var once sync.Once
	return eventSubscription{
		subscriber: subscriber, events: subscriber.events, resync: subscriber.resync, revoked: subscriber.revoked,
		snapshot: eventEnvelope{Type: eventTypeSnapshot, Epoch: broker.epoch, Sequence: broker.sequence, Resources: resources},
		unsubscribe: func() {
			once.Do(func() {
				broker.mu.Lock()
				delete(broker.subscribers, id)
				broker.mu.Unlock()
				subscriber.deactivate(false)
			})
		},
	}
}

func (subscriber *eventSubscriber) deactivate(signal bool) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	if !subscriber.active {
		return
	}
	subscriber.active = false
	if signal {
		close(subscriber.revoked)
	}
}

func (subscriber *eventSubscriber) writeIfActive(write func() error) (bool, error) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	if !subscriber.active {
		return false, nil
	}
	return true, write()
}

func (broker *eventBroker) revokeDevice(id security.DeviceID) {
	broker.mu.Lock()
	subscribers := make([]*eventSubscriber, 0)
	for _, subscriber := range broker.subscribers {
		if subscriber.deviceID == id {
			subscribers = append(subscribers, subscriber)
		}
	}
	broker.mu.Unlock()
	for _, subscriber := range subscribers {
		subscriber.deactivate(true)
	}
}

func (broker *eventBroker) publishInvalidation(resource string, revision uint64) {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if current, exists := broker.revisions[resource]; exists && revision <= current {
		return
	}
	broker.revisions[resource] = revision
	broker.sequence++
	event := eventEnvelope{
		Type: eventTypeInvalidation, Epoch: broker.epoch, Sequence: broker.sequence,
		Resource: resource, Revision: revision,
	}
	for _, subscriber := range broker.subscribers {
		subscriber.mu.Lock()
		if !subscriber.active || subscriber.overflowed {
			subscriber.mu.Unlock()
			continue
		}
		select {
		case subscriber.events <- event:
		default:
			subscriber.overflowed = true
			subscriber.resync <- eventEnvelope{Type: eventTypeResyncRequired, Epoch: broker.epoch, Sequence: broker.sequence}
		}
		subscriber.mu.Unlock()
	}
}
