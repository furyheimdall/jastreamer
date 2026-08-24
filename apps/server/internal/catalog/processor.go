package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jastreamer/jastreamer-server/internal/analysis"
)

type Processor struct {
	store       *Store
	concurrency int
	ctx         context.Context
	cancel      context.CancelFunc
	signal      chan struct{}
	completed   chan TrackID
	failures    chan error
	done        sync.WaitGroup
}

func NewProcessor(store *Store, concurrency int) *Processor {
	if concurrency < 1 {
		concurrency = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Processor{store: store, concurrency: concurrency, ctx: ctx, cancel: cancel, signal: make(chan struct{}, 1), completed: make(chan TrackID, concurrency), failures: make(chan error, 1)}
}
func (p *Processor) Start()                    { p.done.Go(p.loop); p.Notify() }
func (p *Processor) Completed() <-chan TrackID { return p.completed }
func (p *Processor) Errors() <-chan error      { return p.failures }
func (p *Processor) Notify() {
	select {
	case p.signal <- struct{}{}:
	default:
	}
}
func (p *Processor) Close() { p.cancel(); p.done.Wait() }
func (p *Processor) loop() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-p.signal:
			if _, err := p.Process(0); err != nil && !errors.Is(err, context.Canceled) {
				select {
				case p.failures <- err:
				default:
				}
				p.cancel()
				return
			}
		}
	}
}

// Process drains at most limit jobs; zero means all. It is also the durable
// restart seam used by parity QA without timing or polling.
func (p *Processor) Process(limit int) (completed int, err error) {
	provenance := analysis.CurrentProvenance()
	if _, err = p.store.ScheduleAnalysis(p.ctx, provenance); err != nil {
		return 0, fmt.Errorf("schedule analysis: %w", err)
	}
	for p.ctx.Err() == nil && (limit == 0 || completed < limit) {
		batch := p.concurrency
		if limit > 0 {
			batch = min(batch, limit-completed)
		}
		jobs, claimErr := p.store.ClaimAnalysis(p.ctx, batch, provenance)
		if claimErr != nil {
			return completed, fmt.Errorf("claim analysis: %w", claimErr)
		}
		if len(jobs) == 0 {
			return completed, nil
		}
		work := make([]analysis.Job, len(jobs))
		for i, j := range jobs {
			work[i] = analysis.Job{ID: string(j.TrackID), Fingerprint: j.Fingerprint, Path: p.store.analysisPath(j.RelativePath)}
		}
		results, workerErr := analysis.NewWorker(p.concurrency).Run(p.ctx, work, nil)
		if workerErr != nil {
			return completed, fmt.Errorf("run analysis: %w", workerErr)
		}
		for _, j := range jobs {
			result, ok := results[string(j.TrackID)]
			if !ok {
				return completed, errors.New("analysis worker omitted result")
			}
			if finishErr := p.store.FinishAnalysis(p.ctx, j.TrackID, provenance, j.Fingerprint, result.Vector, result.FailureReason); finishErr != nil {
				return completed, fmt.Errorf("finish analysis %s: %w", j.TrackID, finishErr)
			}
			completed++
			select {
			case p.completed <- j.TrackID:
			default:
			}
		}
	}
	return completed, p.ctx.Err()
}
