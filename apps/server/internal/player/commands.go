package player

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type commandRecord struct {
	id           string
	action       string
	entryID      string
	positionMS   int64
	startedAt    time.Time
	errorInfo    diagnosticErrorInfo
	initialState storedState
}

type startResult struct {
	entry              queueRecord
	track              library.Track
	playID             string
	resource           output.Resource
	positionMS         int64
	playAcknowledgedAt time.Time
}

func (s *Service) Command(ctx context.Context, command Command) (State, error) {
	if err := validCommand(command); err != nil {
		return State{}, err
	}
	commandID, err := randomID("command")
	if err != nil {
		return State{}, fmt.Errorf("player: create command identity: %w", err)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)

	s.opMu.Lock()
	err = s.acceptCommandLocked(ctx, commandID, now, command)
	s.opMu.Unlock()
	if err != nil {
		return State{}, err
	}
	s.notify("player")
	s.signalWorker()
	return s.Snapshot(ctx)
}

// StopForRestart stops active playback and waits for the renderer command to
// reach a durable terminal result. A restart must not tear down the runtime
// while the physical renderer outcome is still unknown.
func (s *Service) StopForRestart(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	commandID, err := randomID("command")
	if err != nil {
		return fmt.Errorf("player: create restart stop identity: %w", err)
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	s.opMu.Lock()
	st, err := s.loadState(ctx)
	var active int
	if err == nil {
		err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_commands WHERE service_epoch=? AND status IN ('pending','running')", s.epoch).Scan(&active)
	}
	if err == nil && active != 0 {
		err = fault.New(409, "COMMAND_IN_PROGRESS", "Wait for the active playback command to finish.")
	}
	if err == nil && st.state == StateStopped {
		if st.rendererID != "" && st.resumeRequired && st.errorMessage != "" {
			err = fmt.Errorf("player: previous renderer stop is unconfirmed: %s", st.errorMessage)
		} else {
			s.opMu.Unlock()
			return nil
		}
	}
	if err == nil {
		err = s.acceptCommandLocked(ctx, commandID, now, Command{Action: "stop"})
	}
	s.opMu.Unlock()
	if err != nil {
		return err
	}
	s.notify("player")
	s.signalWorker()

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var status, message string
		err = s.db.QueryRowContext(ctx, "SELECT status,error FROM player_commands WHERE command_id=?", commandID).Scan(&status, &message)
		if err != nil {
			return fmt.Errorf("player: inspect restart stop: %w", err)
		}
		switch status {
		case "succeeded":
			return nil
		case "failed", "unknown":
			if message == "" {
				message = "The renderer stop did not produce a confirmed result."
			}
			return fmt.Errorf("player: restart stop was not confirmed: %s", message)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("player: wait for restart stop: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func validCommand(command Command) error {
	switch command.Action {
	case "play", "pause", "stop", "next", "previous":
		if command.PositionMS != 0 {
			return fault.New(400, "INVALID_PLAYER_COMMAND", "A position is only valid for seek.")
		}
	case "seek":
		if command.EntryID != "" || command.PositionMS < 0 {
			return fault.New(400, "INVALID_PLAYER_COMMAND", "The seek position is invalid.")
		}
	default:
		return fault.New(400, "INVALID_PLAYER_COMMAND", "The playback action is not supported.")
	}
	if command.Action != "play" && command.EntryID != "" {
		return fault.New(400, "INVALID_PLAYER_COMMAND", "A queue entry is only valid for play.")
	}
	return nil
}

func (s *Service) acceptCommandLocked(ctx context.Context, commandID, now string, command Command) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("player: begin command: %w", err)
	}
	defer tx.Rollback()
	var st storedState
	var seekable, resume int
	if err := tx.QueryRowContext(ctx, `SELECT revision,queue_revision,renderer_id,current_entry_id,play_id,current_uri,current_seekable,state,position_ms,duration_ms,resume_required FROM player_state WHERE singleton=1`).Scan(
		&st.revision, &st.queueRevision, &st.rendererID, &st.currentEntryID, &st.playID, &st.currentURI,
		&seekable, &st.state, &st.positionMS, &st.durationMS, &resume,
	); err != nil {
		return fmt.Errorf("player: load command state: %w", err)
	}
	st.currentSeekable = seekable != 0
	st.resumeRequired = resume != 0
	if command.Action == "stop" {
		const superseded = "Automatic media-failure advance was superseded by explicit Stop."
		if _, err := tx.ExecContext(ctx, `UPDATE player_commands SET status='failed',error=?,completed_at=? WHERE service_epoch=? AND action='error_next' AND status='pending'`, superseded, now, s.epoch); err != nil {
			return fmt.Errorf("player: supersede automatic advance: %w", err)
		}
	}
	var active int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_commands WHERE service_epoch=? AND status IN ('pending','running')", s.epoch).Scan(&active); err != nil {
		return fmt.Errorf("player: inspect command queue: %w", err)
	}
	if active != 0 {
		return fault.New(409, "COMMAND_IN_PROGRESS", "Wait for the active playback command to finish.")
	}
	if st.rendererID == "" {
		return fault.New(409, "OUTPUT_REQUIRED", "Select a renderer before controlling playback.")
	}
	if command.EntryID != "" {
		var exists int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_queue WHERE entry_id=?", command.EntryID).Scan(&exists); err != nil {
			return fmt.Errorf("player: inspect requested queue entry: %w", err)
		}
		if exists == 0 {
			return fault.New(404, "QUEUE_ENTRY_NOT_FOUND", "The requested queue entry no longer exists.")
		}
	}
	switch command.Action {
	case "pause":
		if st.state != StatePlaying && st.state != StateStarting {
			return fault.New(409, "INVALID_PLAYER_STATE", "Playback is not currently playing.")
		}
	case "seek":
		if st.currentEntryID == "" {
			return fault.New(409, "NO_CURRENT_TRACK", "There is no current track to seek.")
		}
		if st.state != StatePlaying && st.state != StatePaused && st.state != StateStarting {
			return fault.New(409, "INVALID_PLAYER_STATE", "Playback is not active, so it cannot seek.")
		}
		if st.durationMS <= 0 {
			return fault.New(409, "SEEK_UNAVAILABLE", "The current stream duration is unknown, so it cannot seek safely.")
		}
		if command.PositionMS > st.durationMS {
			return fault.New(400, "INVALID_SEEK", "The seek position is beyond the end of the track.")
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_commands(command_id,service_epoch,action,entry_id,position_ms,status,error,created_at,started_at,completed_at) VALUES(?,?,?,?,?,'pending','',?,'','')`, commandID, s.epoch, command.Action, command.EntryID, command.PositionMS, now); err != nil {
		return fmt.Errorf("player: persist command intent: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE player_state SET revision=revision+1,error='' WHERE singleton=1"); err != nil {
		return fmt.Errorf("player: publish command intent: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("player: commit command intent: %w", err)
	}
	s.resetListeningInterval(st.playID)
	s.logCommandAccepted(commandID, command.Action, st.rendererID, st.playID, st.currentEntryID, command.EntryID, "request", st.state, st.revision+1, command.PositionMS)
	return nil
}

func (s *Service) commandLoop(ctx context.Context) {
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
		for {
			command, found, err := s.claimCommand(ctx)
			if err != nil || !found {
				break
			}
			s.executeSafely(ctx, command)
		}
	}
}

func (s *Service) claimCommand(ctx context.Context) (commandRecord, bool, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return commandRecord{}, false, err
	}
	defer tx.Rollback()
	var command commandRecord
	err = tx.QueryRowContext(ctx, `SELECT command_id,action,entry_id,position_ms FROM player_commands WHERE service_epoch=? AND status='pending' ORDER BY created_at LIMIT 1`, s.epoch).Scan(&command.id, &command.action, &command.entryID, &command.positionMS)
	if err == sql.ErrNoRows {
		return commandRecord{}, false, nil
	}
	if err != nil {
		return commandRecord{}, false, err
	}
	startedAt := s.now().UTC()
	command.startedAt = startedAt
	result, err := tx.ExecContext(ctx, "UPDATE player_commands SET status='running',started_at=? WHERE command_id=? AND status='pending'", startedAt.Format(time.RFC3339Nano), command.id)
	if err != nil {
		return commandRecord{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return commandRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return commandRecord{}, false, err
	}
	return command, true, nil
}

func (s *Service) executeSafely(runCtx context.Context, command commandRecord) {
	defer s.ensureCommandTerminal(command)
	defer func() {
		if recover() != nil {
			command.errorInfo = diagnosticError(nil, "panic")
			s.completeUnknown(command, "The renderer command ended unexpectedly; its outcome is unknown.")
		}
	}()
	ctx, cancel := context.WithTimeout(runCtx, commandTimeout)
	defer cancel()
	if err := s.executeCommand(ctx, command); err != nil {
		command.errorInfo = diagnosticError(err, "internal")
		if ctx.Err() != nil || rendererOutcomeUnknown(err) {
			s.completeUnknown(command, rendererUnknownOutcomeMessage(err))
			return
		}
		s.completeFailure(command, rendererFailureMessage(command.action, err), false, "")
	}
}

func (s *Service) executeCommand(ctx context.Context, command commandRecord) error {
	st, err := s.loadState(ctx)
	if err != nil {
		return err
	}
	command.initialState = st
	if command.action == "stop" && recoveredPlaybackIntent(st) {
		if st.playID != "" {
			s.media.Revoke(st.playID)
		}
		command.errorInfo = diagnosticError(nil, "recovery_unconfirmed")
		const message = "Server playback stopped and media access was revoked, but physical renderer playback could not be confirmed after recovery."
		return s.completeUnconfirmedStop(command, st, message)
	}
	device, found := s.devices.Device(st.rendererID)
	if !found || !device.Online {
		if command.action == "stop" {
			if st.playID != "" {
				s.media.Revoke(st.playID)
			}
			command.errorInfo = diagnosticError(nil, "offline")
			const message = "Server playback stopped and media access was revoked, but the offline renderer could not confirm physical stop."
			return s.completeUnconfirmedStop(command, st, message)
		}
		if st.playID != "" {
			s.media.Revoke(st.playID)
		}
		command.errorInfo = diagnosticError(nil, "offline")
		s.completeFailure(command, "The selected renderer is offline. Reconnect it and press Play to resume.", true, "")
		return nil
	}
	switch command.action {
	case "play", "natural_next", "error_next":
		return s.executePlay(ctx, command, st, device)
	case "pause":
		if !device.Capabilities.Pause {
			s.completeFailure(command, "The selected renderer does not support pause.", false, "")
			return nil
		}
		if err := s.callRenderer(func() error { return s.devices.Pause(ctx, device.ID) }); err != nil {
			return err
		}
		return s.completeSuccess(command, successUpdate{})
	case "stop":
		if !device.Capabilities.Stop {
			s.completeFailure(command, "The selected renderer does not support stop.", false, "")
			return nil
		}
		if err := s.callRenderer(func() error { return s.devices.Stop(ctx, device.ID) }); err != nil {
			return err
		}
		if st.playID != "" {
			s.media.Revoke(st.playID)
		}
		return s.completeSuccess(command, successUpdate{state: StateStopped, setState: true, resetPosition: true, queueEntryID: st.currentEntryID, queueStatus: EntryPending, controlAction: "stop"})
	case "next":
		return s.executeNext(ctx, command, st, device)
	case "previous":
		return s.executePrevious(ctx, command, st, device)
	case "seek":
		if !device.Capabilities.Seek || !st.currentSeekable {
			s.completeFailure(command, "This renderer or stream does not support seeking.", false, "")
			return nil
		}
		if st.durationMS > 0 && command.positionMS > st.durationMS {
			s.completeFailure(command, "The seek position is beyond the end of the track.", false, "")
			return nil
		}
		if err := s.callRenderer(func() error { return s.devices.Seek(ctx, device.ID, command.positionMS) }); err != nil {
			return err
		}
		return s.completeSuccess(command, successUpdate{})
	default:
		return fmt.Errorf("unknown persisted player command")
	}
}

func (s *Service) executePlay(ctx context.Context, command commandRecord, st storedState, device output.Device) error {
	target, found, err := s.resolvePlayTarget(ctx, command.entryID, st.currentEntryID)
	if err != nil {
		return err
	}
	if !found {
		s.completeFailure(command, "The queue is empty.", false, "")
		return nil
	}
	if command.action == "play" && st.playID != "" && st.currentURI != "" && !st.resumeRequired && target.id == st.currentEntryID && (st.state == StatePlaying || st.state == StateStarting) {
		return s.completeSuccess(command, successUpdate{queueEntryID: target.id, queueStatus: EntryPlaying})
	}
	if command.action == "play" && st.state == StatePaused && !st.resumeRequired && target.id == st.currentEntryID {
		var playAcknowledgedAt time.Time
		if err := s.callRenderer(func() error {
			err := s.devices.Play(ctx, device.ID)
			if err == nil {
				playAcknowledgedAt = s.now()
			}
			return err
		}); err != nil {
			if isMediaFailure(err) {
				command.errorInfo = diagnosticError(err, "start_failed")
				s.media.Revoke(st.playID)
				return s.completeMediaStartFailure(command, target, rendererFailureMessage("play", err))
			}
			return err
		}
		return s.completeSuccess(command, successUpdate{
			state: StateStarting, setState: true, queueEntryID: target.id, queueStatus: EntryPlaying,
			startup: startupObservationEvidence{playID: st.playID, deadline: playAcknowledgedAt.Add(commandTimeout)},
		})
	}
	controlAction := ""
	if st.playID != "" {
		if recoveredPlaybackIntent(st) {
			s.media.Revoke(st.playID)
		} else {
			if !device.Capabilities.Stop {
				s.completeFailure(command, "Stop is required to change tracks, but this renderer does not support it.", false, target.id)
				return nil
			}
			if err := s.callRenderer(func() error { return s.devices.Stop(ctx, device.ID) }); err != nil {
				return err
			}
			s.media.Revoke(st.playID)
			controlAction = "switch"
		}
	}
	resumePositionMS := int64(0)
	if command.action == "play" && st.resumeRequired && target.id == st.currentEntryID {
		resumePositionMS = st.positionMS
	}
	started, err := s.startEntry(ctx, device, target, resumePositionMS)
	if err != nil {
		if rendererOutcomeUnknown(err) {
			return err
		}
		command.errorInfo = diagnosticError(err, "start_failed")
		if isMediaFailure(err) {
			return s.completeMediaStartFailure(command, target, rendererFailureMessage("play", err))
		}
		s.completeFailure(command, rendererFailureMessage("play", err), false, target.id)
		return nil
	}
	oldStatus := EntryPending
	if command.action == "natural_next" && st.currentEntryID != "" && st.currentEntryID != target.id {
		oldStatus = EntryCompleted
	}
	return s.completeStart(command, st.currentEntryID, oldStatus, started, controlAction)
}

func (s *Service) executeNext(ctx context.Context, command commandRecord, st storedState, device output.Device) error {
	target, found, err := s.adjacentEntry(ctx, st.currentEntryID, 1, true)
	if err != nil {
		return err
	}
	controlAction := ""
	if st.playID != "" {
		if recoveredPlaybackIntent(st) {
			s.media.Revoke(st.playID)
		} else {
			if !device.Capabilities.Stop {
				s.completeFailure(command, "The selected renderer does not support stop, so it cannot advance safely.", false, "")
				return nil
			}
			if err := s.callRenderer(func() error { return s.devices.Stop(ctx, device.ID) }); err != nil {
				return err
			}
			s.media.Revoke(st.playID)
			controlAction = "next"
		}
	}
	if !found {
		return s.completeSuccess(command, successUpdate{state: StateStopped, setState: true, resetPosition: true, queueEntryID: st.currentEntryID, queueStatus: EntryCompleted, controlAction: controlAction})
	}
	started, err := s.startEntry(ctx, device, target, 0)
	if err != nil {
		if rendererOutcomeUnknown(err) {
			return err
		}
		command.errorInfo = diagnosticError(err, "start_failed")
		if isMediaFailure(err) {
			return s.completeMediaStartFailure(command, target, rendererFailureMessage("next", err))
		}
		return s.completeAdvanceFailure(command, st.currentEntryID, target.id, rendererFailureMessage("next", err))
	}
	return s.completeStart(command, st.currentEntryID, EntryCompleted, started, controlAction)
}

func (s *Service) executePrevious(ctx context.Context, command commandRecord, st storedState, device output.Device) error {
	if st.currentEntryID == "" {
		s.completeFailure(command, "The queue has no current track.", false, "")
		return nil
	}
	if st.positionMS > 5000 && !recoveredPlaybackIntent(st) && device.Capabilities.Seek && st.currentSeekable {
		if err := s.callRenderer(func() error { return s.devices.Seek(ctx, device.ID, 0) }); err != nil {
			return err
		}
		return s.completeSuccess(command, successUpdate{})
	}
	target, found, err := s.adjacentEntry(ctx, st.currentEntryID, -1, false)
	if err != nil {
		return err
	}
	if !found {
		target, found, err = s.currentEntry(ctx, st.currentEntryID)
		if err != nil {
			return err
		}
	}
	if !found {
		s.completeFailure(command, "The current queue entry no longer exists.", false, "")
		return nil
	}
	controlAction := ""
	if st.playID != "" {
		if recoveredPlaybackIntent(st) {
			s.media.Revoke(st.playID)
		} else {
			if !device.Capabilities.Stop {
				s.completeFailure(command, "Stop is required to restart a track, but this renderer does not support it.", false, "")
				return nil
			}
			if err := s.callRenderer(func() error { return s.devices.Stop(ctx, device.ID) }); err != nil {
				return err
			}
			s.media.Revoke(st.playID)
			controlAction = "previous"
		}
	}
	started, err := s.startEntry(ctx, device, target, 0)
	if err != nil {
		if rendererOutcomeUnknown(err) {
			return err
		}
		command.errorInfo = diagnosticError(err, "start_failed")
		if isMediaFailure(err) {
			return s.completeMediaStartFailure(command, target, rendererFailureMessage("previous", err))
		}
		s.completeFailure(command, rendererFailureMessage("previous", err), false, target.id)
		return nil
	}
	return s.completeStart(command, st.currentEntryID, EntryPending, started, controlAction)
}

func (s *Service) startEntry(ctx context.Context, device output.Device, entry queueRecord, resumePositionMS int64) (startResult, error) {
	track, err := s.lib.Track(ctx, entry.trackID)
	if err != nil {
		return startResult{}, &playbackStartError{stage: "LoadTrack", cause: err}
	}
	if !track.Available {
		return startResult{}, &playbackStartError{stage: "LoadTrack", cause: fault.New(409, "TRACK_UNAVAILABLE", "The selected track is unavailable.")}
	}
	playID, err := randomID("play")
	if err != nil {
		return startResult{}, &playbackStartError{stage: "CreatePlayID", cause: err}
	}
	resource, err := s.media.Prepare(ctx, device, track, playID)
	if err != nil {
		return startResult{}, &playbackStartError{stage: "PrepareMedia", cause: err}
	}
	if resumePositionMS > 0 && (!device.Capabilities.Seek || !resource.Seekable) {
		s.media.Revoke(playID)
		return startResult{}, &playbackStartError{stage: "Resume", cause: fault.New(409, "SEEK_UNAVAILABLE", "The saved playback position cannot be resumed on this output.")}
	}
	var playAcknowledgedAt time.Time
	err = func() error {
		s.rendererMu.Lock()
		defer s.rendererMu.Unlock()
		if setErr := s.devices.SetURI(ctx, device.ID, resource); setErr != nil {
			return &playbackStartError{stage: "SetURI", cause: setErr}
		}
		if resumePositionMS > 0 {
			if seekErr := s.devices.Seek(ctx, device.ID, resumePositionMS); seekErr != nil {
				return &playbackStartError{stage: "Seek", cause: seekErr}
			}
		}
		if playErr := s.devices.Play(ctx, device.ID); playErr != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = s.devices.Stop(stopCtx, device.ID)
			cancel()
			return &playbackStartError{stage: "Play", cause: playErr}
		}
		playAcknowledgedAt = s.now()
		return nil
	}()
	if err != nil {
		s.media.Revoke(playID)
		return startResult{}, err
	}
	return startResult{entry: entry, track: track, playID: playID, resource: resource, positionMS: resumePositionMS, playAcknowledgedAt: playAcknowledgedAt}, nil
}

func (s *Service) callRenderer(call func() error) error {
	s.rendererMu.Lock()
	defer s.rendererMu.Unlock()
	return call()
}

func rendererOutcomeUnknown(err error) bool {
	var actionErr *output.ActionError
	if !errors.As(err, &actionErr) {
		return false
	}
	switch actionErr.Kind {
	case output.ErrorTimeout, output.ErrorTransport, output.ErrorResponse, output.ErrorCancelled:
		return true
	default:
		return false
	}
}

func isMediaFailure(err error) bool {
	var actionErr *output.ActionError
	if errors.As(err, &actionErr) && actionErr.Kind == output.ErrorMedia {
		return true
	}
	var publicError *fault.Error
	return errors.As(err, &publicError) &&
		(publicError.Code == "TRACK_UNAVAILABLE" || publicError.Code == "NOT_FOUND")
}

type safeRendererError interface {
	SafeRendererError() string
}

type playbackStartError struct {
	stage string
	cause error
}

func (err *playbackStartError) Error() string { return err.SafeRendererError() }

func (err *playbackStartError) Unwrap() error { return err.cause }

func (err *playbackStartError) SafeRendererError() string {
	message := err.stage + " failed."
	if detail := safeRendererErrorDetail(err.cause); detail != "" {
		message += " " + detail
	}
	return message
}

func rendererUnknownOutcomeMessage(err error) string {
	message := "The renderer did not confirm the command; its outcome is unknown."
	var actionError *output.ActionError
	if errors.As(err, &actionError) {
		message = fmt.Sprintf("The renderer did not confirm %s (%s); its outcome is unknown.", actionError.Action, actionError.Kind)
	}
	if detail := safeRendererErrorDetail(err); detail != "" {
		message += " " + detail
	}
	return message
}

func rendererFailureMessage(action string, err error) string {
	message := commandFailureMessage(action)
	if detail := safeRendererErrorDetail(err); detail != "" {
		message += " " + detail
	}
	return message
}

func safeRendererErrorDetail(err error) string {
	var detail safeRendererError
	if errors.As(err, &detail) {
		return detail.SafeRendererError()
	}
	var actionError *output.ActionError
	if errors.As(err, &actionError) {
		return actionError.Error()
	}
	var publicError *fault.Error
	if errors.As(err, &publicError) {
		return publicError.Code + ": " + publicError.Message
	}
	return ""
}

func (s *Service) resolvePlayTarget(ctx context.Context, requested, current string) (queueRecord, bool, error) {
	if requested != "" {
		return s.entryByID(ctx, requested)
	}
	if current != "" {
		if entry, found, err := s.currentEntry(ctx, current); found || err != nil {
			return entry, found, err
		}
	}
	return s.adjacentEntry(ctx, "", 1, false)
}

func (s *Service) currentEntry(ctx context.Context, entryID string) (queueRecord, bool, error) {
	entry, found, err := s.entryByID(ctx, entryID)
	if found || err != nil {
		return entry, found, err
	}
	err = s.db.QueryRowContext(ctx, "SELECT entry_id,track_id,queue_index FROM player_current WHERE singleton=1 AND entry_id=?", entryID).Scan(
		&entry.id, &entry.trackID, &entry.position,
	)
	if err == sql.ErrNoRows {
		return queueRecord{}, false, nil
	}
	if err != nil {
		return queueRecord{}, false, err
	}
	entry.status = EntryPending
	return entry, true, nil
}

func (s *Service) entryByID(ctx context.Context, entryID string) (queueRecord, bool, error) {
	var entry queueRecord
	err := s.db.QueryRowContext(ctx, "SELECT entry_id,track_id,status,position FROM player_queue WHERE entry_id=?", entryID).Scan(&entry.id, &entry.trackID, &entry.status, &entry.position)
	if err == sql.ErrNoRows {
		return queueRecord{}, false, nil
	}
	if err != nil {
		return queueRecord{}, false, err
	}
	return entry, true, nil
}

func commandFailureMessage(action string) string {
	switch action {
	case "play", "natural_next", "error_next":
		return "The renderer could not start this track. The failed entry was retained."
	case "pause":
		return "The renderer could not pause playback."
	case "stop":
		return "The renderer did not confirm stop. Its playback state may be unknown."
	case "next":
		return "The renderer could not advance to the next track. The queue was preserved."
	case "previous":
		return "The renderer could not return to the previous track. The queue was preserved."
	case "seek":
		return "The renderer could not seek to that position."
	default:
		return "The renderer command failed."
	}
}

type successUpdate struct {
	state         string
	setState      bool
	resetPosition bool
	queueEntryID  string
	queueStatus   string
	controlAction string
	startup       startupObservationEvidence
}

func (s *Service) completeSuccess(command commandRecord, update successUpdate) error {
	finished := s.now().UTC()
	now := finished.Format(time.RFC3339Nano)
	s.opMu.Lock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	st := command.initialState
	if err == nil {
		setParts := "revision=revision+1,error='',resume_required=0,control_action=?,control_at=?"
		controlAt := ""
		if update.controlAction != "" {
			controlAt = now
		}
		args := []any{update.controlAction, controlAt}
		if update.setState {
			setParts += ",state=?"
			args = append(args, update.state)
			if update.state == StateStopped {
				setParts += ",play_id='',current_uri='',current_seekable=0"
			}
		}
		if update.resetPosition {
			setParts += ",position_ms=0"
		}
		_, err = tx.ExecContext(context.Background(), "UPDATE player_state SET "+setParts+" WHERE singleton=1", args...)
		if err == nil && update.queueEntryID != "" && update.queueStatus != "" {
			result, updateErr := tx.ExecContext(context.Background(), "UPDATE player_queue SET status=? WHERE entry_id=? AND status<>?", update.queueStatus, update.queueEntryID, update.queueStatus)
			err = updateErr
			if err == nil {
				if changed, rowsErr := result.RowsAffected(); rowsErr == nil && changed > 0 {
					_, err = tx.ExecContext(context.Background(), "UPDATE player_state SET queue_revision=queue_revision+1 WHERE singleton=1")
				}
			}
		}
		if err == nil {
			_, err = tx.ExecContext(context.Background(), "UPDATE player_commands SET status='succeeded',error='',completed_at=? WHERE command_id=? AND status='running'", now, command.id)
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	if err == nil {
		if update.setState && update.state == StateStopped {
			s.startup = startupObservationEvidence{}
		} else if update.startup.playID != "" {
			s.startup = update.startup
		}
		if update.setState && update.state == StateStopped {
			s.listening = listeningEvidence{}
		} else {
			s.resetListeningInterval(st.playID)
		}
	}
	if err != nil && tx != nil {
		_ = tx.Rollback()
	}
	s.opMu.Unlock()
	if err != nil {
		return err
	}
	resultState := st.state
	resultPosition := st.positionMS
	resultPlayID := st.playID
	if update.setState {
		resultState = update.state
		if update.state == StateStopped {
			resultPlayID = ""
		}
	}
	if update.resetPosition {
		resultPosition = 0
	}
	resultEntryID := st.currentEntryID
	if update.queueEntryID != "" {
		resultEntryID = update.queueEntryID
	}
	s.logCommandTerminal(command, "succeeded", "renderer_acknowledged", st, resultPlayID, resultEntryID, resultState, resultPosition, diagnosticErrorInfo{}, finished)
	s.notify("player")
	if update.queueEntryID != "" && update.queueStatus != "" {
		s.notify("queue")
	}
	s.signalObserver()
	return nil
}

func (s *Service) completeStart(command commandRecord, oldEntryID, oldStatus string, started startResult, controlAction string) error {
	finished := s.now().UTC()
	now := finished.Format(time.RFC3339Nano)
	controlAt := ""
	if controlAction != "" {
		controlAt = now
	}
	s.opMu.Lock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	st := command.initialState
	queueChanged := false
	if err == nil {
		err = recordTraversalSelectionTx(context.Background(), tx, oldEntryID, started.entry.id)
	}
	if err == nil && oldEntryID != "" && oldEntryID != started.entry.id {
		var result sql.Result
		result, err = tx.ExecContext(context.Background(), "UPDATE player_queue SET status=? WHERE entry_id=? AND status<>?", oldStatus, oldEntryID, oldStatus)
		if err == nil {
			var changed int64
			changed, err = result.RowsAffected()
			queueChanged = changed > 0
		}
	}
	if err == nil {
		var result sql.Result
		result, err = tx.ExecContext(context.Background(), "UPDATE player_queue SET status='playing' WHERE entry_id=? AND status<>'playing'", started.entry.id)
		if err == nil {
			var changed int64
			changed, err = result.RowsAffected()
			queueChanged = queueChanged || changed > 0
		}
	}
	if err == nil {
		err = selectCurrentBindingTx(context.Background(), tx, started.entry.id)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,current_entry_id=?,play_id=?,current_uri=?,current_seekable=?,state='starting',position_ms=?,duration_ms=?,observed_at='',last_observed_state='',last_observed_uri='',last_observed_position_ms=0,last_observed_duration_ms=0,last_observed_has_position=0,last_observed_at='',resume_required=0,error='',control_action=?,control_at=? WHERE singleton=1`, boolInt(queueChanged), started.entry.id, started.playID, started.resource.URL, boolInt(started.resource.Seekable), started.positionMS, chooseDuration(started.resource.DurationMS, started.track.DurationMS), controlAction, controlAt)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), "UPDATE player_commands SET status='succeeded',error='',completed_at=? WHERE command_id=? AND status='running'", now, command.id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		s.startup = startupObservationEvidence{
			playID: started.playID, deadline: started.playAcknowledgedAt.Add(commandTimeout),
		}
		s.listening = listeningEvidence{
			playID: started.playID, trackID: started.entry.trackID,
			durationMS: chooseDuration(started.resource.DurationMS, started.track.DurationMS),
		}
	}
	if err != nil && tx != nil {
		_ = tx.Rollback()
	}
	s.opMu.Unlock()
	if err != nil {
		return err
	}
	s.logCommandTerminal(command, "succeeded", "renderer_acknowledged", st, started.playID, started.entry.id, StateStarting, started.positionMS, diagnosticErrorInfo{}, finished)
	if queueChanged {
		s.notify("queue")
	}
	s.notify("player")
	s.signalObserver()
	return nil
}

func (s *Service) completeMediaStartFailure(command commandRecord, target queueRecord, message string) error {
	finished := s.now().UTC()
	now := finished.Format(time.RFC3339Nano)
	s.opMu.Lock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	st := command.initialState
	queueChanged := false
	if err == nil {
		err = recordTraversalSelectionTx(context.Background(), tx, st.currentEntryID, target.id)
	}
	if err == nil {
		oldStatus := EntryPending
		if command.action == "next" || command.action == "natural_next" {
			oldStatus = EntryCompleted
		}
		result, updateErr := tx.ExecContext(context.Background(), "UPDATE player_queue SET status=? WHERE status='playing' AND entry_id<>?", oldStatus, target.id)
		err = updateErr
		if err == nil {
			changed, rowsErr := result.RowsAffected()
			err = rowsErr
			queueChanged = changed > 0
		}
	}
	if err == nil {
		result, updateErr := tx.ExecContext(context.Background(), "UPDATE player_queue SET status='error' WHERE entry_id=? AND status<>'error'", target.id)
		err = updateErr
		if err == nil {
			changed, rowsErr := result.RowsAffected()
			err = rowsErr
			queueChanged = queueChanged || changed > 0
		}
	}
	var nextEntryID string
	if err == nil {
		next, found, selectErr := s.adjacentEntryTx(context.Background(), tx, target.id, 1, false)
		err = selectErr
		if found {
			nextEntryID = next.id
		}
	}
	var nextCommandID string
	if err == nil && nextEntryID != "" {
		nextCommandID, err = randomID("command")
	}
	if err == nil && nextCommandID != "" {
		_, err = tx.ExecContext(context.Background(), `INSERT INTO player_commands(command_id,service_epoch,action,entry_id,position_ms,status,error,created_at,started_at,completed_at) VALUES(?,?, 'error_next',?,0,'pending','',?,'','')`, nextCommandID, s.epoch, nextEntryID, now)
	}
	state := StateError
	controlAction := ""
	playerMessage := message
	if nextEntryID != "" {
		state = StateStarting
		controlAction = "error_next"
		playerMessage = ""
	}
	if err == nil {
		selectedEntryID := target.id
		if nextEntryID != "" {
			selectedEntryID = nextEntryID
		}
		err = selectCurrentBindingTx(context.Background(), tx, selectedEntryID)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,current_entry_id=CASE WHEN ?<>'' THEN ? ELSE ? END,play_id='',current_uri='',current_seekable=0,state=?,position_ms=0,duration_ms=0,resume_required=0,error=?,control_action=?,control_at=? WHERE singleton=1`, boolInt(queueChanged), nextEntryID, nextEntryID, target.id, state, playerMessage, controlAction, now)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), "UPDATE player_commands SET status='failed',error=?,completed_at=? WHERE command_id=? AND status='running'", message, now, command.id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		s.startup = startupObservationEvidence{}
		s.listening = listeningEvidence{}
	}
	if err != nil && tx != nil {
		_ = tx.Rollback()
	}
	s.opMu.Unlock()
	if err != nil {
		return err
	}
	if command.errorInfo.category == "" {
		command.errorInfo = diagnosticError(nil, "media")
	}
	s.logCommandTerminal(command, "failed", "media_failed", st, "", target.id, state, 0, command.errorInfo, finished)
	s.recordPlayerHistory(s.commandHistoryRecord(command, st, target.id, message, "failed", finished))
	if nextCommandID != "" {
		s.logCommandAccepted(nextCommandID, "error_next", st.rendererID, "", target.id, nextEntryID, "media_failure", state, st.revision+1, 0)
		s.signalWorker()
	}
	s.notify("queue")
	s.notify("player")
	return nil
}

func (s *Service) completeAdvanceFailure(command commandRecord, oldEntryID, targetEntryID, message string) error {
	finished := s.now().UTC()
	now := finished.Format(time.RFC3339Nano)
	s.opMu.Lock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	st := command.initialState
	if err == nil {
		err = recordTraversalSelectionTx(context.Background(), tx, oldEntryID, targetEntryID)
	}
	if err == nil && oldEntryID != "" {
		_, err = tx.ExecContext(context.Background(), "UPDATE player_queue SET status='completed' WHERE entry_id=?", oldEntryID)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), "UPDATE player_queue SET status='error' WHERE entry_id=?", targetEntryID)
	}
	if err == nil {
		err = selectCurrentBindingTx(context.Background(), tx, targetEntryID)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+1,current_entry_id=?,play_id='',current_uri='',current_seekable=0,state='error',position_ms=0,resume_required=1,error=?,control_action='next',control_at=? WHERE singleton=1`, targetEntryID, message, now)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), "UPDATE player_commands SET status='failed',error=?,completed_at=? WHERE command_id=? AND status='running'", message, now, command.id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		s.startup = startupObservationEvidence{}
		s.listening = listeningEvidence{}
	}
	if err != nil && tx != nil {
		_ = tx.Rollback()
	}
	s.opMu.Unlock()
	if err == nil {
		if command.errorInfo.category == "" {
			command.errorInfo = diagnosticError(nil, "start_failed")
		}
		s.logCommandTerminal(command, "failed", "next_track_start_failed", st, "", targetEntryID, StateError, 0, command.errorInfo, finished)
		s.recordPlayerHistory(s.commandHistoryRecord(command, st, targetEntryID, message, "failed", finished))
		s.notify("queue")
		s.notify("player")
	}
	return err
}

func (s *Service) completeUnconfirmedStop(command commandRecord, st storedState, message string) error {
	finished := s.now().UTC()
	now := finished.Format(time.RFC3339Nano)
	s.opMu.Lock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	queueChanged := false
	if err == nil && st.currentEntryID != "" {
		result, updateErr := tx.ExecContext(context.Background(), "UPDATE player_queue SET status='pending' WHERE entry_id=? AND status='playing'", st.currentEntryID)
		err = updateErr
		if err == nil {
			changed, rowsErr := result.RowsAffected()
			err = rowsErr
			queueChanged = changed > 0
		}
	}
	if err == nil {
		queueIncrement := 0
		if queueChanged {
			queueIncrement = 1
		}
		_, err = tx.ExecContext(context.Background(), `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,state='stopped',play_id='',current_uri='',current_seekable=0,position_ms=0,resume_required=1,error=?,control_action='',control_at='' WHERE singleton=1`, queueIncrement, message)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), "UPDATE player_commands SET status='unknown',error=?,completed_at=? WHERE command_id=? AND status='running'", message, now, command.id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		s.startup = startupObservationEvidence{}
		s.listening = listeningEvidence{}
	}
	if err != nil && tx != nil {
		_ = tx.Rollback()
	}
	s.opMu.Unlock()
	if err == nil {
		if command.errorInfo.category == "" {
			command.errorInfo = diagnosticError(nil, "unconfirmed")
		}
		s.logCommandTerminal(command, "unknown", "stop_unconfirmed", st, "", st.currentEntryID, StateStopped, 0, command.errorInfo, finished)
		s.recordPlayerHistory(s.commandHistoryRecord(command, st, st.currentEntryID, message, "unknown", finished))
		if queueChanged {
			s.notify("queue")
		}
		s.notify("player")
	}
	return err
}

func recoveredPlaybackIntent(st storedState) bool {
	return st.state == StateUnavailable && st.resumeRequired
}

func (s *Service) completeFailure(command commandRecord, message string, unavailable bool, entryID string) {
	finished := s.now().UTC()
	now := finished.Format(time.RFC3339Nano)
	state := StateError
	targetStatus := EntryError
	if unavailable {
		state = StateUnavailable
		targetStatus = EntryPending
	}
	explicitEntry := entryID != ""
	s.opMu.Lock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	queueChanged := false
	var st storedState
	if err == nil {
		err = tx.QueryRowContext(context.Background(), "SELECT revision,renderer_id,current_entry_id,play_id,state,position_ms FROM player_state WHERE singleton=1").Scan(&st.revision, &st.rendererID, &st.currentEntryID, &st.playID, &st.state, &st.positionMS)
		if entryID == "" {
			entryID = st.currentEntryID
		}
	}
	if err == nil && explicitEntry {
		err = recordTraversalSelectionTx(context.Background(), tx, st.currentEntryID, entryID)
	}
	if err == nil {
		result, updateErr := tx.ExecContext(context.Background(), "UPDATE player_queue SET status='pending' WHERE status='playing'")
		err = updateErr
		if err == nil {
			changed, rowsErr := result.RowsAffected()
			err = rowsErr
			queueChanged = changed > 0
		}
	}
	if err == nil && entryID != "" {
		result, updateErr := tx.ExecContext(context.Background(), "UPDATE player_queue SET status=? WHERE entry_id=? AND status<>?", targetStatus, entryID, targetStatus)
		err = updateErr
		if err == nil {
			changed, rowsErr := result.RowsAffected()
			err = rowsErr
			queueChanged = queueChanged || changed > 0
		}
	}
	if err == nil && explicitEntry {
		err = selectCurrentBindingTx(context.Background(), tx, entryID)
	}
	clearPlayback := unavailable || explicitEntry
	if err == nil {
		queueIncrement := 0
		if queueChanged {
			queueIncrement = 1
		}
		cursorChanged := entryID != "" && entryID != st.currentEntryID
		_, err = tx.ExecContext(context.Background(), `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,current_entry_id=CASE WHEN ?<>'' THEN ? ELSE current_entry_id END,position_ms=CASE WHEN ? THEN 0 ELSE position_ms END,duration_ms=CASE WHEN ? THEN 0 ELSE duration_ms END,state=?,resume_required=1,error=?,play_id=CASE WHEN ? THEN '' ELSE play_id END,current_uri=CASE WHEN ? THEN '' ELSE current_uri END,current_seekable=CASE WHEN ? THEN 0 ELSE current_seekable END,control_action='',control_at='' WHERE singleton=1`, queueIncrement, entryID, entryID, cursorChanged, cursorChanged, state, message, clearPlayback, clearPlayback, clearPlayback)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), "UPDATE player_commands SET status='failed',error=?,completed_at=? WHERE command_id=? AND status='running'", message, now, command.id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		s.startup = startupObservationEvidence{}
		if clearPlayback {
			s.listening = listeningEvidence{}
		} else {
			s.resetListeningInterval(st.playID)
		}
	}
	if err != nil && tx != nil {
		_ = tx.Rollback()
	}
	s.opMu.Unlock()
	if err == nil {
		if command.errorInfo.category == "" {
			fallback := "rejected"
			if unavailable {
				fallback = "offline"
			}
			command.errorInfo = diagnosticError(nil, fallback)
		}
		resultPlayID := st.playID
		if clearPlayback {
			resultPlayID = ""
		}
		s.logCommandTerminal(command, "failed", "command_failed", st, resultPlayID, entryID, state, st.positionMS, command.errorInfo, finished)
		s.recordPlayerHistory(s.commandHistoryRecord(command, st, entryID, message, "failed", finished))
		if unavailable {
			s.logRendererUnavailable(st.rendererID, st.playID, st.currentEntryID, st.state, "renderer_missing_or_offline", st.revision+1)
		}
		if queueChanged {
			s.notify("queue")
		}
		s.notify("player")
	}
}

func (s *Service) completeUnknown(command commandRecord, message string) {
	finished := s.now().UTC()
	now := finished.Format(time.RFC3339Nano)
	s.opMu.Lock()
	tx, err := s.db.BeginTx(context.Background(), nil)
	queueChanged := false
	var st storedState
	if err == nil {
		err = tx.QueryRowContext(context.Background(), "SELECT renderer_id,current_entry_id,play_id,state,position_ms FROM player_state WHERE singleton=1").Scan(&st.rendererID, &st.currentEntryID, &st.playID, &st.state, &st.positionMS)
		if err == nil && st.currentEntryID != "" {
			result, updateErr := tx.ExecContext(context.Background(), "UPDATE player_queue SET status='pending' WHERE entry_id=? AND status='playing'", st.currentEntryID)
			err = updateErr
			if err == nil {
				changed, rowsErr := result.RowsAffected()
				err = rowsErr
				queueChanged = changed > 0
			}
		}
	}
	if err == nil {
		queueIncrement := 0
		if queueChanged {
			queueIncrement = 1
		}
		_, err = tx.ExecContext(context.Background(), `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,state=CASE WHEN renderer_id='' THEN 'stopped' ELSE 'unavailable' END,play_id='',current_uri='',current_seekable=0,resume_required=1,error=?,control_action='',control_at='' WHERE singleton=1`, queueIncrement, message)
	}
	if err == nil {
		_, err = tx.ExecContext(context.Background(), "UPDATE player_commands SET status='unknown',error=?,completed_at=? WHERE command_id=? AND status='running'", message, now, command.id)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err == nil {
		s.startup = startupObservationEvidence{}
		s.listening = listeningEvidence{}
	}
	if err != nil && tx != nil {
		_ = tx.Rollback()
	}
	s.opMu.Unlock()
	if err == nil {
		if command.errorInfo.category == "" {
			command.errorInfo = diagnosticError(nil, "unconfirmed")
		}
		resultState := StateUnavailable
		if st.rendererID == "" {
			resultState = StateStopped
		}
		s.logCommandTerminal(command, "unknown", "outcome_unconfirmed", st, "", st.currentEntryID, resultState, st.positionMS, command.errorInfo, finished)
		s.recordPlayerHistory(s.commandHistoryRecord(command, st, st.currentEntryID, message, "unknown", finished))
		if st.playID != "" {
			s.media.Revoke(st.playID)
		}
		if queueChanged {
			s.notify("queue")
		}
		s.notify("player")
	}
}

func (s *Service) ensureCommandTerminal(command commandRecord) {
	const message = "The renderer command finished without a durable outcome; it will not be replayed."
	for attempt := range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var status string
		err := s.db.QueryRowContext(ctx, "SELECT status FROM player_commands WHERE command_id=?", command.id).Scan(&status)
		cancel()
		if err == sql.ErrNoRows || (err == nil && status != "pending" && status != "running") {
			return
		}
		if err == nil {
			s.completeUnknown(command, message)
		}
		if attempt < 2 {
			time.Sleep(25 * time.Millisecond)
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func chooseDuration(resourceDuration, trackDuration int64) int64 {
	if resourceDuration > 0 {
		return resourceDuration
	}
	return trackDuration
}
