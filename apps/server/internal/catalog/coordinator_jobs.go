package catalog

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

func (coordinator *Coordinator) runScan(ctx context.Context, root Root, id ScanJobID) {
	defer coordinator.workers.Done()
	coordinator.mu.Lock()
	job := coordinator.jobs[id]
	job.Status, job.StartedAt = ScanRunning, coordinator.now().UTC()
	coordinator.jobs[id] = job
	previous := tracksForRoot(coordinator.snapshot, root.ID)
	persistErr := coordinator.persistLocked()
	coordinator.mu.Unlock()

	result, scanErr := ScanResult{}, persistErr
	if scanErr == nil {
		result, scanErr = coordinator.scan(ctx, root, previous)
		if err := ctx.Err(); err != nil {
			scanErr = err
		}
	}

	coordinator.mu.Lock()
	job = coordinator.jobs[id]
	job.FinishedAt = coordinator.now().UTC()
	published := false
	switch {
	case errors.Is(scanErr, context.Canceled) || errors.Is(scanErr, context.DeadlineExceeded):
		job.Status, job.ErrorCode = ScanCancelled, "CANCELLED"
	case scanErr != nil:
		job.Status, job.ErrorCode, job.ErrorDetail = ScanFailed, "SCAN_FAILED", scanErr.Error()
	case !result.Complete:
		job.Status, job.ErrorCode = ScanFailed, "INCOMPLETE_SCAN"
	default:
		coordinator.publish(root.ID, previous, result.Snapshot)
		job.Status, job.CatalogRevision = ScanComplete, coordinator.snapshot.Revision
		published = true
	}
	coordinator.jobs[id] = job
	coordinator.active, coordinator.cancel = "", nil
	if persistErr := coordinator.persistLocked(); persistErr != nil {
		job.Status, job.ErrorCode, job.ErrorDetail = ScanFailed, "STATE_PERSIST_FAILED", persistErr.Error()
		coordinator.jobs[id] = job
		published = false
	}
	observer := coordinator.observer
	snapshot := Snapshot{}
	if published && observer != nil {
		snapshot = Snapshot{
			Generation: coordinator.snapshot.Generation,
			Revision:   coordinator.snapshot.Revision,
			Tracks:     cloneTracks(coordinator.snapshot.Tracks),
		}
	}
	close(coordinator.done[id])
	coordinator.mu.Unlock()
	if published && observer != nil {
		observer(snapshot)
	}
}

func (coordinator *Coordinator) CancelScan(_ context.Context, id ScanJobID) error {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	job, found := coordinator.jobs[id]
	if !found {
		return ErrScanNotFound
	}
	switch job.Status {
	case ScanQueued, ScanRunning:
	case ScanComplete, ScanFailed, ScanCancelled:
		return ErrScanFinished
	default:
		return ErrInvalidCoordinatorState
	}
	if coordinator.active != id || coordinator.cancel == nil {
		return ErrScanFinished
	}
	coordinator.cancel()
	return nil
}

func (coordinator *Coordinator) Wait(ctx context.Context, id ScanJobID) (ScanJob, error) {
	coordinator.mu.RLock()
	job, found := coordinator.jobs[id]
	done := coordinator.done[id]
	coordinator.mu.RUnlock()
	if !found {
		return ScanJob{}, ErrScanNotFound
	}
	switch job.Status {
	case ScanComplete, ScanFailed, ScanCancelled:
		return job, nil
	case ScanQueued, ScanRunning:
		if done == nil {
			return ScanJob{}, ErrInvalidCoordinatorState
		}
	default:
		return ScanJob{}, ErrInvalidCoordinatorState
	}
	select {
	case <-ctx.Done():
		return ScanJob{}, fmt.Errorf("wait for scan: %w", ctx.Err())
	case <-done:
		return coordinator.Job(id)
	}
}

func (coordinator *Coordinator) Job(id ScanJobID) (ScanJob, error) {
	coordinator.mu.RLock()
	defer coordinator.mu.RUnlock()
	job, found := coordinator.jobs[id]
	if !found {
		return ScanJob{}, ErrScanNotFound
	}
	return job, nil
}

func (coordinator *Coordinator) publish(rootID RootID, previous, scanned Snapshot) {
	changed := !reflect.DeepEqual(previous.Tracks, scanned.Tracks)
	tracks := cloneTracks(coordinator.snapshot.Tracks)
	for id, track := range tracks {
		if track.RootID == rootID {
			delete(tracks, id)
		}
	}
	for id, track := range scanned.Tracks {
		track.RootID = rootID
		if existing, found := tracks[id]; found && existing.RootID != rootID {
			id = TrackID(hashID("root-track", string(rootID), string(id)))
			track.TrackID, track.FileID, track.Order.TrackID = id, FileID(hashID("root-file", string(rootID), string(track.FileID))), id
		}
		tracks[id] = track
	}
	coordinator.snapshot.Tracks = tracks
	coordinator.snapshot.Generation++
	if changed {
		coordinator.snapshot.Revision++
	}
}

func tracksForRoot(snapshot Snapshot, rootID RootID) Snapshot {
	result := Snapshot{Generation: snapshot.Generation, Tracks: make(map[TrackID]Track)}
	for id, track := range snapshot.Tracks {
		if track.RootID == rootID {
			result.Tracks[id] = track
		}
	}
	return result
}
