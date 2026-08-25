package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

func (manager *Manager) isLimited(requester string, now time.Time) bool {
	value, exists := manager.state.Failures[requester]
	return exists && value.Count >= manager.config.MaxFailures && now.Before(value.Since.Add(manager.config.PairingTTL))
}

func recordFailure(state *persistedState, requester string, now time.Time, ttl time.Duration) {
	value := state.Failures[requester]
	if value.Since.IsZero() || !now.Before(value.Since.Add(ttl)) {
		value = failures{Since: now}
	}
	value.Count++
	state.Failures[requester] = value
}

func (manager *Manager) persist(state persistedState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode security state: %w", err)
	}
	directoryPath := filepath.Dir(manager.config.StatePath)
	if err := os.MkdirAll(directoryPath, 0o700); err != nil {
		return fmt.Errorf("create security directory: %w", err)
	}
	if err := secureDirectory(directoryPath); err != nil {
		return fmt.Errorf("secure security directory: %w", err)
	}
	temporary := manager.config.StatePath + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create security state: %w", err)
	}
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write security state: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync security state: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close security state: %w", err)
	}
	if err := replaceFile(temporary, manager.config.StatePath); err != nil {
		return fmt.Errorf("replace security state: %w", err)
	}
	if runtime.GOOS != "windows" {
		directory, err := os.Open(directoryPath)
		if err != nil {
			return fmt.Errorf("open security directory: %w", err)
		}
		if err := directory.Sync(); err != nil {
			_ = directory.Close()
			return fmt.Errorf("sync security directory: %w", err)
		}
		if err := directory.Close(); err != nil {
			return fmt.Errorf("close security directory: %w", err)
		}
	}
	return nil
}

func cloneState(state persistedState) persistedState {
	cloned := state
	cloned.Devices = make(map[string]storedDevice, len(state.Devices))
	maps.Copy(cloned.Devices, state.Devices)
	cloned.Codes = make(map[string]storedCode, len(state.Codes))
	maps.Copy(cloned.Codes, state.Codes)
	cloned.Failures = make(map[string]failures, len(state.Failures))
	maps.Copy(cloned.Failures, state.Failures)
	return cloned
}

func (manager *Manager) ensureMaps() {
	if manager.state.Devices == nil {
		manager.state.Devices = map[string]storedDevice{}
	}
	if manager.state.Codes == nil {
		manager.state.Codes = map[string]storedCode{}
	}
	if manager.state.Failures == nil {
		manager.state.Failures = map[string]failures{}
	}
}

func tokenDigest(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
