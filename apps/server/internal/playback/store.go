package playback

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

type Store struct {
	mu         sync.Mutex
	db         *sqliteDB
	clock      Clock
	commitHook func(commitStage) error
	closed     bool
	version    int
}

func Open(ctx context.Context, config Config) (*Store, error) {
	if !isLocalPath(config.Path) || !isLocalPath(config.BackupDirectory) {
		return nil, ErrNonLocalDatabase
	}
	db, err := openSQLite(config.Path)
	if err != nil {
		return nil, err
	}
	if err := integrityCheck(db); err != nil {
		return nil, errors.Join(err, db.close())
	}
	version, err := db.versionNumber()
	if err != nil {
		return nil, errors.Join(err, db.close())
	}
	if err := validateJournalVersion(config.JournalMode, version); err != nil {
		return nil, errors.Join(err, db.close())
	}
	if err := (migrationRun{db: db, config: config}).apply(ctx); err != nil {
		return nil, errors.Join(err, db.close())
	}
	if err := db.exec("PRAGMA foreign_keys = ON"); err != nil {
		return nil, errors.Join(err, db.close())
	}
	journal := "PRAGMA journal_mode = DELETE"
	if config.JournalMode == JournalWAL {
		journal = "PRAGMA journal_mode = WAL"
	}
	if err := db.exec(journal); err != nil {
		return nil, errors.Join(err, db.close())
	}
	clock := config.Clock
	if clock == nil {
		clock = playbackClock{}
	}
	store := &Store{db: db, clock: clock, version: version}
	if err := store.recoverRendererSessions(ctx); err != nil {
		return nil, errors.Join(err, db.close())
	}
	return store, nil
}

func validateJournalVersion(mode JournalMode, version int) error {
	if mode == JournalWAL && version < 3_051_003 {
		return ErrUnsafeWAL
	}
	return nil
}

func (store *Store) SQLiteVersion() int {
	return store.version
}

func isLocalPath(path string) bool {
	return path != "" &&
		!strings.Contains(path, "://") &&
		!strings.HasPrefix(path, "file:") &&
		!strings.HasPrefix(path, "//") &&
		!strings.HasPrefix(path, `\\`) &&
		!strings.ContainsRune(path, '\x00')
}

func (store *Store) Close() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	return store.db.close()
}

func (store *Store) transaction(ctx context.Context, operation func(*sqliteDB) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrClosed
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if err := store.db.exec("BEGIN IMMEDIATE"); err != nil {
		return err
	}
	if err := errors.Join(operation(store.db), store.db.takePending()); err != nil {
		if rollbackErr := store.db.exec("ROLLBACK"); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	if err := store.db.exec("COMMIT"); err != nil {
		return errors.Join(err, store.db.exec("ROLLBACK"))
	}
	return nil
}

func (store *Store) read(ctx context.Context, operation func(*sqliteDB) error) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return ErrClosed
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	return errors.Join(operation(store.db), store.db.takePending())
}

func ensureZone(db *sqliteDB, zoneID ZoneID) error {
	stmt, err := db.prepare("SELECT 1 FROM playback_idempotency WHERE zone_id=? AND operation='delete_zone' LIMIT 1")
	if err != nil {
		return err
	}
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		stmt.close()
		return err
	}
	deleted, err := stmt.step()
	stmt.close()
	if err != nil {
		return err
	}
	if deleted {
		return ErrZoneNotFound
	}
	if err := execBound(db, "INSERT OR IGNORE INTO playback_zones(zone_id) VALUES (?)", func(stmt *sqliteStmt) error {
		return stmt.bindText(1, string(zoneID))
	}); err != nil {
		return err
	}
	return execBound(db, "INSERT OR IGNORE INTO playback_continuation_policies(zone_id) VALUES (?)", func(stmt *sqliteStmt) error {
		return stmt.bindText(1, string(zoneID))
	})
}

type zoneRecord struct {
	revision         Revision
	transport        Transport
	sessionID        SessionID
	seed             string
	decisionSequence int64
	queueSequence    int64
	currentPlay      PlayID
}

func loadZone(db *sqliteDB, zoneID ZoneID) (zoneRecord, error) {
	stmt, err := db.prepare("SELECT revision, transport, session_id, session_seed, decision_sequence, queue_sequence, current_play_id FROM playback_zones WHERE zone_id = ?")
	if err != nil {
		return zoneRecord{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return zoneRecord{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return zoneRecord{}, err
	}
	if !row {
		return zoneRecord{}, fmt.Errorf("zone %q does not exist", zoneID)
	}
	record := zoneRecord{revision: Revision(stmt.int64(0)), transport: Transport(stmt.text(1)), decisionSequence: stmt.int64(4), queueSequence: stmt.int64(5)}
	if !stmt.isNull(2) {
		record.sessionID = SessionID(stmt.text(2))
	}
	if !stmt.isNull(3) {
		record.seed = stmt.text(3)
	}
	if !stmt.isNull(6) {
		record.currentPlay = PlayID(stmt.text(6))
	}
	return record, nil
}

func (store *Store) Backup(ctx context.Context, path string) error {
	if !isLocalPath(path) {
		return ErrNonLocalDatabase
	}
	return store.read(ctx, func(db *sqliteDB) error {
		if err := integrityCheck(db); err != nil {
			return err
		}
		if err := sqliteBackup(db, path); err != nil {
			return fmt.Errorf("online backup: %w", err)
		}
		backup, err := openSQLite(path)
		if err != nil {
			return err
		}
		checkErr := integrityCheck(backup)
		return errors.Join(checkErr, backup.close())
	})
}
