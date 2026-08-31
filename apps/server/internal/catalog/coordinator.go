package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Coordinator struct {
	mu        sync.RWMutex
	registry  *RootRegistry
	statePath string
	now       func() time.Time
	scan      ScanFunc
	ctx       context.Context
	stop      context.CancelFunc
	jobs      map[ScanJobID]ScanJob
	done      map[ScanJobID]chan struct{}
	active    ScanJobID
	cancel    context.CancelFunc
	snapshot  Snapshot
	nextJob   uint64
	observer  func(Snapshot)
	workers   sync.WaitGroup
	closed    bool
}

func OpenCoordinator(parent context.Context, config CoordinatorConfig) (*Coordinator, error) {
	if config.StatePath == "" || config.Now == nil {
		return nil, errors.New("catalog: invalid coordinator configuration")
	}
	if config.Scan == nil {
		config.Scan = ScanRoot
	}
	registry, err := NewRootRegistry(config.AllowedBases)
	if err != nil {
		return nil, err
	}
	state, stateExists, err := loadCoordinatorState(config.StatePath)
	if err != nil {
		return nil, err
	}
	if !stateExists {
		state.Snapshot = Snapshot{
			Generation: config.InitialSnapshot.Generation,
			Revision:   config.InitialSnapshot.Revision,
			Tracks:     cloneTracks(config.InitialSnapshot.Tracks),
		}
	}
	for _, persisted := range state.Roots {
		root := Root{ID: persisted.ID, DisplayName: persisted.DisplayName, CanonicalPath: persisted.CanonicalPath}
		if err := registry.restore(root); err != nil {
			return nil, fmt.Errorf("restore catalog root: %w", err)
		}
	}
	ctx, stop := context.WithCancel(parent)
	coordinator := &Coordinator{
		registry: registry, statePath: config.StatePath, now: config.Now, scan: config.Scan,
		ctx: ctx, stop: stop, jobs: make(map[ScanJobID]ScanJob), done: make(map[ScanJobID]chan struct{}),
		snapshot: state.Snapshot, nextJob: state.NextJob,
	}
	changed := false
	for _, job := range state.Jobs {
		switch job.Status {
		case ScanQueued, ScanRunning:
			job.Status, job.ErrorCode, job.FinishedAt = ScanCancelled, "PROCESS_RESTARTED", config.Now().UTC()
			changed = true
		case ScanComplete, ScanFailed, ScanCancelled:
		default:
			stop()
			return nil, fmt.Errorf("scan job %q status %q: %w", job.ID, job.Status, ErrInvalidCoordinatorState)
		}
		if _, exists := coordinator.jobs[job.ID]; exists {
			stop()
			return nil, fmt.Errorf("duplicate scan job %q: %w", job.ID, ErrInvalidCoordinatorState)
		}
		coordinator.jobs[job.ID] = job
	}
	if changed {
		if err := coordinator.persistLocked(); err != nil {
			stop()
			return nil, err
		}
	}
	return coordinator, nil
}

func (coordinator *Coordinator) PrepareRoot(path, displayName string) (Root, error) {
	coordinator.mu.RLock()
	defer coordinator.mu.RUnlock()
	if coordinator.closed {
		return Root{}, ErrCoordinatorClosed
	}
	return coordinator.registry.prepare(path, displayName)
}

func (coordinator *Coordinator) ReconcileRoots(ctx context.Context, desired []DesiredRoot) error {
	for {
		coordinator.mu.Lock()
		if coordinator.closed {
			coordinator.mu.Unlock()
			return ErrCoordinatorClosed
		}
		if coordinator.active == "" {
			err := coordinator.reconcileRootsLocked(desired)
			coordinator.mu.Unlock()
			return err
		}
		cancel := coordinator.cancel
		done := coordinator.done[coordinator.active]
		if cancel == nil || done == nil {
			coordinator.mu.Unlock()
			return ErrInvalidCoordinatorState
		}
		cancel()
		coordinator.mu.Unlock()
		select {
		case <-ctx.Done():
			return fmt.Errorf("reconcile roots: %w", ctx.Err())
		case <-done:
		}
	}
}

func (coordinator *Coordinator) reconcileRootsLocked(desired []DesiredRoot) error {
	next, err := coordinator.registry.reconciled(desired)
	if err != nil {
		return err
	}
	previousRegistry := coordinator.registry
	previousSnapshot := coordinator.snapshot
	coordinator.registry = next
	coordinator.snapshot = Snapshot{
		Generation: previousSnapshot.Generation,
		Revision:   previousSnapshot.Revision,
		Tracks:     cloneTracks(previousSnapshot.Tracks),
	}
	allowed := make(map[RootID]struct{}, len(desired))
	for _, root := range desired {
		allowed[root.ID] = struct{}{}
	}
	for trackID, track := range coordinator.snapshot.Tracks {
		if _, exists := allowed[track.RootID]; !exists {
			delete(coordinator.snapshot.Tracks, trackID)
		}
	}
	if len(coordinator.snapshot.Tracks) != len(previousSnapshot.Tracks) {
		coordinator.snapshot.Generation++
		coordinator.snapshot.Revision++
	}
	if err := coordinator.persistLocked(); err != nil {
		coordinator.registry = previousRegistry
		coordinator.snapshot = previousSnapshot
		return err
	}
	return nil
}

func (coordinator *Coordinator) AddRoot(_ context.Context, path, displayName string) (Root, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.closed {
		return Root{}, ErrCoordinatorClosed
	}
	root, err := coordinator.registry.Add(path, displayName)
	if err != nil {
		return Root{}, err
	}
	if err := coordinator.persistLocked(); err != nil {
		coordinator.registry.remove(root.ID)
		return Root{}, err
	}
	return root, nil
}

func (coordinator *Coordinator) Snapshot() Snapshot {
	coordinator.mu.RLock()
	defer coordinator.mu.RUnlock()
	return Snapshot{
		Generation: coordinator.snapshot.Generation,
		Revision:   coordinator.snapshot.Revision,
		Tracks:     cloneTracks(coordinator.snapshot.Tracks),
	}
}

func (coordinator *Coordinator) Roots() []Root {
	return coordinator.registry.Roots()
}

func (coordinator *Coordinator) ObserveSnapshots(observer func(Snapshot)) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	coordinator.observer = observer
}

func (coordinator *Coordinator) StartScan(_ context.Context, rootID RootID) (ScanJob, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.closed {
		return ScanJob{}, ErrCoordinatorClosed
	}
	if coordinator.active != "" {
		return ScanJob{}, ErrScanInProgress
	}
	root, err := coordinator.registry.Get(rootID)
	if err != nil {
		return ScanJob{}, err
	}
	coordinator.nextJob++
	id := ScanJobID(fmt.Sprintf("scan-%020d", coordinator.nextJob))
	job := ScanJob{ID: id, RootID: rootID, Status: ScanQueued, RequestedAt: coordinator.now().UTC()}
	coordinator.jobs[id] = job
	coordinator.done[id] = make(chan struct{})
	coordinator.active = id
	jobContext, cancel := context.WithCancel(coordinator.ctx)
	coordinator.cancel = cancel
	if err := coordinator.persistLocked(); err != nil {
		cancel()
		delete(coordinator.jobs, id)
		delete(coordinator.done, id)
		coordinator.active, coordinator.cancel = "", nil
		return ScanJob{}, err
	}
	coordinator.workers.Add(1)
	go coordinator.runScan(jobContext, root, id)
	return job, nil
}
