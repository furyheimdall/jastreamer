package library

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
)

const schema = `
CREATE TABLE IF NOT EXISTS library_roots (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  path TEXT NOT NULL,
  configured INTEGER NOT NULL CHECK(configured IN (0,1)),
  updated_at TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX IF NOT EXISTS library_roots_configured_path ON library_roots(path) WHERE configured=1;
CREATE TABLE IF NOT EXISTS library_artwork (
  id TEXT PRIMARY KEY,
  mime TEXT NOT NULL,
  cache_path TEXT NOT NULL,
  created_at TEXT NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS library_tracks (
  id TEXT PRIMARY KEY,
  root_id TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  title TEXT NOT NULL,
  artist TEXT NOT NULL,
  album TEXT NOT NULL,
  album_artist TEXT NOT NULL,
  album_id TEXT NOT NULL,
  disc INTEGER NOT NULL CHECK(disc>=0),
  track_number INTEGER NOT NULL CHECK(track_number>=0),
  genres_json TEXT NOT NULL,
  duration_ms INTEGER NOT NULL CHECK(duration_ms>=0),
  format TEXT NOT NULL,
  mime TEXT NOT NULL,
  artwork_id TEXT NOT NULL,
  byte_size INTEGER NOT NULL CHECK(byte_size>=0),
  modified_ns INTEGER NOT NULL,
  modified_at TEXT NOT NULL,
  available INTEGER NOT NULL CHECK(available IN (0,1)),
  last_seen_scan TEXT NOT NULL,
  UNIQUE(root_id, relative_path)
) STRICT;
CREATE INDEX IF NOT EXISTS library_tracks_available_album ON library_tracks(available,album_id,disc,track_number,title,id);
CREATE INDEX IF NOT EXISTS library_tracks_available_artist ON library_tracks(available,artist,title,id);
CREATE INDEX IF NOT EXISTS library_tracks_root_path ON library_tracks(root_id,relative_path,available);
CREATE TABLE IF NOT EXISTS library_scan_jobs (
  id TEXT PRIMARY KEY,
  status TEXT NOT NULL CHECK(status IN ('queued','running','complete','failed','cancelled')),
  discovered INTEGER NOT NULL CHECK(discovered>=0),
  processed INTEGER NOT NULL CHECK(processed>=0),
  added INTEGER NOT NULL CHECK(added>=0),
  updated INTEGER NOT NULL CHECK(updated>=0),
  unavailable INTEGER NOT NULL CHECK(unavailable>=0),
  errors INTEGER NOT NULL CHECK(errors>=0),
  started_at TEXT NOT NULL,
  finished_at TEXT NOT NULL,
  error TEXT NOT NULL
) STRICT;
CREATE INDEX IF NOT EXISTS library_scan_jobs_started ON library_scan_jobs(started_at DESC,id DESC);
CREATE TABLE IF NOT EXISTS library_playlists (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  revision INTEGER NOT NULL CHECK(revision>0),
  updated_at TEXT NOT NULL
) STRICT;
CREATE TABLE IF NOT EXISTS library_playlist_items (
  playlist_id TEXT NOT NULL REFERENCES library_playlists(id) ON DELETE CASCADE,
  position INTEGER NOT NULL CHECK(position>=0),
  track_id TEXT NOT NULL,
  PRIMARY KEY(playlist_id,position)
) STRICT;
CREATE INDEX IF NOT EXISTS library_playlist_track ON library_playlist_items(track_id);
`

type Service struct {
	db       *sql.DB
	cacheDir string
	notify   func(string)
	ctx      context.Context

	rootsMu sync.RWMutex
	roots   map[string]Root

	scanMu  sync.Mutex
	cancels map[string]context.CancelFunc
}

func New(ctx context.Context, db *sql.DB, roots []Root, cacheDir string, notify func(string)) (*Service, error) {
	if db == nil {
		return nil, errors.New("library: nil database")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if cacheDir == "" || !filepath.IsAbs(cacheDir) {
		return nil, errors.New("library: artwork cache directory must be absolute")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("create library artwork cache: %w", err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("initialize library schema: %w", err)
	}
	if notify == nil {
		notify = func(string) {}
	}
	service := &Service{db: db, cacheDir: cacheDir, notify: notify, ctx: ctx, roots: make(map[string]Root), cancels: make(map[string]context.CancelFunc)}
	if _, err := db.ExecContext(ctx, `UPDATE library_scan_jobs SET status='failed',finished_at=?,error='scan interrupted by server restart' WHERE status IN ('queued','running')`, timestamp(time.Now())); err != nil {
		return nil, fmt.Errorf("recover library scan jobs: %w", err)
	}
	if err := service.SetRoots(roots); err != nil {
		return nil, err
	}
	return service, nil
}

func (service *Service) SetRoots(roots []Root) error {
	validated, err := validateRoots(roots)
	if err != nil {
		return err
	}
	service.scanMu.Lock()
	err = service.setRootsLocked(validated)
	service.scanMu.Unlock()
	if err != nil {
		return err
	}
	service.notify("library")
	return nil
}

func (service *Service) setRootsLocked(validated []Root) error {
	var active int
	if err := service.db.QueryRowContext(service.ctx, `SELECT count(*) FROM library_scan_jobs WHERE status IN ('queued','running')`).Scan(&active); err != nil {
		return fmt.Errorf("check active library scan: %w", err)
	}
	if active != 0 {
		return conflict("SCAN_IN_PROGRESS", "library roots cannot change while a scan is running")
	}
	now := timestamp(time.Now())
	tx, err := service.db.BeginTx(service.ctx, nil)
	if err != nil {
		return fmt.Errorf("begin root update: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(service.ctx, `UPDATE library_roots SET configured=0,updated_at=? WHERE configured=1`, now); err != nil {
		return fmt.Errorf("disable library roots: %w", err)
	}
	for _, root := range validated {
		if _, err = tx.ExecContext(service.ctx, `UPDATE library_tracks SET available=0 WHERE root_id=? AND EXISTS (SELECT 1 FROM library_roots WHERE id=? AND path<>?)`, root.ID, root.ID, root.Path); err != nil {
			return fmt.Errorf("invalidate changed library root: %w", err)
		}
		if _, err = tx.ExecContext(service.ctx, `INSERT INTO library_roots(id,name,path,configured,updated_at) VALUES(?,?,?,1,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,path=excluded.path,configured=1,updated_at=excluded.updated_at`, root.ID, root.Name, root.Path, now); err != nil {
			return fmt.Errorf("save library root: %w", err)
		}
	}
	if _, err = tx.ExecContext(service.ctx, `UPDATE library_tracks SET available=0 WHERE root_id IN (SELECT id FROM library_roots WHERE configured=0)`); err != nil {
		return fmt.Errorf("invalidate removed library roots: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit root update: %w", err)
	}
	rootMap := make(map[string]Root, len(validated))
	for _, root := range validated {
		rootMap[root.ID] = root
	}
	service.rootsMu.Lock()
	service.roots = rootMap
	service.rootsMu.Unlock()
	return nil
}

func validateRoots(roots []Root) ([]Root, error) {
	result := make([]Root, 0, len(roots))
	ids := make(map[string]struct{}, len(roots))
	paths := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		root.ID = strings.TrimSpace(root.ID)
		root.Name = strings.TrimSpace(root.Name)
		if root.ID == "" || root.Name == "" || strings.ContainsAny(root.ID, "\x00/\\") {
			return nil, invalid("library root id and name are required")
		}
		if root.Path == "" || !filepath.IsAbs(root.Path) {
			return nil, invalid("library root path must be absolute")
		}
		root.Path = filepath.Clean(root.Path)
		if filepath.Dir(root.Path) == root.Path {
			return nil, invalid("filesystem root cannot be a library root")
		}
		if _, exists := ids[root.ID]; exists {
			return nil, invalid("library root ids must be unique")
		}
		if _, exists := paths[root.Path]; exists {
			return nil, invalid("library root paths must be unique")
		}
		ids[root.ID] = struct{}{}
		paths[root.Path] = struct{}{}
		result = append(result, root)
	}
	return result, nil
}

func (service *Service) rootsSnapshot() []Root {
	service.rootsMu.RLock()
	defer service.rootsMu.RUnlock()
	roots := make([]Root, 0, len(service.roots))
	for _, root := range service.roots {
		roots = append(roots, root)
	}
	return roots
}

func (service *Service) root(id string) (Root, bool) {
	service.rootsMu.RLock()
	defer service.rootsMu.RUnlock()
	root, ok := service.roots[id]
	return root, ok
}

func stableID(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		hash.Write([]byte{0})
		hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return hex.EncodeToString(value[:]), nil
}

func timestamp(value time.Time) string { return value.UTC().Format(time.RFC3339Nano) }

func invalid(message string) error {
	return fault.New(http.StatusBadRequest, "INVALID_REQUEST", message)
}
func notFound(message string) error       { return fault.New(http.StatusNotFound, "NOT_FOUND", message) }
func conflict(code, message string) error { return fault.New(http.StatusConflict, code, message) }
