package downloads

import (
	"context"
	"crypto/sha256"
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
	"syscall"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
)

type pendingTrack struct {
	index         int
	trackID       string
	sourceVersion string
	quality       string
}

type preparedArtifact struct {
	name   string
	mime   string
	codec  string
	size   int64
	digest string
}

func (service *Service) runWorker() {
	defer service.workers.Done()
	for {
		select {
		case <-service.ctx.Done():
			return
		case id := <-service.queue:
			service.prepareJob(id)
		}
	}
}

func (service *Service) prepareJob(id string) {
	defer func() { <-service.admissions }()
	var jobStatus, expiresAt string
	if err := service.db.QueryRowContext(service.ctx, `SELECT status,expires_at FROM download_jobs WHERE id=?`, id).Scan(&jobStatus, &expiresAt); err != nil {
		return
	}
	if jobStatus != "preparing" {
		return
	}
	deadline, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		deadline = time.Now()
	}
	ctx, cancel := context.WithDeadline(service.ctx, deadline)
	service.cancelMu.Lock()
	service.cancels[id] = cancel
	service.cancelMu.Unlock()
	defer func() {
		cancel()
		service.cancelMu.Lock()
		delete(service.cancels, id)
		service.cancelMu.Unlock()
	}()

	rows, err := service.db.QueryContext(ctx, `SELECT position,track_id,source_version,quality FROM download_job_tracks WHERE job_id=? AND status='pending' ORDER BY position`, id)
	if err != nil {
		_ = service.failQueuedJob(context.Background(), id, "PREPARATION_FAILED", "Download preparation could not be started.")
		return
	}
	pending := []pendingTrack{}
	for rows.Next() {
		var item pendingTrack
		if err := rows.Scan(&item.index, &item.trackID, &item.sourceVersion, &item.quality); err != nil {
			rows.Close()
			_ = service.failQueuedJob(context.Background(), id, "PREPARATION_FAILED", "Download preparation could not be started.")
			return
		}
		pending = append(pending, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		_ = service.failQueuedJob(context.Background(), id, "PREPARATION_FAILED", "Download preparation could not be started.")
		return
	}
	rows.Close()

	for _, item := range pending {
		if ctx.Err() != nil {
			break
		}
		result, err := service.db.ExecContext(ctx, `UPDATE download_job_tracks SET status='preparing',error_code='',error_message='' WHERE job_id=? AND position=? AND status='pending' AND EXISTS(SELECT 1 FROM download_jobs WHERE id=? AND status='preparing')`, id, item.index, id)
		if err != nil {
			break
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			continue
		}

		if reused, ok := service.reusableArtifact(ctx, id, item); ok {
			if err := service.commitTrack(ctx, id, item.index, reused); err == nil {
				continue
			} else if ctx.Err() != nil {
				break
			}
		}
		trackCtx, stop := context.WithTimeout(ctx, preparationTimeout)
		artifact, prepareErr := service.prepareTrack(trackCtx, id, item)
		trackContextErr := trackCtx.Err()
		stop()
		if prepareErr != nil {
			if ctx.Err() != nil {
				break
			}
			if errors.Is(trackContextErr, context.DeadlineExceeded) {
				prepareErr = context.DeadlineExceeded
			}
			code, message := preparationError(prepareErr)
			_ = service.failTrack(ctx, id, item.index, code, message)
			continue
		}
		if err := service.commitTrack(ctx, id, item.index, artifact); err != nil {
			_ = os.Remove(filepath.Join(service.directory, id, artifact.name+".uncommitted"))
			if ctx.Err() != nil {
				break
			}
			code, message := preparationError(err)
			_ = service.failTrack(ctx, id, item.index, code, message)
		}
	}

	finalCtx := context.Background()
	var status string
	if err := service.db.QueryRowContext(finalCtx, `SELECT status FROM download_jobs WHERE id=?`, id).Scan(&status); err != nil || status != "preparing" {
		return
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = service.terminatePreparation(finalCtx, id, "JOB_EXPIRED", "Download preparation reached its deadline.")
		} else {
			_ = service.terminatePreparation(finalCtx, id, "JOB_INTERRUPTED", "Download preparation was interrupted.")
		}
		return
	}
	_ = service.finishJob(finalCtx, id, time.Now().UTC().Add(artifactRetention).Format(time.RFC3339Nano))
}

func (service *Service) reusableArtifact(ctx context.Context, jobID string, item pendingTrack) (preparedArtifact, bool) {
	var artifact preparedArtifact
	err := service.db.QueryRowContext(ctx, `SELECT artifact_name,mime,codec,byte_size,sha256 FROM download_job_tracks WHERE job_id=? AND source_version=? AND quality=? AND status='ready' AND artifact_name<>'' ORDER BY position LIMIT 1`, jobID, item.sourceVersion, item.quality).Scan(&artifact.name, &artifact.mime, &artifact.codec, &artifact.size, &artifact.digest)
	if err != nil {
		return preparedArtifact{}, false
	}
	path, err := service.artifactPath(jobID, artifact.name)
	if err != nil {
		return preparedArtifact{}, false
	}
	info, err := os.Lstat(path)
	return artifact, err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() == artifact.size
}

func (service *Service) prepareTrack(ctx context.Context, jobID string, item pendingTrack) (preparedArtifact, error) {
	source, current, err := service.catalog.Open(ctx, item.trackID)
	if err != nil {
		return preparedArtifact{}, err
	}
	defer source.Close()
	stopSourceCloser := make(chan struct{})
	defer close(stopSourceCloser)
	go func() {
		select {
		case <-ctx.Done():
			_ = source.Close()
		case <-stopSourceCloser:
		}
	}()
	if sourceVersion(current) != item.sourceVersion {
		return preparedArtifact{}, errSourceChanged
	}
	before, err := source.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != current.Size {
		return preparedArtifact{}, errSourceChanged
	}
	if before.Size() > maximumArtifactBytes {
		return preparedArtifact{}, errArtifactTooLarge
	}
	originalCodec := strings.ToLower(current.Format)
	if item.quality == QualityOriginal {
		info, inspectErr := service.catalog.Info(ctx, item.trackID)
		if inspectErr != nil {
			return preparedArtifact{}, inspectErr
		}
		if sourceVersion(info.Track) != item.sourceVersion {
			return preparedArtifact{}, errSourceChanged
		}
		if info.Audio.Codec != "" {
			originalCodec = strings.ToLower(info.Audio.Codec)
		}
	}

	directory, err := service.jobDirectory(jobID)
	if err != nil {
		return preparedArtifact{}, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return preparedArtifact{}, fmt.Errorf("create job artifact directory: %w", err)
	}
	directoryInfo, err := os.Lstat(directory)
	if err != nil || !directoryInfo.IsDir() || directoryInfo.Mode()&os.ModeSymlink != 0 {
		return preparedArtifact{}, errors.New("download artifact directory is unsafe")
	}
	temporary, err := os.CreateTemp(directory, ".prepare-*")
	if err != nil {
		return preparedArtifact{}, fmt.Errorf("create temporary artifact: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	artifact := preparedArtifact{}
	if item.quality == QualityOriginal {
		extension := safeExtension(current.Format)
		artifact.name = "original-" + item.sourceVersion + extension
		artifact.mime = current.Mime
		artifact.codec = originalCodec
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(temporary, hash), io.LimitReader(source, maximumArtifactBytes+1))
		if copyErr != nil {
			temporary.Close()
			return preparedArtifact{}, fmt.Errorf("copy original artifact: %w", copyErr)
		}
		if written != current.Size || written > maximumArtifactBytes {
			temporary.Close()
			return preparedArtifact{}, errSourceChanged
		}
		artifact.size = written
		artifact.digest = hex.EncodeToString(hash.Sum(nil))
	} else {
		if !service.aacEnabled {
			temporary.Close()
			return preparedArtifact{}, errQualityUnavailable
		}
		artifact.name = "aac-" + item.sourceVersion + ".m4a"
		artifact.mime = "audio/mp4"
		artifact.codec = "aac"
		if err := temporary.Close(); err != nil {
			return preparedArtifact{}, fmt.Errorf("close temporary artifact: %w", err)
		}
		arguments := []string{
			"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-y",
			"-fd", "0", "-i", "fd:", "-map", "0:a:0", "-vn", "-sn", "-dn",
			"-af", "aformat=channel_layouts=mono|stereo",
			"-c:a", "aac", "-profile:a", "aac_low", "-b:a", "256k",
			"-movflags", "+faststart", "-fs", strconv.FormatInt(maximumArtifactBytes, 10),
			"-f", "ipod", temporaryPath,
		}
		command := exec.CommandContext(ctx, service.ffmpegPath, arguments...)
		command.Stdin = source
		command.Stdout = io.Discard
		command.Stderr = &limitedWriter{writer: io.Discard, remaining: 64 << 10}
		command.WaitDelay = 2 * time.Second
		if err := command.Run(); err != nil {
			return preparedArtifact{}, fmt.Errorf("convert AAC artifact: %w", errConversionFailed)
		}
		temporary, err = os.OpenFile(temporaryPath, os.O_RDWR, 0)
		if err != nil {
			return preparedArtifact{}, fmt.Errorf("open converted artifact: %w", err)
		}
		hash := sha256.New()
		written, hashErr := io.Copy(hash, io.LimitReader(temporary, maximumArtifactBytes+1))
		if hashErr != nil || written == 0 || written > maximumArtifactBytes {
			temporary.Close()
			return preparedArtifact{}, errIntegrityFailed
		}
		artifact.size = written
		artifact.digest = hex.EncodeToString(hash.Sum(nil))
	}

	after, statErr := source.Stat()
	if statErr != nil || !os.SameFile(before, after) || before.Size() != after.Size() || before.ModTime() != after.ModTime() {
		temporary.Close()
		return preparedArtifact{}, errSourceChanged
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return preparedArtifact{}, fmt.Errorf("sync prepared artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return preparedArtifact{}, fmt.Errorf("close prepared artifact: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o400); err != nil {
		return preparedArtifact{}, fmt.Errorf("protect prepared artifact: %w", err)
	}
	uncommitted := filepath.Join(directory, artifact.name+".uncommitted")
	if err := os.Rename(temporaryPath, uncommitted); err != nil {
		return preparedArtifact{}, fmt.Errorf("stage prepared artifact: %w", err)
	}
	return artifact, nil
}

func (service *Service) commitTrack(ctx context.Context, jobID string, index int, artifact preparedArtifact) error {
	service.operationMu.Lock()
	defer service.operationMu.Unlock()
	var jobStatus, trackStatus string
	if err := service.db.QueryRowContext(ctx, `SELECT jobs.status,tracks.status FROM download_jobs AS jobs JOIN download_job_tracks AS tracks ON tracks.job_id=jobs.id WHERE jobs.id=? AND tracks.position=?`, jobID, index).Scan(&jobStatus, &trackStatus); err != nil {
		return err
	}
	if jobStatus != "preparing" || trackStatus != "preparing" {
		return context.Canceled
	}
	finalPath, err := service.artifactPath(jobID, artifact.name)
	if err != nil {
		return err
	}
	stagedPath := finalPath + ".uncommitted"
	if info, statErr := os.Lstat(finalPath); statErr == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != artifact.size {
			return errIntegrityFailed
		}
		_ = os.Remove(stagedPath)
	} else {
		var retained int64
		if err := service.db.QueryRowContext(ctx, `SELECT COALESCE(sum(byte_size),0) FROM (SELECT job_id,artifact_name,max(byte_size) AS byte_size FROM download_job_tracks WHERE artifact_name<>'' GROUP BY job_id,artifact_name)`).Scan(&retained); err != nil {
			return fmt.Errorf("measure retained download artifacts: %w", err)
		}
		if artifact.size < 0 || retained > maximumArtifactBytes-artifact.size {
			return errStorageFull
		}
		if err := os.Rename(stagedPath, finalPath); err != nil {
			return fmt.Errorf("commit prepared artifact: %w", err)
		}
		if err := syncDirectory(filepath.Dir(finalPath)); err != nil {
			return err
		}
	}
	mediaPath := fmt.Sprintf("/api/v1/downloads/%s/files/%d", jobID, index)
	result, err := service.db.ExecContext(ctx, `UPDATE download_job_tracks SET status='ready',mime=?,codec=?,byte_size=?,sha256=?,media_path=?,error_code='',error_message='',artifact_name=? WHERE job_id=? AND position=? AND status='preparing' AND EXISTS(SELECT 1 FROM download_jobs WHERE id=? AND status='preparing')`, artifact.mime, artifact.codec, artifact.size, artifact.digest, mediaPath, artifact.name, jobID, index, jobID)
	if err != nil {
		return fmt.Errorf("save prepared artifact: %w", err)
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return context.Canceled
	}
	return nil
}

func (service *Service) failTrack(ctx context.Context, jobID string, index int, code, message string) error {
	_, err := service.db.ExecContext(ctx, `UPDATE download_job_tracks SET status='failed',mime='',codec='',byte_size=0,sha256='',media_path='',artifact_name='',error_code=?,error_message=? WHERE job_id=? AND position=? AND status IN ('pending','preparing') AND EXISTS(SELECT 1 FROM download_jobs WHERE id=? AND status='preparing')`, code, message, jobID, index, jobID)
	return err
}

func safeExtension(format string) string {
	switch strings.ToLower(format) {
	case "aac", "aiff", "ape", "flac", "m4a", "mp3", "ogg", "opus", "wav", "wv", "wma":
		return "." + strings.ToLower(format)
	default:
		return ".audio"
	}
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact directory: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync artifact directory: %w", err)
	}
	return nil
}

var (
	errSourceChanged      = errors.New("download source changed")
	errArtifactTooLarge   = errors.New("download artifact too large")
	errQualityUnavailable = errors.New("download quality unavailable")
	errConversionFailed   = errors.New("download conversion failed")
	errIntegrityFailed    = errors.New("download integrity failed")
	errStorageFull        = errors.New("download artifact storage full")
)

func preparationError(err error) (string, string) {
	var public *fault.Error
	switch {
	case errors.Is(err, errSourceChanged):
		return "SOURCE_CHANGED", "The source track changed after the download was requested."
	case errors.Is(err, errArtifactTooLarge):
		return "ARTIFACT_TOO_LARGE", "The prepared file exceeds the server download limit."
	case errors.Is(err, errQualityUnavailable):
		return "QUALITY_UNAVAILABLE", "The requested quality is not available on this server."
	case errors.Is(err, errConversionFailed):
		return "CONVERSION_FAILED", "The server could not create the AAC file."
	case errors.Is(err, errIntegrityFailed):
		return "INTEGRITY_FAILED", "The prepared file failed integrity validation."
	case errors.Is(err, syscall.ENOSPC):
		return "STORAGE_FULL", "The server does not have enough space to prepare this track."
	case errors.Is(err, errStorageFull):
		return "STORAGE_FULL", "The server download artifact limit has been reached."
	case errors.As(err, &public) && public.Code == "MEDIA_STALE":
		return "SOURCE_CHANGED", "The source track changed after the download was requested."
	case errors.As(err, &public) && public.Status == http.StatusNotFound:
		return "SOURCE_UNAVAILABLE", "The source track is unavailable."
	case errors.Is(err, context.DeadlineExceeded):
		return "PREPARATION_TIMEOUT", "Download preparation took too long."
	default:
		return "PREPARATION_FAILED", "The server could not prepare this track."
	}
}
