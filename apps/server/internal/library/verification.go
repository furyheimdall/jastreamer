package library

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"unicode/utf8"
)

const (
	verificationPollInterval = 250 * time.Millisecond
	verificationProcessDelay = 2 * time.Second
	verificationProbeTimeout = 5 * time.Second
	verificationMinimumTime  = 30 * time.Second
	verificationMaximumTime  = 30 * time.Minute
	verificationBufferSize   = 32 << 10
	verificationErrorBytes   = 8 << 10
	verificationProbeBytes   = 1 << 20
)

var (
	errVerificationPaused     = errors.New("library verification paused")
	errVerificationSuperseded = errors.New("library verification superseded")
)

type verificationItem struct {
	runID        string
	scanID       string
	trackID      string
	rootID       string
	rootName     string
	relativePath string
	title        string
	format       string
	durationMS   int64
	byteSize     int64
	modifiedNS   int64
}

type verificationResult struct {
	status  string
	code    string
	message string
	details json.RawMessage
}

type flacStreamInfo struct {
	channels      int
	bitsPerSample int
	totalSamples  uint64
	md5           [md5.Size]byte
	checksum      bool
}

type boundedOutput struct {
	mu        sync.Mutex
	remaining int
	data      []byte
}

func (output *boundedOutput) Write(value []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
	if len(value) <= output.remaining {
		output.data = append(output.data, value...)
		output.remaining -= len(value)
	} else if output.remaining > 0 {
		output.data = append(output.data, value[:output.remaining]...)
		output.remaining = 0
	}
	return len(value), nil
}

func (service *Service) StartVerification(options VerificationOptions) error {
	service.verifyMu.Lock()
	if service.verifyStarted {
		service.verifyMu.Unlock()
		return errors.New("library verification already started")
	}
	if options.IsPlaybackActive == nil {
		options.IsPlaybackActive = func() bool { return false }
	}
	service.verifyStarted = true
	service.verifyOptions = options
	service.verifyMu.Unlock()

	path, capabilityError := probeVerificationEngine(service.ctx, options.FFmpegPath)
	if capabilityError != "" {
		if _, err := service.db.ExecContext(service.ctx, `UPDATE library_verification_state SET state='unavailable',reason='engine',engine='ffmpeg',error=?,current_track_id='',current_track_title='',updated_at=? WHERE singleton=1`, capabilityError, timestamp(time.Now())); err != nil {
			return fmt.Errorf("save verification engine status: %w", err)
		}
		service.notify("verification")
		return nil
	}
	service.verifyMu.Lock()
	service.verifyOptions.FFmpegPath = path
	service.verifyMu.Unlock()
	if _, err := service.db.ExecContext(service.ctx, `UPDATE library_verification_state SET state=CASE WHEN pending>0 THEN 'queued' WHEN run_id<>'' THEN 'complete' ELSE 'idle' END,reason='',engine='ffmpeg',error='',current_track_id='',current_track_title='',updated_at=? WHERE singleton=1`, timestamp(time.Now())); err != nil {
		return fmt.Errorf("save verification engine status: %w", err)
	}
	service.verifyMu.Lock()
	service.verifyDone = make(chan struct{})
	service.verifyMu.Unlock()
	go service.runVerificationWorker()
	service.wakeVerification()
	return nil
}

func (service *Service) VerificationStatus(ctx context.Context) (VerificationStatus, error) {
	var status VerificationStatus
	err := service.db.QueryRowContext(ctx, `SELECT state,reason,total,pending,verified,failed,unverified,current_track_id,current_track_title,engine,error FROM library_verification_state WHERE singleton=1`).Scan(
		&status.State, &status.Reason, &status.Total, &status.Pending, &status.Verified, &status.Failed,
		&status.Unverified, &status.CurrentTrackID, &status.CurrentTrackTitle, &status.Engine, &status.Error,
	)
	if err != nil {
		return VerificationStatus{}, fmt.Errorf("load library verification status: %w", err)
	}
	return status, nil
}

func (service *Service) WaitVerification(ctx context.Context) error {
	service.verifyMu.Lock()
	done := service.verifyDone
	service.verifyMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func probeVerificationEngine(parent context.Context, path string) (string, string) {
	if path == "" {
		return "", "FFmpeg is not configured."
	}
	if !filepath.IsAbs(path) {
		return "", "FFmpeg is not configured with an absolute executable path."
	}
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", "The configured FFmpeg engine is unavailable."
	}
	checks := []struct {
		argument string
		required string
	}{
		{argument: "-protocols", required: "fd"},
		{argument: "-encoders", required: "pcm_s32le"},
		{argument: "-muxers", required: "s32le"},
	}
	for _, check := range checks {
		ctx, cancel := context.WithTimeout(parent, verificationProbeTimeout)
		output := &boundedOutput{remaining: verificationProbeBytes}
		command := exec.CommandContext(ctx, path, "-hide_banner", check.argument)
		command.Stdout = output
		command.Stderr = output
		command.WaitDelay = verificationProcessDelay
		err := command.Run()
		cancel()
		if err != nil || !containsFFmpegCapability(string(output.data), check.required) {
			return "", "The configured FFmpeg engine lacks required verification capabilities."
		}
	}
	return path, ""
}

func containsFFmpegCapability(output, required string) bool {
	for _, field := range strings.Fields(output) {
		if field == required {
			return true
		}
	}
	return false
}

func (service *Service) recoverVerification(ctx context.Context) error {
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin verification recovery: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE library_verification_items SET status='pending' WHERE status='running'`); err != nil {
		return fmt.Errorf("recover verification item: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_verification_state SET
		total=(SELECT count(*) FROM library_verification_items WHERE run_id=library_verification_state.run_id),
		pending=(SELECT count(*) FROM library_verification_items WHERE run_id=library_verification_state.run_id AND status IN ('pending','running')),
		verified=(SELECT count(*) FROM library_verification_items WHERE run_id=library_verification_state.run_id AND status='verified'),
		failed=(SELECT count(*) FROM library_verification_items WHERE run_id=library_verification_state.run_id AND status='failed'),
		unverified=(SELECT count(*) FROM library_verification_items WHERE run_id=library_verification_state.run_id AND status='unverified'),
		state=CASE WHEN run_id='' THEN 'idle' WHEN EXISTS(SELECT 1 FROM library_verification_items WHERE run_id=library_verification_state.run_id AND status IN ('pending','running')) THEN 'queued' ELSE 'complete' END,
		reason=CASE WHEN run_id='' THEN '' WHEN EXISTS(SELECT 1 FROM library_verification_items WHERE run_id=library_verification_state.run_id AND status IN ('pending','running')) THEN 'restart' ELSE '' END,
		current_track_id='',current_track_title='',error='',updated_at=? WHERE singleton=1`, timestamp(time.Now())); err != nil {
		return fmt.Errorf("recover verification status: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit verification recovery: %w", err)
	}
	return nil
}

func (service *Service) configuredRootsMatch(ctx context.Context, roots []Root) (bool, error) {
	rows, err := service.db.QueryContext(ctx, `SELECT id,name,path FROM library_roots WHERE configured=1 ORDER BY id`)
	if err != nil {
		return false, fmt.Errorf("load configured library roots: %w", err)
	}
	defer rows.Close()
	stored := make(map[string]Root, len(roots))
	for rows.Next() {
		var root Root
		if err := rows.Scan(&root.ID, &root.Name, &root.Path); err != nil {
			return false, fmt.Errorf("read configured library root: %w", err)
		}
		stored[root.ID] = root
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read configured library roots: %w", err)
	}
	if len(stored) != len(roots) {
		return false, nil
	}
	for _, root := range roots {
		if previous, ok := stored[root.ID]; !ok || previous.Name != root.Name || previous.Path != root.Path {
			return false, nil
		}
	}
	return true, nil
}

func (service *Service) reconcileVerificationRoots(roots []Root) error {
	ctx, stop := context.WithTimeout(context.WithoutCancel(service.ctx), 5*time.Second)
	defer stop()
	configured := make(map[string]Root, len(roots))
	for _, root := range roots {
		configured[root.ID] = root
	}
	rows, err := service.db.QueryContext(ctx, `SELECT id,name,path FROM library_roots WHERE configured=1`)
	if err != nil {
		return fmt.Errorf("load verification roots: %w", err)
	}
	previous := make(map[string]Root)
	for rows.Next() {
		var root Root
		if err = rows.Scan(&root.ID, &root.Name, &root.Path); err != nil {
			rows.Close()
			return fmt.Errorf("read verification root: %w", err)
		}
		previous[root.ID] = root
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read verification roots: %w", err)
	}
	if err = rows.Close(); err != nil {
		return fmt.Errorf("close verification roots: %w", err)
	}
	invalidated := make([]string, 0)
	for id, oldRoot := range previous {
		root, exists := configured[id]
		if !exists || root.Path != oldRoot.Path {
			invalidated = append(invalidated, id)
		}
	}

	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin verification root reconciliation: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE library_verification_items SET status='pending' WHERE status='running'`); err != nil {
		return fmt.Errorf("pause verification root reconciliation: %w", err)
	}
	for _, id := range invalidated {
		if _, err = tx.ExecContext(ctx, `DELETE FROM library_verification_items WHERE root_id=?`, id); err != nil {
			return fmt.Errorf("invalidate verification root: %w", err)
		}
	}
	for id, root := range configured {
		if oldRoot, exists := previous[id]; exists && oldRoot.Path == root.Path && oldRoot.Name != root.Name {
			if _, err = tx.ExecContext(ctx, `UPDATE library_verification_items SET root_name=? WHERE root_id=?`, root.Name, id); err != nil {
				return fmt.Errorf("rename verification root: %w", err)
			}
		}
	}
	var runID, scanID, state, reason, stateError string
	if err = tx.QueryRowContext(ctx, `SELECT run_id,scan_id,state,reason,error FROM library_verification_state WHERE singleton=1`).Scan(&runID, &scanID, &state, &reason, &stateError); err != nil {
		return fmt.Errorf("load verification root state: %w", err)
	}
	var total, pending, verified, failed, unverified int64
	if runID != "" {
		if err = tx.QueryRowContext(ctx, `SELECT count(*),
			coalesce(sum(status IN ('pending','running')),0),
			coalesce(sum(status='verified'),0),
			coalesce(sum(status='failed'),0),
			coalesce(sum(status='unverified'),0)
			FROM library_verification_items WHERE run_id=?`, runID).Scan(&total, &pending, &verified, &failed, &unverified); err != nil {
			return fmt.Errorf("count reconciled verification items: %w", err)
		}
	}
	engineUnavailable := state == "unavailable" && reason == "engine"
	if len(invalidated) != 0 && total == 0 {
		runID = ""
		scanID = ""
	}
	switch {
	case engineUnavailable:
		state = "unavailable"
	case runID == "":
		state = "idle"
		reason = ""
		stateError = ""
	case pending != 0:
		state = "queued"
		reason = ""
		stateError = ""
	default:
		state = "complete"
		reason = ""
		stateError = ""
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_verification_state SET run_id=?,scan_id=?,state=?,reason=?,total=?,pending=?,verified=?,failed=?,unverified=?,current_track_id='',current_track_title='',error=?,updated_at=? WHERE singleton=1`, runID, scanID, state, reason, total, pending, verified, failed, unverified, stateError, timestamp(time.Now())); err != nil {
		return fmt.Errorf("save verification root reconciliation: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit verification root reconciliation: %w", err)
	}
	service.notify("verification")
	return nil
}

func (service *Service) setVerificationScanning(active bool) {
	service.verifyMu.Lock()
	service.scanActive = active
	cancel := service.verifyCancel
	service.verifyMu.Unlock()
	if active && cancel != nil {
		cancel(errVerificationPaused)
	}
	service.wakeVerification()
}

func (service *Service) scheduleVerification(scanID string, full bool) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(service.ctx), 10*time.Second)
	defer cancel()
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin verification scheduling: %w", err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO library_verification_items(run_id,scan_id,track_id,root_id,root_name,relative_path,title,format,duration_ms,byte_size,modified_ns,status)
		SELECT ?,?,tracks.id,tracks.root_id,roots.name,tracks.relative_path,tracks.title,tracks.format,tracks.duration_ms,tracks.byte_size,tracks.modified_ns,
			CASE WHEN ? THEN 'pending'
				WHEN previous.status IN ('verified','failed','unverified')
					AND previous.root_id=tracks.root_id AND previous.relative_path=tracks.relative_path
					AND previous.byte_size=tracks.byte_size AND previous.modified_ns=tracks.modified_ns
				THEN previous.status ELSE 'pending' END
		FROM library_tracks AS tracks
		JOIN library_roots AS roots ON roots.id=tracks.root_id
		LEFT JOIN library_verification_state AS state ON state.singleton=1
		LEFT JOIN library_verification_items AS previous ON previous.run_id=state.run_id AND previous.track_id=tracks.id
		WHERE tracks.available=1 AND tracks.last_seen_scan=? AND roots.configured=1`, scanID, scanID, full, scanID); err != nil {
		return fmt.Errorf("schedule verification items: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM library_verification_items WHERE run_id<>?`, scanID); err != nil {
		return fmt.Errorf("prune previous verification items: %w", err)
	}
	var total, pending, verified, failed, unverified int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*),
		coalesce(sum(status IN ('pending','running')),0),
		coalesce(sum(status='verified'),0),
		coalesce(sum(status='failed'),0),
		coalesce(sum(status='unverified'),0)
		FROM library_verification_items WHERE run_id=?`, scanID).Scan(&total, &pending, &verified, &failed, &unverified); err != nil {
		return fmt.Errorf("count verification items: %w", err)
	}
	state := "queued"
	if pending == 0 {
		state = "complete"
	}
	var previous, previousReason string
	if err = tx.QueryRowContext(ctx, `SELECT state,reason FROM library_verification_state WHERE singleton=1`).Scan(&previous, &previousReason); err != nil {
		return fmt.Errorf("load verification engine state: %w", err)
	}
	if previous == "unavailable" && previousReason == "engine" {
		state = "unavailable"
	}
	if _, err = tx.ExecContext(ctx, `UPDATE library_verification_state SET run_id=?,scan_id=?,state=?,reason=CASE WHEN ?='unavailable' THEN reason ELSE '' END,total=?,pending=?,verified=?,failed=?,unverified=?,current_track_id='',current_track_title='',error=CASE WHEN ?='unavailable' THEN error ELSE '' END,updated_at=? WHERE singleton=1`, scanID, scanID, state, state, total, pending, verified, failed, unverified, state, timestamp(time.Now())); err != nil {
		return fmt.Errorf("save verification schedule: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit verification schedule: %w", err)
	}
	service.notify("verification")
	service.wakeVerification()
	return nil
}

func (service *Service) setVerificationSchedulingFailure() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(service.ctx), 5*time.Second)
	defer cancel()
	_, _ = service.db.ExecContext(ctx, `UPDATE library_verification_state SET state='unavailable',reason='storage',error='Verification could not be scheduled.',current_track_id='',current_track_title='',updated_at=? WHERE singleton=1`, timestamp(time.Now()))
	service.notify("verification")
}

func (service *Service) wakeVerification() {
	select {
	case service.verifyWake <- struct{}{}:
	default:
	}
}

func (service *Service) runVerificationWorker() {
	defer func() {
		service.verifyMu.Lock()
		done := service.verifyDone
		service.verifyDone = nil
		service.verifyMu.Unlock()
		if done != nil {
			close(done)
		}
	}()
	for {
		if service.ctx.Err() != nil {
			return
		}
		item, ok, err := service.claimVerificationItem()
		if err != nil {
			if errors.Is(err, errVerificationPaused) {
				if !service.waitVerification() {
					return
				}
				continue
			}
			service.markVerificationStorageFailure()
			if !service.waitVerificationWake() {
				return
			}
			continue
		}
		if !ok {
			if !service.waitVerificationWake() {
				return
			}
			continue
		}
		service.verifyClaimedItem(item)
	}
}

func (service *Service) verificationPauseReason() string {
	service.verifyMu.Lock()
	scanning := service.scanActive
	playback := service.verifyOptions.IsPlaybackActive
	service.verifyMu.Unlock()
	if scanning {
		return "scan"
	}
	if playback != nil && playback() {
		return "playback"
	}
	return ""
}

func (service *Service) waitVerification() bool {
	timer := time.NewTimer(verificationPollInterval)
	defer timer.Stop()
	select {
	case <-service.ctx.Done():
		return false
	case <-service.verifyWake:
		return true
	case <-timer.C:
		return true
	}
}

func (service *Service) waitVerificationWake() bool {
	select {
	case <-service.ctx.Done():
		return false
	case <-service.verifyWake:
		return true
	}
}

func (service *Service) markVerificationPaused(reason string) {
	result, err := service.db.ExecContext(service.ctx, `UPDATE library_verification_state SET state='paused',reason=?,current_track_id='',current_track_title='',updated_at=? WHERE singleton=1 AND pending>0 AND state<>'unavailable' AND (state<>'paused' OR reason<>?)`, reason, timestamp(time.Now()), reason)
	if err == nil {
		if changed, _ := result.RowsAffected(); changed > 0 {
			service.notify("verification")
		}
	}
}

func (service *Service) markVerificationStorageFailure() {
	_, _ = service.db.ExecContext(service.ctx, `UPDATE library_verification_state SET state='unavailable',reason='storage',error='Verification progress could not be saved.',current_track_id='',current_track_title='',updated_at=? WHERE singleton=1`, timestamp(time.Now()))
	service.notify("verification")
}

func (service *Service) claimVerificationItem() (verificationItem, bool, error) {
	var item verificationItem
	err := service.db.QueryRowContext(service.ctx, `SELECT items.run_id,items.scan_id,items.track_id,items.root_id,items.root_name,items.relative_path,items.title,items.format,items.duration_ms,items.byte_size,items.modified_ns
		FROM library_verification_items AS items JOIN library_verification_state AS state ON state.run_id=items.run_id
		WHERE items.status='pending' AND state.state IN ('queued','running','paused') ORDER BY items.track_id LIMIT 1`).Scan(
		&item.runID, &item.scanID, &item.trackID, &item.rootID, &item.rootName, &item.relativePath,
		&item.title, &item.format, &item.durationMS, &item.byteSize, &item.modifiedNS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return verificationItem{}, false, nil
	}
	if err != nil {
		return verificationItem{}, false, err
	}
	if reason := service.verificationPauseReason(); reason != "" {
		service.markVerificationPaused(reason)
		return verificationItem{}, false, errVerificationPaused
	}
	result, err := service.db.ExecContext(service.ctx, `UPDATE library_verification_items SET status='running' WHERE run_id=? AND track_id=? AND status='pending'`, item.runID, item.trackID)
	if err != nil {
		return verificationItem{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return verificationItem{}, false, err
	}
	if _, err = service.db.ExecContext(service.ctx, `UPDATE library_verification_state SET state='running',reason='',current_track_id=?,current_track_title=?,error='',updated_at=? WHERE singleton=1 AND run_id=?`, item.trackID, item.title, timestamp(time.Now()), item.runID); err != nil {
		_, _ = service.db.ExecContext(service.ctx, `UPDATE library_verification_items SET status='pending' WHERE run_id=? AND track_id=? AND status='running'`, item.runID, item.trackID)
		return verificationItem{}, false, err
	}
	service.notify("verification")
	return item, true, nil
}

func (service *Service) verifyClaimedItem(item verificationItem) {
	verifyContext, cancel := service.verificationItemContext(item.durationMS)
	service.verifyMu.Lock()
	service.verifyCancel = cancel
	service.verifyMu.Unlock()
	result := service.verifyFile(verifyContext, item)
	cause := context.Cause(verifyContext)
	cancel(errVerificationSuperseded)
	service.verifyMu.Lock()
	if service.verifyCancel != nil {
		service.verifyCancel = nil
	}
	service.verifyMu.Unlock()

	if errors.Is(cause, errVerificationPaused) {
		_, _ = service.db.ExecContext(service.ctx, `UPDATE library_verification_items SET status='pending' WHERE run_id=? AND track_id=? AND status='running'`, item.runID, item.trackID)
		service.markVerificationPaused(service.verificationPauseReason())
		return
	}
	if service.ctx.Err() != nil || errors.Is(cause, errVerificationSuperseded) {
		return
	}
	if errors.Is(cause, context.DeadlineExceeded) {
		result = unknownVerification("VERIFY_TIMEOUT", "Audio verification exceeded its bounded runtime.", map[string]any{"check": "full_decode", "timed_out": true})
	}
	committed, err := service.commitVerificationResult(item, result)
	if err != nil {
		service.markVerificationStorageFailure()
		return
	}
	if !committed {
		_, _ = service.db.ExecContext(service.ctx, `UPDATE library_verification_items SET status='pending' WHERE run_id=? AND track_id=? AND status='running'`, item.runID, item.trackID)
		return
	}
	service.notify("verification")
}

func (service *Service) verificationItemContext(durationMS int64) (context.Context, context.CancelCauseFunc) {
	allowed := verificationMinimumTime
	if durationMS > 0 {
		maximumDurationMS := int64((verificationMaximumTime - verificationMinimumTime) / (4 * time.Millisecond))
		if durationMS >= maximumDurationMS {
			allowed = verificationMaximumTime
		} else {
			allowed = time.Duration(durationMS)*time.Millisecond*4 + verificationMinimumTime
		}
	}
	ctx, timeout := context.WithTimeoutCause(service.ctx, allowed, context.DeadlineExceeded)
	ctx, cancel := context.WithCancelCause(ctx)
	go func() {
		ticker := time.NewTicker(verificationPollInterval)
		defer ticker.Stop()
		select {
		case <-ctx.Done():
		case <-ticker.C:
			for {
				if service.verificationPauseReason() != "" {
					cancel(errVerificationPaused)
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}
	}()
	return ctx, func(cause error) {
		cancel(cause)
		timeout()
	}
}

func (service *Service) verifyFile(ctx context.Context, item verificationItem) verificationResult {
	file, track, err := service.Open(ctx, item.trackID)
	if err != nil {
		return unknownVerification("FILE_UNAVAILABLE", "The indexed audio file could not be opened for verification.", map[string]any{"check": "open"})
	}
	defer file.Close()
	if track.RootID != item.rootID || track.Path != item.relativePath || track.Format != item.format || track.Size != item.byteSize {
		return unknownVerification("FILE_CHANGED", "The audio file changed after verification was scheduled.", map[string]any{"check": "identity"})
	}
	before, err := file.Stat()
	if err != nil || !before.Mode().IsRegular() || before.Size() != item.byteSize || before.ModTime().UnixNano() != item.modifiedNS {
		return unknownVerification("FILE_CHANGED", "The audio file changed after verification was scheduled.", map[string]any{"check": "identity"})
	}

	result := service.decodeFile(ctx, file, item)
	if ctx.Err() != nil {
		return result
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != item.byteSize || after.ModTime().UnixNano() != item.modifiedNS {
		return unknownVerification("FILE_CHANGED", "The audio file changed during verification.", map[string]any{"check": "identity"})
	}
	current, _, err := service.Open(ctx, item.trackID)
	if err != nil {
		return unknownVerification("FILE_CHANGED", "The audio file changed during verification.", map[string]any{"check": "identity"})
	}
	currentInfo, statErr := current.Stat()
	closeErr := current.Close()
	if statErr != nil || closeErr != nil || !os.SameFile(before, currentInfo) {
		return unknownVerification("FILE_CHANGED", "The audio file changed during verification.", map[string]any{"check": "identity"})
	}
	return result
}

func (service *Service) decodeFile(ctx context.Context, file *os.File, item verificationItem) verificationResult {
	var streamInfo *flacStreamInfo
	if item.format == "flac" {
		parsed, err := readFLACStreamInfo(file)
		if err != nil {
			var readError *os.PathError
			if errors.As(err, &readError) {
				return unknownVerification("FILE_UNAVAILABLE", "The audio file could not be read for verification.", map[string]any{"check": "open"})
			}
			return failedVerification("FLAC_STREAMINFO_INVALID", "The FLAC STREAMINFO block is invalid or truncated.", map[string]any{"check": "flac_streaminfo"})
		}
		streamInfo = &parsed
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return unknownVerification("FILE_UNAVAILABLE", "The audio file could not be positioned for verification.", map[string]any{"check": "open"})
	}

	service.verifyMu.Lock()
	ffmpegPath := service.verifyOptions.FFmpegPath
	service.verifyMu.Unlock()
	demuxer := verificationDemuxer(item.format)
	if demuxer == "" {
		return unknownVerification("FORMAT_UNSUPPORTED", "The indexed audio format is not supported by the verification engine.", map[string]any{"check": "full_decode", "format": item.format})
	}
	arguments := []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-err_detect", "explode",
		"-max_alloc", "33554432", "-cpucount", "1", "-filter_threads", "1", "-filter_complex_threads", "1",
		"-threads", "1", "-protocol_whitelist", "fd,pipe", "-format_whitelist", demuxer, "-fd", "0", "-i", "fd:",
		"-map", "0:a:0", "-vn", "-sn", "-dn", "-threads", "1", "-acodec", "pcm_s32le", "-f", "s32le", "pipe:1",
	}
	command := exec.CommandContext(ctx, ffmpegPath, arguments...)
	command.Stdin = file
	stderr := &boundedOutput{remaining: verificationErrorBytes}
	command.Stderr = stderr
	command.WaitDelay = verificationProcessDelay
	stdout, err := command.StdoutPipe()
	if err != nil {
		return unknownVerification("ENGINE_UNAVAILABLE", "The verification engine could not be started.", map[string]any{"check": "full_decode", "engine": "ffmpeg"})
	}
	if err = command.Start(); err != nil {
		return unknownVerification("ENGINE_UNAVAILABLE", "The verification engine could not be started.", map[string]any{"check": "full_decode", "engine": "ffmpeg"})
	}
	decoded, digest, readErr := readDecodedPCM(stdout, streamInfo)
	if readErr != nil {
		_ = stdout.Close()
		_ = command.Process.Kill()
	}
	waitErr := command.Wait()
	if ctx.Err() != nil {
		return unknownVerification("VERIFY_INTERRUPTED", "Audio verification was interrupted.", map[string]any{"check": "full_decode"})
	}
	if readErr != nil {
		return unknownVerification("ENGINE_INCONCLUSIVE", "The verification engine produced an incomplete result.", map[string]any{"check": "full_decode", "engine": "ffmpeg"})
	}
	if waitErr != nil {
		return classifyDecodeFailure(string(stderr.data), item.format)
	}
	if decoded == 0 {
		if len(stderr.data) != 0 {
			return classifyDecodeFailure(string(stderr.data), item.format)
		}
		return failedVerification("AUDIO_DECODE_EMPTY", "The audio stream decoded without any samples.", map[string]any{"check": "full_decode"})
	}
	if streamInfo == nil {
		if len(stderr.data) != 0 {
			return classifyDecodeFailure(string(stderr.data), item.format)
		}
		return verifiedVerification(map[string]any{"check": "full_decode", "decoded_pcm_values": decoded})
	}
	if streamInfo.totalSamples != 0 && decoded != streamInfo.totalSamples {
		return failedVerification("FLAC_SAMPLE_COUNT_MISMATCH", "The FLAC decoded sample count does not match STREAMINFO.", map[string]any{"check": "flac_sample_count", "declared_samples": streamInfo.totalSamples, "decoded_samples": decoded})
	}
	if streamInfo.checksum && digest != streamInfo.md5 {
		return failedVerification("FLAC_CHECKSUM_MISMATCH", "The FLAC decoded-audio checksum does not match STREAMINFO.", map[string]any{"check": "flac_md5", "expected_md5": hex.EncodeToString(streamInfo.md5[:]), "actual_md5": hex.EncodeToString(digest[:]), "decoded_samples": decoded})
	}
	if len(stderr.data) != 0 {
		return classifyDecodeFailure(string(stderr.data), item.format)
	}
	return verifiedVerification(map[string]any{"check": "full_decode", "decoded_samples": decoded, "flac_md5_available": streamInfo.checksum, "flac_md5_verified": streamInfo.checksum})
}

func verificationDemuxer(format string) string {
	switch format {
	case "flac", "mp3", "ogg", "wav":
		return format
	case "opus":
		return "ogg"
	case "m4a":
		return "mov"
	default:
		return ""
	}
}

func readFLACStreamInfo(file *os.File) (flacStreamInfo, error) {
	var block [42]byte
	if _, err := file.ReadAt(block[:], 0); err != nil {
		return flacStreamInfo{}, err
	}
	if string(block[:4]) != "fLaC" || block[4]&0x7f != 0 || int(block[5])<<16|int(block[6])<<8|int(block[7]) != 34 {
		return flacStreamInfo{}, errors.New("invalid FLAC STREAMINFO")
	}
	minimumBlockSize := binary.BigEndian.Uint16(block[8:10])
	maximumBlockSize := binary.BigEndian.Uint16(block[10:12])
	if minimumBlockSize < 16 || maximumBlockSize < minimumBlockSize {
		return flacStreamInfo{}, errors.New("invalid FLAC STREAMINFO block sizes")
	}
	packed := binary.BigEndian.Uint64(block[18:26])
	info := flacStreamInfo{
		channels:      int((packed>>41)&0x7) + 1,
		bitsPerSample: int((packed>>36)&0x1f) + 1,
		totalSamples:  packed & 0xfffffffff,
	}
	copy(info.md5[:], block[26:42])
	for _, value := range info.md5 {
		if value != 0 {
			info.checksum = true
			break
		}
	}
	sampleRate := (packed >> 44) & 0xfffff
	if sampleRate == 0 || info.channels < 1 || info.channels > 8 || info.bitsPerSample < 4 || info.bitsPerSample > 32 {
		return flacStreamInfo{}, errors.New("invalid FLAC STREAMINFO audio parameters")
	}
	return info, nil
}

func readDecodedPCM(reader io.Reader, info *flacStreamInfo) (uint64, [md5.Size]byte, error) {
	buffer := make([]byte, verificationBufferSize)
	remainder := 0
	var words uint64
	var digester hash.Hash
	if info != nil && info.checksum {
		digester = md5.New()
	}
	for {
		count, err := reader.Read(buffer[remainder:verificationBufferSize])
		available := remainder + count
		complete := available - available%4
		words += uint64(complete / 4)
		if digester != nil {
			packed := complete
			if info.bitsPerSample != 32 {
				packed = 0
				bytesPerSample := (info.bitsPerSample + 7) / 8
				for offset := 0; offset < complete; offset += 4 {
					sample := int32(binary.LittleEndian.Uint32(buffer[offset : offset+4]))
					shifted := sample >> (32 - info.bitsPerSample)
					binary.LittleEndian.PutUint32(buffer[packed:packed+4], uint32(shifted))
					packed += bytesPerSample
				}
			}
			_, _ = digester.Write(buffer[:packed])
		}
		remainder = available - complete
		copy(buffer[:remainder], buffer[complete:available])
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, [md5.Size]byte{}, err
		}
		if count == 0 {
			return 0, [md5.Size]byte{}, io.ErrNoProgress
		}
	}
	if remainder != 0 {
		return 0, [md5.Size]byte{}, errors.New("partial decoded PCM sample")
	}
	channels := 1
	if info != nil {
		channels = info.channels
	}
	if words%uint64(channels) != 0 {
		return 0, [md5.Size]byte{}, errors.New("partial decoded PCM frame")
	}
	var digest [md5.Size]byte
	if digester != nil {
		copy(digest[:], digester.Sum(nil))
	}
	return words / uint64(channels), digest, nil
}

func classifyDecodeFailure(stderr, format string) verificationResult {
	text := strings.ToLower(stderr)
	for _, fragment := range []string{"input/output error", "permission denied", "access is denied", "cannot allocate memory", "out of memory", "memory allocation", "bad file descriptor", "too many open files", "resource temporarily unavailable"} {
		if strings.Contains(text, fragment) {
			return unknownVerification("ENGINE_RESOURCE_ERROR", "The file could not be verified because of an input or engine resource error.", map[string]any{"check": "full_decode", "format": format, "engine": "ffmpeg"})
		}
	}
	unsupported := []string{"decoder not found", "no decoder found", "unknown decoder", "demuxer not found", "not yet implemented", "unsupported codec"}
	for _, fragment := range unsupported {
		if strings.Contains(text, fragment) {
			return unknownVerification("FORMAT_UNSUPPORTED", "The verification engine does not support this indexed audio format.", map[string]any{"check": "full_decode", "format": format, "engine": "ffmpeg"})
		}
	}
	corrupt := []string{"invalid data", "corrupt", "crc mismatch", "checksum", "truncated", "error while decoding", "packet error", "end of file", "could not find codec parameters", "does not contain any stream", "no frames", "output file is empty"}
	for _, fragment := range corrupt {
		if strings.Contains(text, fragment) {
			return failedVerification("AUDIO_DECODE_CORRUPT", "Audio decoding found invalid or truncated content.", map[string]any{"check": "full_decode", "format": format, "engine": "ffmpeg"})
		}
	}
	return unknownVerification("ENGINE_INCONCLUSIVE", "The verification engine could not determine audio integrity.", map[string]any{"check": "full_decode", "format": format, "engine": "ffmpeg"})
}

func verifiedVerification(details map[string]any) verificationResult {
	return verificationResult{status: "verified", details: encodeVerificationDetails(details)}
}

func failedVerification(code, message string, details map[string]any) verificationResult {
	return verificationResult{status: "failed", code: code, message: message, details: encodeVerificationDetails(details)}
}

func unknownVerification(code, message string, details map[string]any) verificationResult {
	return verificationResult{status: "unverified", code: code, message: message, details: encodeVerificationDetails(details)}
}

func encodeVerificationDetails(details map[string]any) json.RawMessage {
	encoded, err := json.Marshal(details)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return encoded
}

func (service *Service) commitVerificationResult(item verificationItem, result verificationResult) (bool, error) {
	if result.status != "verified" && result.status != "failed" && result.status != "unverified" {
		return false, errors.New("invalid verification result")
	}
	service.verifyMu.Lock()
	history := service.verifyOptions.History
	service.verifyMu.Unlock()
	tx, err := service.db.BeginTx(service.ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	const settleUnverified = `UPDATE library_verification_items SET status=?
		WHERE run_id=? AND scan_id=? AND track_id=? AND status='running'
		AND NOT EXISTS(SELECT 1 FROM library_scan_jobs WHERE status IN ('queued','running'))
		AND EXISTS(SELECT 1 FROM library_verification_state WHERE singleton=1 AND run_id=?)`
	var updated sql.Result
	if result.status == "unverified" && (result.code == "FILE_CHANGED" || result.code == "FILE_UNAVAILABLE") {
		updated, err = tx.ExecContext(service.ctx, settleUnverified, result.status, item.runID, item.scanID, item.trackID, item.runID)
	} else {
		updated, err = tx.ExecContext(service.ctx, `UPDATE library_verification_items SET status=?
			WHERE run_id=? AND scan_id=? AND track_id=? AND status='running'
			AND NOT EXISTS(SELECT 1 FROM library_scan_jobs WHERE status IN ('queued','running'))
			AND EXISTS(SELECT 1 FROM library_tracks AS tracks JOIN library_roots AS roots ON roots.id=tracks.root_id
				WHERE tracks.id=? AND tracks.root_id=? AND tracks.relative_path=? AND tracks.byte_size=? AND tracks.modified_ns=?
				AND tracks.available=1 AND tracks.last_seen_scan=? AND roots.configured=1)`,
			result.status, item.runID, item.scanID, item.trackID, item.trackID, item.rootID, item.relativePath, item.byteSize, item.modifiedNS, item.scanID)
	}
	if err != nil {
		return false, err
	}
	changed, err := updated.RowsAffected()
	if err == nil && changed == 0 && result.code != "FILE_CHANGED" && result.code != "FILE_UNAVAILABLE" {
		// A cancelled rescan may have updated metadata without replacing this
		// verification run. Finish it as unknown instead of decoding it forever.
		result = unknownVerification("FILE_CHANGED", "The audio file or its library entry changed before verification finished.", map[string]any{"check": "identity"})
		updated, err = tx.ExecContext(service.ctx, settleUnverified, result.status, item.runID, item.scanID, item.trackID, item.runID)
		if err != nil {
			return false, err
		}
		changed, err = updated.RowsAffected()
	}
	if err != nil || changed != 1 {
		return false, err
	}
	if _, err = tx.ExecContext(service.ctx, `UPDATE library_verification_state SET
		total=(SELECT count(*) FROM library_verification_items WHERE run_id=?),
		pending=(SELECT count(*) FROM library_verification_items WHERE run_id=? AND status IN ('pending','running')),
		verified=(SELECT count(*) FROM library_verification_items WHERE run_id=? AND status='verified'),
		failed=(SELECT count(*) FROM library_verification_items WHERE run_id=? AND status='failed'),
		unverified=(SELECT count(*) FROM library_verification_items WHERE run_id=? AND status='unverified'),
		state=CASE WHEN EXISTS(SELECT 1 FROM library_verification_items WHERE run_id=? AND status IN ('pending','running')) THEN 'queued' ELSE 'complete' END,
		reason='',current_track_id='',current_track_title='',error='',updated_at=? WHERE singleton=1 AND run_id=?`,
		item.runID, item.runID, item.runID, item.runID, item.runID, item.runID, timestamp(time.Now()), item.runID); err != nil {
		return false, err
	}
	recorded := false
	if history != nil && (result.status == "failed" || result.status == "unverified") {
		recorded, err = history.RecordInTransaction(service.ctx, tx, verificationProblemEvent(item, result))
		if err != nil {
			return false, err
		}
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	if recorded {
		service.notify("history")
	}
	return true, nil
}

func verificationProblemEvent(item verificationItem, result verificationResult) errorhistory.Event {
	outcome := "unknown"
	if result.status == "failed" {
		outcome = "failed"
	}
	return errorhistory.Event{
		ReceivedAt: time.Now().UTC(),
		Kind:       "integrity", TrackID: item.trackID, TrackTitle: boundedVerificationText(item.title, 256),
		RootName: boundedVerificationText(item.rootName, 256), RelativePath: boundedVerificationText(item.relativePath, 4096),
		Stage: "verification", Code: result.code, Message: result.message,
		Outcome: outcome, Details: result.details,
		Key: "integrity:" + item.scanID + ":" + item.trackID + ":" + fmt.Sprintf("%d:%d", item.byteSize, item.modifiedNS),
	}
}

func boundedVerificationText(value string, maximum int) string {
	value = strings.ToValidUTF8(strings.ReplaceAll(value, "\x00", ""), "\uFFFD")
	if len(value) <= maximum {
		return value
	}
	end := maximum - 3
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + "..."
}
