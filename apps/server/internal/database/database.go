package database

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

const busyTimeoutMilliseconds = 5000

func Open(path string) (*sql.DB, error) {
	if path == "" || strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) || strings.HasPrefix(path, "file:") || strings.HasPrefix(path, "//") || strings.HasPrefix(path, `\\`) || strings.Contains(path, "://") {
		return nil, fmt.Errorf("database path must be an absolute local file path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	query := url.Values{
		"_busy_timeout": {fmt.Sprint(busyTimeoutMilliseconds)},
		"_defensive":    {"1"},
		"_foreign_keys": {"1"},
		"_journal_mode": {"WAL"},
		"_synchronous":  {"NORMAL"},
		"_txlock":       {"immediate"},
	}
	uriPath := filepath.ToSlash(path)
	if !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	closeWith := func(operationErr error) (*sql.DB, error) {
		return nil, errors.Join(operationErr, db.Close())
	}
	if err := db.Ping(); err != nil {
		return closeWith(fmt.Errorf("connect database: %w", err))
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return closeWith(fmt.Errorf("protect database: %w", err))
	}

	var foreignKeys int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return closeWith(fmt.Errorf("verify database foreign keys: %w", err))
	}
	if foreignKeys != 1 {
		return closeWith(fmt.Errorf("database foreign keys could not be enabled"))
	}
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return closeWith(fmt.Errorf("verify database journal mode: %w", err))
	}
	if !strings.EqualFold(journalMode, "wal") {
		return closeWith(fmt.Errorf("database WAL mode could not be enabled"))
	}
	return db, nil
}
