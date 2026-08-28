package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

type idempotencyRecord struct {
	KeyHash     string   `json:"key_hash"`
	RequestHash string   `json:"request_hash"`
	Revision    uint64   `json:"revision"`
	Response    Snapshot `json:"response,omitzero"`
}

type persistedDocument struct {
	SchemaVersion int                 `json:"schema_version"`
	Revision      uint64              `json:"revision"`
	Settings      Values              `json:"settings"`
	Idempotency   []idempotencyRecord `json:"idempotency,omitempty"`
}

func LoadDocument(path string) (Document, error) {
	persisted, err := loadPersisted(path)
	if err != nil {
		return Document{}, err
	}
	return Document{SchemaVersion: persisted.SchemaVersion, Revision: persisted.Revision, Settings: persisted.Settings}, nil
}

func loadPersisted(path string) (persistedDocument, error) {
	file, err := os.Open(path)
	if err != nil {
		return persistedDocument{}, fmt.Errorf("open settings document: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var document persistedDocument
	if err := decoder.Decode(&document); err != nil {
		return persistedDocument{}, fmt.Errorf("decode settings document: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return persistedDocument{}, fmt.Errorf("decode settings document: exactly one object is required")
	}
	if document.SchemaVersion != CurrentSchemaVersion {
		return persistedDocument{}, fmt.Errorf("settings schema %d is unsupported", document.SchemaVersion)
	}
	return document, nil
}

func persistDocument(path string, document persistedDocument, previous *persistedDocument) error {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings document: %w", err)
	}
	data = append(data, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	if previous != nil {
		backup, err := json.MarshalIndent(previous, "", "  ")
		if err != nil {
			return fmt.Errorf("encode previous settings: %w", err)
		}
		if err := writeAtomic(path+".bak", append(backup, '\n')); err != nil {
			return fmt.Errorf("backup previous settings: %w", err)
		}
	}
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("install settings document: %w", err)
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		_ = file.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := replaceFile(temporary, path); err != nil {
		return fmt.Errorf("replace destination: %w", err)
	}
	removeTemporary = false
	if runtime.GOOS == "windows" {
		return nil
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open parent directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync parent directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close parent directory: %w", err)
	}
	return nil
}
