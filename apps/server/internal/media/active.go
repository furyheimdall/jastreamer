package media

import (
	"errors"
	"io"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type activeStream struct {
	rendererID playback.RendererID
	playID     playback.PlayID
	resource   io.Closer
}

func (service *Service) track(claims Claims, resource io.Closer) func() {
	service.mu.Lock()
	service.nextStream++
	id := service.nextStream
	service.active[id] = activeStream{rendererID: claims.RendererID, playID: claims.PlayID, resource: resource}
	service.mu.Unlock()
	return func() {
		service.mu.Lock()
		delete(service.active, id)
		service.mu.Unlock()
	}
}

func (service *Service) CancelRenderer(rendererID playback.RendererID) error {
	return service.cancel(func(stream activeStream) bool { return stream.rendererID == rendererID })
}

func (service *Service) CancelPlay(playID playback.PlayID) error {
	return service.cancel(func(stream activeStream) bool { return stream.playID == playID })
}

func (service *Service) cancel(matches func(activeStream) bool) error {
	service.mu.Lock()
	resources := make([]io.Closer, 0)
	for id, stream := range service.active {
		if matches(stream) {
			resources = append(resources, stream.resource)
			delete(service.active, id)
		}
	}
	service.mu.Unlock()
	var result error
	for _, resource := range resources {
		result = errors.Join(result, resource.Close())
	}
	return result
}
