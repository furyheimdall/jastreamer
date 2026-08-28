package api

import (
	"errors"
	"io"
	"sync"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type rendererResources struct {
	mu      sync.Mutex
	nextID  uint64
	closers map[playback.RendererID]map[uint64]io.Closer
}

func newRendererResources() rendererResources {
	return rendererResources{closers: map[playback.RendererID]map[uint64]io.Closer{}}
}

func (handler *RendererZoneAPI) TrackRendererResource(id playback.RendererID, resource io.Closer) func() {
	handler.resources.mu.Lock()
	defer handler.resources.mu.Unlock()
	handler.resources.nextID++
	resourceID := handler.resources.nextID
	if handler.resources.closers[id] == nil {
		handler.resources.closers[id] = map[uint64]io.Closer{}
	}
	handler.resources.closers[id][resourceID] = resource
	return func() {
		handler.resources.mu.Lock()
		defer handler.resources.mu.Unlock()
		delete(handler.resources.closers[id], resourceID)
	}
}

func (handler *RendererZoneAPI) closeRendererResources(id playback.RendererID) error {
	handler.resources.mu.Lock()
	closers := handler.resources.closers[id]
	delete(handler.resources.closers, id)
	handler.resources.mu.Unlock()
	failed := map[uint64]io.Closer{}
	var closeErr error
	for resourceID, closer := range closers {
		if err := closer.Close(); err != nil {
			failed[resourceID] = closer
			closeErr = errors.Join(closeErr, err)
		}
	}
	if len(failed) > 0 {
		handler.resources.mu.Lock()
		if handler.resources.closers[id] == nil {
			handler.resources.closers[id] = map[uint64]io.Closer{}
		}
		for resourceID, closer := range failed {
			handler.resources.closers[id][resourceID] = closer
		}
		handler.resources.mu.Unlock()
	}
	return closeErr
}
