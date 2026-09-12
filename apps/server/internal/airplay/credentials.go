package airplay

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const credentialSchema = `
CREATE TABLE IF NOT EXISTS airplay_identity (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  client_id TEXT NOT NULL,
  updated_at TEXT NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS airplay_credentials (
  device_id TEXT PRIMARY KEY,
  credentials TEXT NOT NULL,
  password TEXT NOT NULL,
  updated_at TEXT NOT NULL
) STRICT;
`

type storedAuth struct {
	credentials string
	password    string
}

func initializeCredentials(ctx context.Context, db *sql.DB) (string, error) {
	if db == nil {
		return "", errors.New("airplay: nil database")
	}
	if _, err := db.ExecContext(ctx, credentialSchema); err != nil {
		return "", fmt.Errorf("airplay: initialize credential storage: %w", err)
	}
	var identity string
	err := db.QueryRowContext(ctx, `SELECT client_id FROM airplay_identity WHERE singleton=1`).Scan(&identity)
	if err == nil {
		if validClientID(identity) {
			return identity, nil
		}
		return "", errors.New("airplay: stored client identity is invalid")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("airplay: load client identity: %w", err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("airplay: create client identity: %w", err)
	}
	identity = strings.ToUpper(hex.EncodeToString(random[:]))
	if _, err := db.ExecContext(ctx, `INSERT INTO airplay_identity(singleton,client_id,updated_at) VALUES(1,?,?)`, identity, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return "", fmt.Errorf("airplay: save client identity: %w", err)
	}
	return identity, nil
}

func validClientID(value string) bool {
	if len(value) != 16 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func loadAuth(ctx context.Context, db *sql.DB, deviceID string) (storedAuth, error) {
	var value storedAuth
	err := db.QueryRowContext(ctx, `SELECT credentials,password FROM airplay_credentials WHERE device_id=?`, deviceID).Scan(&value.credentials, &value.password)
	if errors.Is(err, sql.ErrNoRows) {
		return storedAuth{}, nil
	}
	if err != nil {
		return storedAuth{}, fmt.Errorf("airplay: load receiver credentials: %w", err)
	}
	return value, nil
}

func saveAuth(ctx context.Context, db *sql.DB, deviceID string, value storedAuth) error {
	if len(deviceID) < len("airplay:")+1 || len(deviceID) > 256 || len(value.credentials) > 8192 || len(value.password) > 1024 {
		return errors.New("airplay: invalid receiver credentials")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO airplay_credentials(device_id,credentials,password,updated_at) VALUES(?,?,?,?) ON CONFLICT(device_id) DO UPDATE SET credentials=excluded.credentials,password=excluded.password,updated_at=excluded.updated_at`, deviceID, value.credentials, value.password, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("airplay: save receiver credentials: %w", err)
	}
	return nil
}

func (manager *Manager) invalidateAuth(deviceID string, failed storedAuth) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := manager.db.ExecContext(ctx, `UPDATE airplay_credentials SET credentials='',password='',updated_at=? WHERE device_id=? AND credentials=? AND password=?`, time.Now().UTC().Format(time.RFC3339Nano), deviceID, failed.credentials, failed.password)
	if err != nil {
		return
	}
	changed, err := result.RowsAffected()
	if err != nil || changed == 0 {
		return
	}
	manager.mu.Lock()
	record, ok := manager.devices[deviceID]
	if ok && record.auth == failed {
		record.auth = storedAuth{}
		record.device.PairingRequired = record.endpoint.pairingRequired
		record.device.PasswordRequired = record.endpoint.passwordRequired
		manager.devices[deviceID] = record
	} else {
		ok = false
	}
	manager.mu.Unlock()
	if ok {
		manager.config.Notify("renderers")
	}
}
