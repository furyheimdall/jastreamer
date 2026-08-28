package catalog

import (
	"errors"
	"sort"
)

func (coordinator *Coordinator) Close() error {
	coordinator.mu.Lock()
	if coordinator.closed {
		coordinator.mu.Unlock()
		return nil
	}
	coordinator.closed = true
	coordinator.stop()
	coordinator.mu.Unlock()
	coordinator.workers.Wait()
	return nil
}

func (coordinator *Coordinator) persistLocked() error {
	jobs := make([]ScanJob, 0, len(coordinator.jobs))
	for _, job := range coordinator.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(left, right int) bool { return jobs[left].ID < jobs[right].ID })
	roots := coordinator.registry.Roots()
	persistedRoots := make([]persistedRoot, len(roots))
	for index, root := range roots {
		persistedRoots[index] = persistedRoot{ID: root.ID, DisplayName: root.DisplayName, CanonicalPath: root.CanonicalPath}
	}
	state := coordinatorState{
		Roots: persistedRoots, Jobs: jobs,
		Snapshot: coordinator.snapshot, NextJob: coordinator.nextJob,
	}
	if err := writeCoordinatorState(coordinator.statePath, state); err != nil {
		return errors.Join(errors.New("persist catalog coordinator"), err)
	}
	return nil
}
