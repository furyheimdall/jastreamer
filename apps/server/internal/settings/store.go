package settings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
)

type Store struct {
	mu          sync.RWMutex
	path        string
	locks       Locks
	applied     Values
	value       persistedDocument
	diagnostics Diagnostics
}

func Open(config Config) (*Store, error) {
	if config.Path == "" || !filepath.IsAbs(config.Path) {
		return nil, fmt.Errorf("settings path must be absolute")
	}
	for _, temporary := range []string{config.Path + ".tmp", config.Path + ".bak.tmp"} {
		if err := os.Remove(temporary); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove interrupted settings stage: %w", err)
		}
	}
	locks, err := normalizeLocks(config.Locks)
	if err != nil {
		return nil, err
	}
	defaults, err := normalizeValues(config.Defaults, locks.AllowedCatalogBases)
	if err != nil {
		return nil, fmt.Errorf("validate default settings: %w", err)
	}
	value, err := loadPersisted(config.Path)
	switch {
	case err == nil:
		value.Settings, err = normalizeValues(value.Settings, locks.AllowedCatalogBases)
		if err != nil {
			return nil, fmt.Errorf("validate persisted settings: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		value = persistedDocument{SchemaVersion: CurrentSchemaVersion, Settings: defaults}
		if err := persistDocument(config.Path, value, nil); err != nil {
			return nil, err
		}
	default:
		backup, backupErr := loadPersisted(config.Path + ".bak")
		if backupErr != nil {
			return nil, fmt.Errorf("load settings primary: %v; backup: %w", err, backupErr)
		}
		backup.Settings, backupErr = normalizeValues(backup.Settings, locks.AllowedCatalogBases)
		if backupErr != nil {
			return nil, fmt.Errorf("validate backup settings: %w", backupErr)
		}
		if writeErr := persistDocument(config.Path, backup, nil); writeErr != nil {
			return nil, fmt.Errorf("restore settings backup: %w", writeErr)
		}
		value = backup
	}
	return &Store{path: config.Path, locks: locks, applied: cloneValues(value.Settings), value: value}, nil
}

func (store *Store) Snapshot() Snapshot {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return store.snapshot()
}

func (store *Store) SetFFmpegDiagnostic(value FFmpegDiagnostic) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if value.ConfiguredPath != "" {
		value.Warning = strings.ReplaceAll(value.Warning, value.ConfiguredPath, "[redacted]")
	}
	value.ConfiguredPath = ""
	if len(value.Warning) > 512 {
		value.Warning = value.Warning[:512]
	}
	store.diagnostics.FFmpeg = value
}

func (store *Store) Patch(ctx context.Context, mutation Mutation) (Snapshot, error) {
	result, err := store.PatchResult(ctx, mutation)
	return result.Snapshot, err
}

func (store *Store) PatchResult(ctx context.Context, mutation Mutation) (MutationResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if err := context.Cause(ctx); err != nil {
		return MutationResult{}, err
	}
	requestHash, err := hashUpdate(mutation.Update)
	if err != nil {
		return MutationResult{}, err
	}
	if mutation.IdempotencyKey != "" {
		keyHash := hashText(mutation.IdempotencyKey)
		for _, record := range store.value.Idempotency {
			if record.KeyHash != keyHash {
				continue
			}
			if record.RequestHash != requestHash {
				return MutationResult{}, ErrIdempotencyConflict
			}
			if record.Response.SchemaVersion != 0 {
				return MutationResult{Snapshot: cloneSnapshot(record.Response), Replayed: true}, nil
			}
			return MutationResult{Snapshot: store.snapshot(), Replayed: true}, nil
		}
	}
	if mutation.ExpectedRevision != store.value.Revision {
		return MutationResult{}, ErrRevisionMismatch
	}
	if locked := store.lockedField(mutation.Update); locked != "" {
		return MutationResult{}, &LockedFieldError{Field: locked}
	}
	next := applyUpdate(store.value.Settings, mutation.Update)
	next, err = normalizeValues(next, store.locks.AllowedCatalogBases)
	if err != nil {
		return MutationResult{}, err
	}
	if reflect.DeepEqual(next, store.value.Settings) {
		if mutation.IdempotencyKey == "" {
			return MutationResult{Snapshot: store.snapshot()}, nil
		}
		document := store.value
		response := store.snapshot()
		document.Idempotency = appendIdempotency(document.Idempotency, mutation.IdempotencyKey, requestHash, response)
		if err := persistDocument(store.path, document, &store.value); err != nil {
			return MutationResult{}, err
		}
		store.value = document
		return MutationResult{Snapshot: response}, nil
	}
	if store.value.Revision == ^uint64(0) {
		return MutationResult{}, ErrRevisionExhausted
	}
	document := store.value
	document.Revision++
	document.Settings = next
	response := store.snapshotFor(document)
	if mutation.IdempotencyKey != "" {
		document.Idempotency = appendIdempotency(document.Idempotency, mutation.IdempotencyKey, requestHash, response)
	}
	if err := persistDocument(store.path, document, &store.value); err != nil {
		return MutationResult{}, err
	}
	store.value = document
	return MutationResult{Snapshot: response}, nil
}

func (store *Store) snapshot() Snapshot {
	return store.snapshotFor(store.value)
}

func (store *Store) snapshotFor(document persistedDocument) Snapshot {
	fields := restartFields(store.applied, document.Settings)
	return Snapshot{
		SchemaVersion: CurrentSchemaVersion, Revision: document.Revision,
		Settings: cloneValues(document.Settings), Locks: cloneLocks(store.locks), Diagnostics: store.diagnostics,
		RestartRequired: len(fields) != 0, RestartFields: fields,
	}
}

func appendIdempotency(records []idempotencyRecord, key, requestHash string, response Snapshot) []idempotencyRecord {
	records = append(records, idempotencyRecord{
		KeyHash: hashText(key), RequestHash: requestHash, Revision: response.Revision, Response: cloneSnapshot(response),
	})
	if len(records) > 128 {
		return slices.Clone(records[len(records)-128:])
	}
	return records
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	snapshot.Settings = cloneValues(snapshot.Settings)
	snapshot.Locks = cloneLocks(snapshot.Locks)
	snapshot.RestartFields = slices.Clone(snapshot.RestartFields)
	return snapshot
}

func (store *Store) lockedField(update Update) string {
	fields := updateFields(update)
	for _, locked := range store.locks.EnvironmentLockedFields {
		if slices.Contains(fields, locked) {
			return locked
		}
	}
	return ""
}

func hashUpdate(update Update) (string, error) {
	encoded, err := json.Marshal(update)
	if err != nil {
		return "", fmt.Errorf("encode settings update: %w", err)
	}
	return hashText(string(encoded)), nil
}

func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
