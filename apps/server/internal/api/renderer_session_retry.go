package api

import (
	"context"
	"errors"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type rendererRetryTimer struct {
	timer   *time.Timer
	channel <-chan time.Time
}

func newRendererRetryTimer() *rendererRetryTimer {
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	return &rendererRetryTimer{timer: timer}
}

func (retry *rendererRetryTimer) stop() {
	retry.reset(time.Time{})
}

func (retry *rendererRetryTimer) schedule(ctx context.Context, session *rendererSocketSession) error {
	at, err := session.handler.store.RendererCommandWakeAt(ctx, session.rendererID, session.epoch)
	if errors.Is(err, playback.ErrNoRendererCommand) {
		retry.reset(time.Time{})
		return nil
	}
	if err != nil {
		return err
	}
	retry.reset(at)
	return nil
}

func (retry *rendererRetryTimer) reset(at time.Time) {
	if !retry.timer.Stop() {
		select {
		case <-retry.timer.C:
		default:
		}
	}
	retry.channel = nil
	if at.IsZero() {
		return
	}
	delay := max(time.Until(at), 0)
	retry.timer.Reset(delay)
	retry.channel = retry.timer.C
}
