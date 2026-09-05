package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	modernsqlite "modernc.org/sqlite"
)

const minimumSQLiteVersion = 3_051_003

var (
	ErrInvalidStoreConfig = errors.New("catalog: invalid store configuration")
	ErrSQLiteTooOld       = errors.New("catalog: SQLite version is too old")
)

type StoreConfig struct {
	Path   string
	Root   string
	Schema string
	Now    func() time.Time
}

type Store struct {
	db      *sql.DB
	root    string
	rootID  string
	now     func() time.Time
	version int
}

func OpenStore(ctx context.Context, config StoreConfig) (*Store, error) {
	if config.Path == "" || config.Root == "" || config.Schema == "" || config.Now == nil || strings.HasPrefix(config.Path, "file:") {
		return nil, ErrInvalidStoreConfig
	}
	root, err := canonicalRoot(config.Root)
	if err != nil {
		return nil, fmt.Errorf("catalog store root: %w", err)
	}
	databasePath, err := filepath.Abs(config.Path)
	if err != nil {
		return nil, fmt.Errorf("catalog database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o700); err != nil {
		return nil, fmt.Errorf("create catalog database directory: %w", err)
	}
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open catalog database: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db, root: root, rootID: hashID("root", root), now: config.Now}
	if err := store.initializeWithRetry(ctx, config.Schema); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return store, nil
}

func (store *Store) initializeWithRetry(ctx context.Context, schema string) error {
	deadline := time.Now().Add(5 * time.Second)
	var err error
	for {
		err = store.initialize(ctx, schema)
		if err == nil || !isSQLiteBusy(err) {
			return err
		}
		if !time.Now().Before(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr *modernsqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code() == 5 {
		return true
	}
	return strings.Contains(err.Error(), "database is locked")
}

func (store *Store) initialize(ctx context.Context, schema string) error {
	if _, err := store.db.ExecContext(ctx, "PRAGMA busy_timeout=5000"); err != nil {
		return fmt.Errorf("configure catalog database: %w", err)
	}
	if _, err := store.db.ExecContext(ctx, "PRAGMA foreign_keys=ON; PRAGMA journal_mode=WAL"); err != nil {
		return fmt.Errorf("configure catalog database: %w", err)
	}
	var version string
	if err := store.db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&version); err != nil {
		return fmt.Errorf("read SQLite version: %w", err)
	}
	parsed, err := parseSQLiteVersion(version)
	if err != nil {
		return err
	}
	if parsed < minimumSQLiteVersion {
		return fmt.Errorf("%s: %w", version, ErrSQLiteTooOld)
	}
	store.version = parsed
	var tables int
	if err := store.db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='catalog_roots'").Scan(&tables); err != nil {
		return fmt.Errorf("inspect catalog schema: %w", err)
	}
	if tables == 0 {
		if _, err := store.db.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("apply catalog schema: %w", err)
		}
	}
	if err := store.ensureCompatibleSchema(ctx); err != nil {
		return err
	}
	return nil
}

func (store *Store) ensureCompatibleSchema(ctx context.Context) error {
	if err := store.ensureAnalysisSchema(ctx); err != nil {
		return err
	}
	_, err := store.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS catalog_track_tags (
			track_id TEXT NOT NULL REFERENCES catalog_tracks(track_id) ON DELETE CASCADE,
			tag_type TEXT NOT NULL CHECK (tag_type IN ('genre','style','mood','local')),
			tag_value TEXT NOT NULL CHECK (tag_value<>''),
			PRIMARY KEY (track_id,tag_type,tag_value)
		) STRICT;
		CREATE INDEX IF NOT EXISTS catalog_track_tags_lookup_idx
			ON catalog_track_tags(tag_type,tag_value,track_id)`)
	if err != nil {
		return fmt.Errorf("migrate catalog curation tags: %w", err)
	}
	return nil
}

func parseSQLiteVersion(value string) (int, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return 0, fmt.Errorf("SQLite version %q: %w", value, ErrSQLiteTooOld)
	}
	numbers := [3]int{}
	for index, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return 0, fmt.Errorf("SQLite version %q: %w", value, ErrSQLiteTooOld)
		}
		numbers[index] = number
	}
	return numbers[0]*1_000_000 + numbers[1]*1_000 + numbers[2], nil
}

func (store *Store) SQLiteVersion() int {
	return store.version
}

func (store *Store) IntegrityCheck(ctx context.Context) error {
	var result string
	if err := store.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("catalog integrity check: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("catalog integrity result %q", result)
	}
	return nil
}

func (store *Store) Close() error {
	if err := store.db.Close(); err != nil {
		return fmt.Errorf("close catalog database: %w", err)
	}
	return nil
}

func (store *Store) ensureAnalysisSchema(ctx context.Context) error {
	rows, err := store.db.QueryContext(ctx, "PRAGMA table_info(catalog_analysis)")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var defaultValue any
		if err = rows.Scan(&cid, &name, &kind, &notnull, &defaultValue, &pk); err != nil {
			return errors.Join(err, rows.Close())
		}
		columns[name] = true
	}
	if err = rows.Close(); err != nil {
		return err
	}
	additions := map[string]string{"normalizer_id": "TEXT NOT NULL DEFAULT ''", "normalizer_version": "TEXT NOT NULL DEFAULT ''", "feature_vector": "BLOB NOT NULL DEFAULT X''"}
	for name, definition := range additions {
		if !columns[name] {
			if _, err = store.db.ExecContext(ctx, "ALTER TABLE catalog_analysis ADD COLUMN "+name+" "+definition); err != nil {
				return fmt.Errorf("migrate catalog analysis %s: %w", name, err)
			}
		}
	}
	_, err = store.db.ExecContext(ctx, "DROP INDEX IF EXISTS catalog_analysis_work_idx; CREATE INDEX catalog_analysis_work_idx ON catalog_analysis(status,feature_schema_version,analyzer_id,analyzer_version,normalizer_id,normalizer_version)")
	return err
}
