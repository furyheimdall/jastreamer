package downloads

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
)

const (
	artifactRetention                 = 24 * time.Hour
	defaultPreparationLifetime        = 24 * time.Hour
	cleanupInterval                   = 15 * time.Minute
	maximumConcurrentPrepares         = 2
	maximumActiveJobsPerOwner         = 16
	maximumRetainedJobsPerOwner       = 256
	maximumQueuedJobs                 = 128
	maximumArtifactBytes        int64 = 10 << 30
	preparationTimeout                = 2 * time.Hour
	ffmpegProbeTimeout                = 3 * time.Second
)

const schema = `
CREATE TABLE IF NOT EXISTS download_jobs (
  id TEXT PRIMARY KEY,
  owner_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('track','album','playlist')),
  source_id TEXT NOT NULL,
  title TEXT NOT NULL,
  quality TEXT NOT NULL CHECK(quality IN ('original','aac_256')),
  status TEXT NOT NULL CHECK(status IN ('preparing','ready','partial','failed','cancelled')),
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  error_code TEXT NOT NULL,
  error_message TEXT NOT NULL
) STRICT;
CREATE INDEX IF NOT EXISTS download_jobs_owner ON download_jobs(owner_id,created_at DESC);
CREATE INDEX IF NOT EXISTS download_jobs_expiry ON download_jobs(expires_at);
CREATE TABLE IF NOT EXISTS download_job_tracks (
  job_id TEXT NOT NULL REFERENCES download_jobs(id) ON DELETE CASCADE,
  position INTEGER NOT NULL CHECK(position>=0),
  track_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('pending','preparing','ready','failed')),
  title TEXT NOT NULL,
  artist TEXT NOT NULL,
  album TEXT NOT NULL,
  album_artist TEXT NOT NULL,
  disc INTEGER NOT NULL CHECK(disc>=0),
  track_number INTEGER NOT NULL CHECK(track_number>=0),
  duration_ms INTEGER NOT NULL CHECK(duration_ms>=0),
  source_version TEXT NOT NULL,
  quality TEXT NOT NULL CHECK(quality IN ('original','aac_256')),
  mime TEXT NOT NULL,
  codec TEXT NOT NULL,
  byte_size INTEGER NOT NULL CHECK(byte_size>=0),
  sha256 TEXT NOT NULL,
  media_path TEXT NOT NULL,
  artwork_path TEXT NOT NULL,
  error_code TEXT NOT NULL,
  error_message TEXT NOT NULL,
  artifact_name TEXT NOT NULL,
  PRIMARY KEY(job_id,position)
) STRICT;
CREATE INDEX IF NOT EXISTS download_job_tracks_artifact ON download_job_tracks(job_id,artifact_name);
`

type catalog interface {
	DownloadSnapshot(context.Context, string, string) (library.DownloadSnapshot, error)
	Open(context.Context, string) (*os.File, library.Track, error)
	Info(context.Context, string) (library.TrackInfo, error)
}

type Service struct {
	db                  *sql.DB
	catalog             catalog
	directory           string
	directoryInfo       os.FileInfo
	ffmpegPath          string
	aacEnabled          bool
	ctx                 context.Context
	cancel              context.CancelFunc
	queue               chan string
	admissions          chan struct{}
	preparationLifetime time.Duration
	workers             sync.WaitGroup
	admissionMu         sync.Mutex
	operationMu         sync.Mutex
	cancelMu            sync.Mutex
	cancels             map[string]context.CancelFunc
}

func New(parent context.Context, db *sql.DB, catalog catalog, directory, ffmpegPath string) (*Service, error) {
	if db == nil || catalog == nil {
		return nil, errors.New("downloads: database and library are required")
	}
	if !filepath.IsAbs(directory) {
		return nil, errors.New("downloads: artifact directory must be absolute")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create download artifact directory: %w", err)
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("downloads: artifact directory must be a real directory")
	}
	if _, err := db.ExecContext(parent, schema); err != nil {
		return nil, fmt.Errorf("initialize download schema: %w", err)
	}
	ctx, cancel := context.WithCancel(parent)
	service := &Service{
		db: db, catalog: catalog, directory: directory, directoryInfo: directoryInfo, ffmpegPath: ffmpegPath,
		ctx: ctx, cancel: cancel, queue: make(chan string, maximumQueuedJobs),
		admissions: make(chan struct{}, maximumQueuedJobs), preparationLifetime: defaultPreparationLifetime,
		cancels: make(map[string]context.CancelFunc),
	}
	service.aacEnabled = probeAAC(ctx, ffmpegPath)
	if err := service.recoverInterrupted(parent); err != nil {
		cancel()
		return nil, err
	}
	if err := service.cleanup(parent); err != nil {
		cancel()
		return nil, err
	}
	for range maximumConcurrentPrepares {
		service.workers.Add(1)
		go service.runWorker()
	}
	service.workers.Add(1)
	go service.runCleanup()
	return service, nil
}

func (service *Service) Close() {
	service.cancel()
	service.cancelMu.Lock()
	for _, cancel := range service.cancels {
		cancel()
	}
	service.cancelMu.Unlock()
	service.workers.Wait()
}

func (service *Service) Capabilities() Capabilities {
	qualities := []string{QualityOriginal}
	if service.aacEnabled {
		qualities = append(qualities, QualityAAC256)
	}
	return Capabilities{Version: 1, Qualities: qualities, MaxTracks: library.MaximumDownloadTracks}
}

func (service *Service) Create(ctx context.Context, ownerID string, request Request) (Manifest, error) {
	if strings.TrimSpace(ownerID) == "" {
		return Manifest{}, fault.New(http.StatusUnauthorized, "AUTH_REQUIRED", "Authentication is required.")
	}
	request.Kind = strings.TrimSpace(request.Kind)
	request.ID = strings.TrimSpace(request.ID)
	request.Quality = strings.TrimSpace(request.Quality)
	if request.ID == "" || len(request.ID) > 256 {
		return Manifest{}, fault.New(http.StatusBadRequest, "INVALID_REQUEST", "A valid download source id is required.")
	}
	if request.Quality != QualityOriginal && request.Quality != QualityAAC256 {
		return Manifest{}, fault.New(http.StatusBadRequest, "UNSUPPORTED_QUALITY", "The requested download quality is not supported.")
	}
	if request.Quality == QualityAAC256 && !service.aacEnabled {
		return Manifest{}, fault.New(http.StatusConflict, "QUALITY_UNAVAILABLE", "AAC download preparation is not available on this server.")
	}
	snapshot, err := service.catalog.DownloadSnapshot(ctx, request.Kind, request.ID)
	if err != nil {
		return Manifest{}, err
	}
	if len(snapshot.Tracks) == 0 || len(snapshot.Tracks) > library.MaximumDownloadTracks {
		return Manifest{}, fault.New(http.StatusBadRequest, "INVALID_COLLECTION", "The download collection is empty or too large.")
	}
	id, err := randomID()
	if err != nil {
		return Manifest{}, err
	}
	now := time.Now().UTC()
	expires := now.Add(service.preparationLifetime)
	status := "preparing"
	pending := 0
	tracks := make([]Track, 0, len(snapshot.Tracks))
	for index, item := range snapshot.Tracks {
		track := Track{
			Index: index, TrackID: item.ID, Status: "pending", Title: item.Title,
			Artist: item.Artist, Album: item.Album, AlbumArtist: item.AlbumArtist,
			Disc: item.Disc, Track: item.Track, DurationMS: item.DurationMS,
			SourceVersion: sourceVersion(item), Quality: request.Quality,
		}
		if item.ArtworkID != "" {
			track.ArtworkPath = "/api/v1/artwork/" + item.ArtworkID
		}
		if item.ID == "" || !item.Available {
			track.Status = "failed"
			track.SourceVersion = ""
			track.Error = &Error{Code: "SOURCE_UNAVAILABLE", Message: "The source track is unavailable."}
		} else if snapshot.IntegrityFailures[item.ID] {
			track.Status = "failed"
			track.Error = &Error{Code: "SOURCE_INTEGRITY_FAILED", Message: "The source track has a known integrity failure at this version."}
		} else {
			pending++
		}
		tracks = append(tracks, track)
	}
	if pending == 0 {
		status = "failed"
		expires = now.Add(artifactRetention)
	}
	service.admissionMu.Lock()
	defer service.admissionMu.Unlock()
	var active, retained int
	if err := service.db.QueryRowContext(ctx, `SELECT COALESCE(sum(status='preparing'),0),COALESCE(sum(CASE WHEN status='preparing' OR expires_at>? THEN 1 ELSE 0 END),0) FROM download_jobs WHERE owner_id=?`, now.Format(time.RFC3339Nano), ownerID).Scan(&active, &retained); err != nil {
		return Manifest{}, fmt.Errorf("count retained downloads: %w", err)
	}
	if active >= maximumActiveJobsPerOwner || retained >= maximumRetainedJobsPerOwner {
		return Manifest{}, fault.New(http.StatusTooManyRequests, "DOWNLOAD_BUSY", "Too many downloads are already being prepared or retained.")
	}
	reserved := false
	if status == "preparing" {
		select {
		case service.admissions <- struct{}{}:
			reserved = true
		default:
			return Manifest{}, fault.New(http.StatusTooManyRequests, "DOWNLOAD_BUSY", "The server download preparation queue is full.")
		}
	}
	defer func() {
		if reserved {
			<-service.admissions
		}
	}()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return Manifest{}, fmt.Errorf("begin download job: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO download_jobs(id,owner_id,kind,source_id,title,quality,status,created_at,expires_at,error_code,error_message) VALUES(?,?,?,?,?,?,?,?,?,'','')`, id, ownerID, request.Kind, request.ID, snapshot.Title, request.Quality, status, now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano)); err != nil {
		return Manifest{}, fmt.Errorf("create download job: %w", err)
	}
	for _, track := range tracks {
		code, message := "", ""
		if track.Error != nil {
			code, message = track.Error.Code, track.Error.Message
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO download_job_tracks(job_id,position,track_id,status,title,artist,album,album_artist,disc,track_number,duration_ms,source_version,quality,mime,codec,byte_size,sha256,media_path,artwork_path,error_code,error_message,artifact_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,'','',0,'','',?,?,?,'')`, id, track.Index, track.TrackID, track.Status, track.Title, track.Artist, track.Album, track.AlbumArtist, track.Disc, track.Track, track.DurationMS, track.SourceVersion, track.Quality, track.ArtworkPath, code, message); err != nil {
			return Manifest{}, fmt.Errorf("create download track: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return Manifest{}, fmt.Errorf("commit download job: %w", err)
	}
	manifest := Manifest{ID: id, Kind: request.Kind, Title: snapshot.Title, Quality: request.Quality, Status: status, ExpiresAt: expires.Format(time.RFC3339Nano), Tracks: tracks}
	if status == "preparing" {
		select {
		case service.queue <- id:
			reserved = false
		case <-ctx.Done():
			_ = service.failQueuedJob(context.Background(), id, "JOB_INTERRUPTED", "Download preparation was interrupted.")
			return Manifest{}, context.Cause(ctx)
		case <-service.ctx.Done():
			_ = service.failQueuedJob(context.Background(), id, "JOB_INTERRUPTED", "Download preparation was interrupted.")
			return Manifest{}, fault.New(http.StatusServiceUnavailable, "DOWNLOAD_UNAVAILABLE", "Download preparation is shutting down.")
		}
	}
	return manifest, nil
}

func (service *Service) Manifest(ctx context.Context, ownerID, id string) (Manifest, error) {
	manifest, err := service.loadManifest(ctx, ownerID, id)
	if err != nil {
		return Manifest{}, err
	}
	if expired(manifest.ExpiresAt) {
		if manifest.Status == "preparing" {
			if err := service.terminatePreparation(ctx, id, "JOB_EXPIRED", "Download preparation reached its deadline."); err != nil {
				return Manifest{}, err
			}
			return service.loadManifest(ctx, ownerID, id)
		}
		service.operationMu.Lock()
		_ = service.releaseArtifacts(ctx, id)
		service.operationMu.Unlock()
		return Manifest{}, fault.New(http.StatusGone, "DOWNLOAD_EXPIRED", "The download job has expired.")
	}
	return manifest, nil
}

func (service *Service) Cancel(ctx context.Context, ownerID, id string) error {
	if _, err := service.loadManifest(ctx, ownerID, id); err != nil {
		return err
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin download cancellation: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE download_jobs SET status='cancelled',error_code='',error_message='',expires_at=? WHERE id=? AND owner_id=?`, time.Now().UTC().Add(artifactRetention).Format(time.RFC3339Nano), id, ownerID)
	if err != nil {
		return fmt.Errorf("cancel download job: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return fault.New(http.StatusNotFound, "DOWNLOAD_NOT_FOUND", "The download job was not found.")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE download_job_tracks SET status='failed',mime='',codec='',byte_size=0,sha256='',media_path='',artifact_name='',error_code='CANCELLED',error_message='The download job was cancelled and released.' WHERE job_id=?`, id); err != nil {
		return fmt.Errorf("release download tracks: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit download cancellation: %w", err)
	}
	service.cancelMu.Lock()
	if cancel := service.cancels[id]; cancel != nil {
		cancel()
	}
	service.cancelMu.Unlock()
	return service.removeJobDirectory(id)
}

func (service *Service) Artifact(ctx context.Context, ownerID, id string, index int) (Artifact, error) {
	if index < 0 {
		return Artifact{}, fault.New(http.StatusNotFound, "DOWNLOAD_FILE_NOT_FOUND", "The download file was not found.")
	}
	var status, trackStatus, name, mime, digest string
	var size int64
	var expiresAt string
	err := service.db.QueryRowContext(ctx, `SELECT jobs.status,jobs.expires_at,tracks.status,tracks.artifact_name,tracks.mime,tracks.byte_size,tracks.sha256 FROM download_jobs AS jobs JOIN download_job_tracks AS tracks ON tracks.job_id=jobs.id WHERE jobs.id=? AND jobs.owner_id=? AND tracks.position=?`, id, ownerID, index).Scan(&status, &expiresAt, &trackStatus, &name, &mime, &size, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, fault.New(http.StatusNotFound, "DOWNLOAD_FILE_NOT_FOUND", "The download file was not found.")
	}
	if err != nil {
		return Artifact{}, fmt.Errorf("load download artifact: %w", err)
	}
	if status == "cancelled" {
		return Artifact{}, fault.New(http.StatusGone, "DOWNLOAD_EXPIRED", "The download file is no longer available.")
	}
	if expired(expiresAt) {
		if status == "preparing" {
			if err := service.terminatePreparation(ctx, id, "JOB_EXPIRED", "Download preparation reached its deadline."); err != nil {
				return Artifact{}, err
			}
			return service.Artifact(ctx, ownerID, id, index)
		}
		return Artifact{}, fault.New(http.StatusGone, "DOWNLOAD_EXPIRED", "The download file is no longer available.")
	}
	if trackStatus != "ready" || name == "" {
		return Artifact{}, fault.New(http.StatusConflict, "DOWNLOAD_NOT_READY", "The download file is not ready.")
	}
	path, err := service.artifactPath(id, name)
	if err != nil {
		return Artifact{}, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != size {
		return Artifact{}, fault.New(http.StatusGone, "DOWNLOAD_FILE_MISSING", "The prepared download file is missing or changed.")
	}
	if len(digest) != 64 {
		return Artifact{}, fault.New(http.StatusGone, "DOWNLOAD_FILE_INVALID", "The prepared download file failed integrity validation.")
	}
	file, err := os.Open(path)
	if err != nil {
		return Artifact{}, fault.New(http.StatusGone, "DOWNLOAD_FILE_MISSING", "The prepared download file is missing.")
	}
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() != size || !os.SameFile(info, opened) {
		file.Close()
		return Artifact{}, fault.New(http.StatusGone, "DOWNLOAD_FILE_MISSING", "The prepared download file is missing or changed.")
	}
	return Artifact{File: file, FileName: path, Mime: mime, Size: size, SHA256: digest, ModTime: opened.ModTime()}, nil
}

func (service *Service) loadManifest(ctx context.Context, ownerID, id string) (Manifest, error) {
	var manifest Manifest
	var errorCode, errorMessage string
	err := service.db.QueryRowContext(ctx, `SELECT id,kind,title,quality,status,expires_at,error_code,error_message FROM download_jobs WHERE id=? AND owner_id=?`, id, ownerID).Scan(&manifest.ID, &manifest.Kind, &manifest.Title, &manifest.Quality, &manifest.Status, &manifest.ExpiresAt, &errorCode, &errorMessage)
	if errors.Is(err, sql.ErrNoRows) {
		return Manifest{}, fault.New(http.StatusNotFound, "DOWNLOAD_NOT_FOUND", "The download job was not found.")
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("load download job: %w", err)
	}
	if errorCode != "" {
		manifest.Error = &Error{Code: errorCode, Message: errorMessage}
	}
	rows, err := service.db.QueryContext(ctx, `SELECT position,track_id,status,title,artist,album,album_artist,disc,track_number,duration_ms,source_version,quality,mime,codec,byte_size,sha256,media_path,artwork_path,error_code,error_message,artifact_name FROM download_job_tracks WHERE job_id=? ORDER BY position`, id)
	if err != nil {
		return Manifest{}, fmt.Errorf("load download tracks: %w", err)
	}
	manifest.Tracks = []Track{}
	for rows.Next() {
		var track Track
		var code, message string
		if err = rows.Scan(&track.Index, &track.TrackID, &track.Status, &track.Title, &track.Artist, &track.Album, &track.AlbumArtist, &track.Disc, &track.Track, &track.DurationMS, &track.SourceVersion, &track.Quality, &track.Mime, &track.Codec, &track.ByteSize, &track.SHA256, &track.MediaPath, &track.ArtworkPath, &code, &message, &track.artifactName); err != nil {
			rows.Close()
			return Manifest{}, fmt.Errorf("read download track: %w", err)
		}
		if code != "" {
			track.Error = &Error{Code: code, Message: message}
		}
		manifest.Tracks = append(manifest.Tracks, track)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Manifest{}, fmt.Errorf("read download tracks: %w", err)
	}
	if err = rows.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close download tracks: %w", err)
	}
	return manifest, nil
}

func (service *Service) recoverInterrupted(ctx context.Context) error {
	now := time.Now().UTC().Add(artifactRetention).Format(time.RFC3339Nano)
	if _, err := service.db.ExecContext(ctx, `UPDATE download_job_tracks SET status='failed',error_code='JOB_INTERRUPTED',error_message='Download preparation was interrupted by a server restart.' WHERE status IN ('pending','preparing') AND job_id IN (SELECT id FROM download_jobs WHERE status='preparing')`); err != nil {
		return fmt.Errorf("recover interrupted download tracks: %w", err)
	}
	rows, err := service.db.QueryContext(ctx, `SELECT id FROM download_jobs WHERE status='preparing'`)
	if err != nil {
		return fmt.Errorf("load interrupted download jobs: %w", err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("read interrupted download job: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read interrupted download jobs: %w", err)
	}
	rows.Close()
	for _, id := range ids {
		if err := service.finishJob(ctx, id, now); err != nil {
			return err
		}
	}
	return nil
}

func (service *Service) failQueuedJob(ctx context.Context, id, code, message string) error {
	return service.terminatePreparation(ctx, id, code, message)
}

func (service *Service) terminatePreparation(ctx context.Context, id, code, message string) error {
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin download termination: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE download_job_tracks SET status='failed',error_code=?,error_message=? WHERE job_id=? AND status IN ('pending','preparing') AND EXISTS(SELECT 1 FROM download_jobs WHERE id=? AND status='preparing')`, code, message, id, id); err != nil {
		return fmt.Errorf("terminate download tracks: %w", err)
	}
	var ready, failed int
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(sum(status='ready'),0),COALESCE(sum(status='failed'),0) FROM download_job_tracks WHERE job_id=?`, id).Scan(&ready, &failed); err != nil {
		return fmt.Errorf("summarize terminated download: %w", err)
	}
	status := "failed"
	jobCode, jobMessage := code, message
	if ready > 0 && failed > 0 {
		status, jobCode, jobMessage = "partial", "", ""
	} else if ready > 0 {
		status, jobCode, jobMessage = "ready", "", ""
	}
	if _, err = tx.ExecContext(ctx, `UPDATE download_jobs SET status=?,expires_at=?,error_code=?,error_message=? WHERE id=? AND status='preparing'`, status, time.Now().UTC().Add(artifactRetention).Format(time.RFC3339Nano), jobCode, jobMessage, id); err != nil {
		return fmt.Errorf("terminate download job: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit download termination: %w", err)
	}
	service.cancelMu.Lock()
	if cancel := service.cancels[id]; cancel != nil {
		cancel()
	}
	service.cancelMu.Unlock()
	return nil
}

func (service *Service) finishJob(ctx context.Context, id, expiresAt string) error {
	var ready, failed int
	if err := service.db.QueryRowContext(ctx, `SELECT COALESCE(sum(status='ready'),0),COALESCE(sum(status='failed'),0) FROM download_job_tracks WHERE job_id=?`, id).Scan(&ready, &failed); err != nil {
		return fmt.Errorf("summarize download job: %w", err)
	}
	status := "failed"
	code, message := "PREPARATION_FAILED", "No tracks could be prepared."
	if ready > 0 && failed > 0 {
		status, code, message = "partial", "", ""
	} else if ready > 0 {
		status, code, message = "ready", "", ""
	}
	if _, err := service.db.ExecContext(ctx, `UPDATE download_jobs SET status=?,expires_at=?,error_code=?,error_message=? WHERE id=? AND status='preparing'`, status, expiresAt, code, message, id); err != nil {
		return fmt.Errorf("finish download job: %w", err)
	}
	return nil
}

func (service *Service) runCleanup() {
	defer service.workers.Done()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-service.ctx.Done():
			return
		case <-ticker.C:
			_ = service.cleanup(service.ctx)
		}
	}
}

func (service *Service) cleanup(ctx context.Context) error {
	rows, err := service.db.QueryContext(ctx, `SELECT id,status FROM download_jobs WHERE expires_at<=? ORDER BY expires_at`, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("list expired downloads: %w", err)
	}
	type expiredJob struct {
		id     string
		status string
	}
	jobs := []expiredJob{}
	for rows.Next() {
		var job expiredJob
		if err := rows.Scan(&job.id, &job.status); err != nil {
			rows.Close()
			return fmt.Errorf("read expired download: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read expired downloads: %w", err)
	}
	rows.Close()
	for _, job := range jobs {
		if job.status == "preparing" {
			if err := service.terminatePreparation(ctx, job.id, "JOB_EXPIRED", "Download preparation reached its deadline."); err != nil {
				return err
			}
			continue
		}
		service.operationMu.Lock()
		err := service.removeJobDirectory(job.id)
		if err == nil {
			_, err = service.db.ExecContext(ctx, `DELETE FROM download_jobs WHERE id=? AND status<>'preparing'`, job.id)
		}
		service.operationMu.Unlock()
		if err != nil {
			return fmt.Errorf("delete expired download: %w", err)
		}
	}
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	return service.cleanupOrphans(ctx)
}
func (service *Service) cleanupOrphans(ctx context.Context) error {
	currentDirectory, err := os.Lstat(service.directory)
	if err != nil || !currentDirectory.IsDir() || currentDirectory.Mode()&os.ModeSymlink != 0 || !os.SameFile(service.directoryInfo, currentDirectory) {
		return errors.New("downloads: artifact directory changed or became unsafe")
	}
	rows, err := service.db.QueryContext(ctx, `SELECT jobs.id,jobs.status,tracks.artifact_name FROM download_jobs AS jobs LEFT JOIN download_job_tracks AS tracks ON tracks.job_id=jobs.id`)
	if err != nil {
		return fmt.Errorf("list retained download artifacts: %w", err)
	}
	statuses := make(map[string]string)
	retained := make(map[string]map[string]bool)
	for rows.Next() {
		var id, status string
		var name sql.NullString
		if err := rows.Scan(&id, &status, &name); err != nil {
			rows.Close()
			return fmt.Errorf("read retained download artifact: %w", err)
		}
		statuses[id] = status
		if retained[id] == nil {
			retained[id] = make(map[string]bool)
		}
		if name.Valid && name.String != "" {
			retained[id][name.String] = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read retained download artifacts: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close retained download artifacts: %w", err)
	}
	entries, err := os.ReadDir(service.directory)
	if err != nil {
		return fmt.Errorf("list download artifact directory: %w", err)
	}
	for _, entry := range entries {
		id := entry.Name()
		status, exists := statuses[id]
		path := filepath.Join(service.directory, id)
		if !exists || !validID(id) {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove orphan download artifact: %w", err)
			}
			continue
		}
		if status == "preparing" {
			continue
		}
		if !entry.IsDir() {
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("remove invalid download artifact directory: %w", err)
			}
			continue
		}
		files, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("list retained job artifacts: %w", err)
		}
		for _, file := range files {
			if retained[id][file.Name()] {
				continue
			}
			if err := os.RemoveAll(filepath.Join(path, file.Name())); err != nil {
				return fmt.Errorf("remove uncommitted download artifact: %w", err)
			}
		}
	}
	return nil
}

func (service *Service) releaseArtifacts(ctx context.Context, id string) error {
	if err := service.removeJobDirectory(id); err != nil {
		return err
	}
	_, err := service.db.ExecContext(ctx, `DELETE FROM download_jobs WHERE id=? AND status<>'preparing'`, id)
	return err
}

func (service *Service) removeJobDirectory(id string) error {
	path, err := service.jobDirectory(id)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove download artifacts: %w", err)
	}
	return nil
}

func (service *Service) jobDirectory(id string) (string, error) {
	if !validID(id) {
		return "", fault.New(http.StatusNotFound, "DOWNLOAD_NOT_FOUND", "The download job was not found.")
	}
	current, err := os.Lstat(service.directory)
	if err != nil || !current.IsDir() || current.Mode()&os.ModeSymlink != 0 || !os.SameFile(service.directoryInfo, current) {
		return "", errors.New("downloads: artifact directory changed or became unsafe")
	}
	return filepath.Join(service.directory, id), nil
}

func (service *Service) artifactPath(id, name string) (string, error) {
	if name == "" || filepath.Base(name) != name || strings.ContainsAny(name, "/\\\x00") {
		return "", fault.New(http.StatusNotFound, "DOWNLOAD_FILE_NOT_FOUND", "The download file was not found.")
	}
	directory, err := service.jobDirectory(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, name), nil
}

func validID(value string) bool {
	if len(value) != 32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("create download id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func sourceVersion(track library.Track) string {
	hash := sha256.New()
	_, _ = io.WriteString(hash, track.ID)
	_, _ = io.WriteString(hash, "\x00"+strconv.FormatInt(track.Size, 10)+"\x00"+track.ModifiedAt)
	return hex.EncodeToString(hash.Sum(nil))
}

func expired(raw string) bool {
	value, err := time.Parse(time.RFC3339Nano, raw)
	return err != nil || !time.Now().UTC().Before(value)
}

func probeAAC(parent context.Context, path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return false
	}
	check := func(argument, token string) bool {
		ctx, cancel := context.WithTimeout(parent, ffmpegProbeTimeout)
		defer cancel()
		var output bytes.Buffer
		command := exec.CommandContext(ctx, path, "-hide_banner", argument)
		command.Stdout = &limitedWriter{writer: &output, remaining: 2 << 20}
		command.Stderr = io.Discard
		if err := command.Run(); err != nil {
			return false
		}
		for _, line := range strings.Split(output.String(), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 && fields[1] == token {
				return true
			}
		}
		return false
	}
	return check("-encoders", "aac") && check("-muxers", "ipod") && check("-filters", "aformat")
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (writer *limitedWriter) Write(value []byte) (int, error) {
	original := len(value)
	if writer.remaining > 0 {
		allowed := int64(len(value))
		if allowed > writer.remaining {
			allowed = writer.remaining
		}
		_, _ = writer.writer.Write(value[:allowed])
		writer.remaining -= allowed
	}
	return original, nil
}
