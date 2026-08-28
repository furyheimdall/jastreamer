package media

import (
	"testing"
)

type closeSignal struct {
	closed chan struct{}
}

func (signal *closeSignal) Close() error {
	close(signal.closed)
	return nil
}

func TestActiveStreams_cancel_exact_play_or_renderer(t *testing.T) {
	// Given
	service := &Service{active: map[uint64]activeStream{}}
	playOne := &closeSignal{closed: make(chan struct{})}
	playTwo := &closeSignal{closed: make(chan struct{})}
	service.track(Claims{RendererID: "renderer-1", PlayID: "play-1"}, playOne)
	service.track(Claims{RendererID: "renderer-2", PlayID: "play-2"}, playTwo)

	// When
	if err := service.CancelPlay("play-1"); err != nil {
		t.Fatal(err)
	}

	// Then
	select {
	case <-playOne.closed:
	default:
		t.Fatal("stopped play remained open")
	}
	select {
	case <-playTwo.closed:
		t.Fatal("unrelated play was closed")
	default:
	}
	if err := service.CancelRenderer("renderer-2"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-playTwo.closed:
	default:
		t.Fatal("revoked renderer stream remained open")
	}
}
