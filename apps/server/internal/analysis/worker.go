package analysis

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var ErrDuplicateJobID = errors.New("analysis: duplicate job ID")

type Status string

const (
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusComplete Status = "complete"
	StatusFailed   Status = "failed"
)

type Job struct {
	ID, Fingerprint, Path string
	WAV                   []byte
}
type Result struct {
	Status                     Status
	Provenance                 Provenance
	Fingerprint, FailureReason string
	Vector                     []byte
	Features                   Features
	Err                        error
	Attempts                   int
}
type Worker struct {
	concurrency int
	analyze     func(context.Context, []byte) (Features, error)
}

func NewWorker(n int) Worker {
	if n < 1 {
		n = 1
	}
	return Worker{n, AnalyzeWAV}
}

func (w Worker) Run(ctx context.Context, jobs []Job, previous map[string]Result) (map[string]Result, error) {
	out := make(map[string]Result, len(previous)+len(jobs))
	for k, v := range previous {
		out[k] = v
	}
	seen := make(map[string]struct{}, len(jobs))
	for _, job := range jobs {
		if _, ok := seen[job.ID]; ok {
			return out, fmt.Errorf("%s: %w", job.ID, ErrDuplicateJobID)
		}
		seen[job.ID] = struct{}{}
	}
	p := CurrentProvenance()
	pending := make(chan Job)
	var mu sync.Mutex
	var group sync.WaitGroup
	for range w.concurrency {
		group.Go(func() {
			for job := range pending {
				mu.Lock()
				prior := out[job.ID]
				out[job.ID] = Result{Status: StatusRunning, Provenance: p, Fingerprint: job.Fingerprint, Attempts: prior.Attempts + 1}
				mu.Unlock()
				var f Features
				var err error
				if job.Path != "" {
					f, err = AnalyzeFile(ctx, job.Path, nil)
				} else {
					f, err = w.analyze(ctx, job.WAV)
				}
				r := Result{Status: StatusComplete, Provenance: p, Fingerprint: job.Fingerprint, Features: f, Vector: f.Vector, Attempts: prior.Attempts + 1}
				if err != nil {
					r.Status = StatusFailed
					r.Err = err
					r.FailureReason = err.Error()
				}
				mu.Lock()
				out[job.ID] = r
				mu.Unlock()
			}
		})
	}
	var sendErr error
send:
	for _, job := range jobs {
		mu.Lock()
		prior, ok := out[job.ID]
		mu.Unlock()
		if ok && prior.Status == StatusComplete && prior.Fingerprint == job.Fingerprint && prior.Provenance == p {
			continue
		}
		select {
		case pending <- job:
		case <-ctx.Done():
			sendErr = ctx.Err()
			break send
		}
	}
	close(pending)
	group.Wait()
	if sendErr != nil {
		return out, sendErr
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}

func IsPermanent(err error) bool { return errors.Is(err, ErrUnsupported) || errors.Is(err, ErrCorrupt) }
