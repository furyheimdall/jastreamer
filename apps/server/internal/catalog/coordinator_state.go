package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func loadCoordinatorState(path string) (coordinatorState, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return coordinatorState{Snapshot: EmptySnapshot()}, false, nil
	}
	if err != nil {
		return coordinatorState{}, false, fmt.Errorf("read catalog coordinator state: %w", err)
	}
	var state coordinatorState
	if err := json.Unmarshal(data, &state); err != nil {
		return coordinatorState{}, false, fmt.Errorf("decode catalog coordinator state: %w", err)
	}
	if state.Snapshot.Tracks == nil {
		state.Snapshot = EmptySnapshot()
	}
	return state, true, nil
}

func writeCoordinatorState(path string, state coordinatorState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode catalog coordinator state: %w", err)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create catalog coordinator directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".catalog-state-*")
	if err != nil {
		return fmt.Errorf("create catalog coordinator state: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect catalog coordinator state: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write catalog coordinator state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync catalog coordinator state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close catalog coordinator state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish catalog coordinator state: %w", err)
	}
	return nil
}
