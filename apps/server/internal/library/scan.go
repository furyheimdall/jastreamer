package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type scanCandidate struct {
	relative   string
	size       int64
	modifiedNS int64
	modifiedAt string
}

func (service *Service) StartScan(ctx context.Context) (ScanJob, error) {
	if err := ctx.Err(); err != nil {
		return ScanJob{}, err
	}
	service.scanMu.Lock()
	defer service.scanMu.Unlock()
	var active int
	if err := service.db.QueryRowContext(ctx, `SELECT count(*) FROM library_scan_jobs WHERE status IN ('queued','running')`).Scan(&active); err != nil {
		return ScanJob{}, fmt.Errorf("check active library scan: %w", err)
	}
	if active != 0 {
		return ScanJob{}, conflict("SCAN_IN_PROGRESS", "a library scan is already running")
	}
	id, err := randomID()
	if err != nil {
		return ScanJob{}, fmt.Errorf("create scan id: %w", err)
	}
	job := ScanJob{ID: id, Status: "queued", StartedAt: timestamp(time.Now()), FinishedAt: "", Error: ""}
	if err := service.insertJob(ctx, job); err != nil {
		return ScanJob{}, err
	}
	scanCtx, cancel := context.WithCancel(service.ctx)
	service.cancels[id] = cancel
	go service.runScan(scanCtx, job, service.rootsSnapshot())
	return job, nil
}

func (service *Service) insertJob(ctx context.Context, job ScanJob) error {
	_, err := service.db.ExecContext(ctx, `INSERT INTO library_scan_jobs(id,status,discovered,processed,added,updated,unavailable,errors,started_at,finished_at,error) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, job.ID, job.Status, job.Discovered, job.Processed, job.Added, job.Updated, job.Unavailable, job.Errors, job.StartedAt, job.FinishedAt, job.Error)
	if err != nil {
		return fmt.Errorf("save library scan: %w", err)
	}
	return nil
}

func (service *Service) updateJob(ctx context.Context, job ScanJob) error {
	_, err := service.db.ExecContext(ctx, `UPDATE library_scan_jobs SET status=?,discovered=?,processed=?,added=?,updated=?,unavailable=?,errors=?,started_at=?,finished_at=?,error=? WHERE id=?`, job.Status, job.Discovered, job.Processed, job.Added, job.Updated, job.Unavailable, job.Errors, job.StartedAt, job.FinishedAt, job.Error, job.ID)
	return err
}

func (service *Service) runScan(ctx context.Context, job ScanJob, roots []Root) {
	service.scanMu.Lock()
	job.Status = "running"
	_ = service.updateJob(context.WithoutCancel(service.ctx), job)
	service.scanMu.Unlock()
	slices.SortFunc(roots, func(a, b Root) int { return strings.Compare(a.ID, b.ID) })
	failedRoot := false
	processedRoots := 0
	for _, root := range roots {
		if ctx.Err() != nil {
			break
		}
		complete, err := service.scanRoot(ctx, root, &job)
		processedRoots++
		if err != nil || !complete {
			failedRoot = true
		}
	}
	if processedRoots == len(roots) && !failedRoot {
		job.Status = "complete"
		job.Error = ""
	} else if ctx.Err() != nil {
		job.Status = "cancelled"
		job.Error = ""
	} else {
		job.Status = "failed"
		job.Error = "one or more library roots could not be scanned"
	}
	job.FinishedAt = timestamp(time.Now())
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(service.ctx), 10*time.Second)
	_ = service.updateJob(persistCtx, job)
	cancel()
	service.scanMu.Lock()
	delete(service.cancels, job.ID)
	service.scanMu.Unlock()
	service.notify("library")
}

func (service *Service) scanRoot(ctx context.Context, root Root, job *ScanJob) (bool, error) {
	rootHandle, err := os.OpenRoot(root.Path)
	if err != nil {
		job.Errors++
		_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
		return false, err
	}
	defer rootHandle.Close()
	rootInfo, statErr := rootHandle.Stat(".")
	pathInfo, pathErr := os.Lstat(root.Path)
	if statErr != nil || pathErr != nil || !rootInfo.IsDir() || !pathInfo.IsDir() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(rootInfo, pathInfo) {
		job.Errors++
		_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
		return false, errors.New("library root is unavailable or unsafe")
	}
	candidates := make([]scanCandidate, 0)
	walkComplete := true
	err = filepath.WalkDir(root.Path, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			walkComplete = false
			job.Errors++
			_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == root.Path {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || !isSupportedPath(path) {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			walkComplete = false
			job.Errors++
			_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
			return nil
		}
		relative, relativeErr := filepath.Rel(root.Path, path)
		if relativeErr != nil || relative == "." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			walkComplete = false
			job.Errors++
			_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
			return nil
		}
		candidates = append(candidates, scanCandidate{relative: filepath.ToSlash(relative), size: info.Size(), modifiedNS: info.ModTime().UnixNano(), modifiedAt: timestamp(info.ModTime())})
		job.Discovered++
		_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return false, err
		}
		walkComplete = false
		job.Errors++
		_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
	}
	slices.SortFunc(candidates, func(a, b scanCandidate) int { return strings.Compare(a.relative, b.relative) })
	processComplete := true
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		result, processErr := service.processCandidate(ctx, root, job.ID, candidate)
		job.Processed++
		switch result {
		case "added":
			job.Added++
		case "updated":
			job.Updated++
		}
		if processErr != nil {
			job.Errors++
			processComplete = false
		}
		_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
	}
	currentRoot, rootErr := os.Lstat(root.Path)
	if rootErr != nil || currentRoot.Mode()&os.ModeSymlink != 0 || !os.SameFile(rootInfo, currentRoot) {
		walkComplete = false
		job.Errors++
		_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
	}
	if !walkComplete || !processComplete || ctx.Err() != nil {
		return false, ctx.Err()
	}
	result, err := service.db.ExecContext(ctx, `UPDATE library_tracks SET available=0 WHERE root_id=? AND available=1 AND last_seen_scan<>?`, root.ID, job.ID)
	if err != nil {
		job.Errors++
		_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
		return false, err
	}
	unavailable, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	job.Unavailable += unavailable
	_ = service.updateJob(context.WithoutCancel(service.ctx), *job)
	return true, nil
}

func (service *Service) processCandidate(ctx context.Context, root Root, scanID string, candidate scanCandidate) (string, error) {
	var oldSize, oldModified int64
	var available int
	var oldArtwork string
	err := service.db.QueryRowContext(ctx, `SELECT byte_size,modified_ns,available,artwork_id FROM library_tracks WHERE root_id=? AND relative_path=?`, root.ID, candidate.relative).Scan(&oldSize, &oldModified, &available, &oldArtwork)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if exists && oldSize == candidate.size && oldModified == candidate.modifiedNS && (oldArtwork == "" || service.cachedArtworkExists(oldArtwork)) {
		_, err = service.db.ExecContext(ctx, `UPDATE library_tracks SET available=1,last_seen_scan=? WHERE root_id=? AND relative_path=?`, scanID, root.ID, candidate.relative)
		if err != nil {
			return "", err
		}
		if available == 0 {
			return "updated", nil
		}
		return "", nil
	}
	file, err := openRootFile(ctx, root.Path, candidate.relative)
	if err != nil {
		service.markSeen(ctx, root.ID, candidate.relative, scanID)
		return "", err
	}
	before, err := file.Stat()
	if err != nil || before.Size() != candidate.size || before.ModTime().UnixNano() != candidate.modifiedNS {
		file.Close()
		service.markSeen(ctx, root.ID, candidate.relative, scanID)
		return "", errors.New("file changed during scan")
	}
	media, err := readMedia(file, candidate.relative, candidate.size)
	after, statErr := file.Stat()
	closeErr := file.Close()
	if err != nil {
		service.markSeen(ctx, root.ID, candidate.relative, scanID)
		return "", mediaParseError(candidate.relative, err)
	}
	if statErr != nil || closeErr != nil || after.Size() != candidate.size || after.ModTime().UnixNano() != candidate.modifiedNS {
		service.markSeen(ctx, root.ID, candidate.relative, scanID)
		return "", errors.New("file changed during scan")
	}
	artworkID, artworkErr := service.artworkFor(ctx, root, candidate.relative, media.artwork, media.artworkMime)
	if artworkErr != nil {
		artworkID = ""
	}
	albumDirectory := filepath.ToSlash(filepath.Dir(filepath.FromSlash(candidate.relative)))
	album := media.metadata.Album
	if album == "" {
		if albumDirectory == "." {
			album = root.Name
		} else {
			album = normalizeDisplay(filepath.Base(filepath.FromSlash(albumDirectory)))
		}
	}
	albumArtist := media.metadata.AlbumArtist
	if albumArtist == "" {
		albumArtist = media.metadata.Artist
	}
	albumID := stableID("album", root.ID, albumDirectory, normalizeSearch(album))
	trackID := stableID("track", root.ID, candidate.relative)
	_, err = service.db.ExecContext(ctx, `INSERT INTO library_tracks(id,root_id,relative_path,title,artist,album,album_artist,album_id,disc,track_number,genres_json,duration_ms,format,mime,artwork_id,byte_size,modified_ns,modified_at,available,last_seen_scan) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?) ON CONFLICT(root_id,relative_path) DO UPDATE SET title=excluded.title,artist=excluded.artist,album=excluded.album,album_artist=excluded.album_artist,album_id=excluded.album_id,disc=excluded.disc,track_number=excluded.track_number,genres_json=excluded.genres_json,duration_ms=excluded.duration_ms,format=excluded.format,mime=excluded.mime,artwork_id=excluded.artwork_id,byte_size=excluded.byte_size,modified_ns=excluded.modified_ns,modified_at=excluded.modified_at,available=1,last_seen_scan=excluded.last_seen_scan`, trackID, root.ID, candidate.relative, media.metadata.Title, media.metadata.Artist, album, albumArtist, albumID, media.metadata.Disc, media.metadata.Track, encodeGenres(media.metadata.Genres), media.durationMS, media.format, media.mime, artworkID, candidate.size, candidate.modifiedNS, candidate.modifiedAt, scanID)
	if err != nil {
		return "", err
	}
	if artworkErr != nil {
		if exists {
			return "updated", artworkErr
		}
		return "added", artworkErr
	}
	if exists {
		return "updated", nil
	}
	return "added", nil
}

func (service *Service) markSeen(ctx context.Context, rootID, relative, scanID string) {
	_, _ = service.db.ExecContext(ctx, `UPDATE library_tracks SET last_seen_scan=? WHERE root_id=? AND relative_path=?`, scanID, rootID, relative)
}

func (service *Service) Scans(ctx context.Context) ([]ScanJob, error) {
	rows, err := service.db.QueryContext(ctx, `SELECT id,status,discovered,processed,added,updated,unavailable,errors,started_at,finished_at,error FROM library_scan_jobs ORDER BY started_at DESC,id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list library scans: %w", err)
	}
	defer rows.Close()
	items := []ScanJob{}
	for rows.Next() {
		var job ScanJob
		if err := rows.Scan(&job.ID, &job.Status, &job.Discovered, &job.Processed, &job.Added, &job.Updated, &job.Unavailable, &job.Errors, &job.StartedAt, &job.FinishedAt, &job.Error); err != nil {
			return nil, fmt.Errorf("read library scan: %w", err)
		}
		items = append(items, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list library scans: %w", err)
	}
	return items, nil
}

func (service *Service) CancelScan(ctx context.Context, id string) error {
	var status string
	err := service.db.QueryRowContext(ctx, `SELECT status FROM library_scan_jobs WHERE id=?`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return notFound("scan was not found")
	}
	if err != nil {
		return fmt.Errorf("load scan: %w", err)
	}
	if status != "queued" && status != "running" {
		return conflict("SCAN_FINISHED", "scan has already finished")
	}
	service.scanMu.Lock()
	cancel := service.cancels[id]
	service.scanMu.Unlock()
	if cancel == nil {
		return conflict("SCAN_FINISHED", "scan is no longer active")
	}
	cancel()
	return nil
}
