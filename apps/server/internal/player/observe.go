package player

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func (s *Service) observationLoop(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-s.observeWake:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}
		s.observeSelected(ctx)
		timer.Reset(s.nextObservationDelay(ctx))
	}
}

func (s *Service) nextObservationDelay(ctx context.Context) time.Duration {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	st, err := s.loadState(ctx)
	if err != nil {
		return s.pollInterval
	}
	delay := adaptiveObservationDelay(s.pollInterval, st)
	if s.startup.playID != "" && s.startup.playID == st.playID {
		remaining := s.startup.deadline.Sub(s.now())
		if remaining > 0 && remaining < delay {
			return remaining
		}
	}
	return delay
}

func adaptiveObservationDelay(configured time.Duration, st storedState) time.Duration {
	if normalizeObservedState(st.lastObservedState) != "playing" || !st.lastObservedHasPosition {
		return configured
	}
	duration := st.lastObservedDurationMS
	if duration <= 0 {
		duration = st.durationMS
	}
	if duration <= 0 || st.lastObservedPositionMS < 0 {
		return configured
	}
	const (
		evidenceLeadMS = int64(2000)
		nearEndPoll    = 500 * time.Millisecond
	)
	remainingMS := duration - st.lastObservedPositionMS
	if remainingMS <= evidenceLeadMS {
		if configured < nearEndPoll {
			return configured
		}
		return nearEndPoll
	}
	delayMS := remainingMS - evidenceLeadMS
	if delayMS >= int64(configured/time.Millisecond) {
		return configured
	}
	delay := time.Duration(delayMS) * time.Millisecond
	if delay < nearEndPoll {
		delay = nearEndPoll
	}
	return delay
}

func (s *Service) observeSelected(ctx context.Context) {
	s.rendererMu.Lock()
	st, err := s.loadState(ctx)
	if err != nil || st.rendererID == "" {
		s.rendererMu.Unlock()
		return
	}
	device, found := s.devices.Device(st.rendererID)
	if !found || !device.Online {
		playID, changed := s.applyRendererUnavailable(ctx, "The selected renderer is offline. Reconnect it and press Play to resume.")
		s.rendererMu.Unlock()
		if playID != "" {
			s.media.Revoke(playID)
		}
		if changed {
			s.notify("queue")
			s.notify("player")
		}
		return
	}
	observeCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	observation, err := s.devices.Observe(observeCtx, st.rendererID)
	cancel()
	if err != nil {
		changed, historyRecord := s.applyObservationFailure(ctx, err)
		s.rendererMu.Unlock()
		s.recordPlayerHistory(historyRecord)
		if changed {
			s.notify("player")
		}
		return
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = s.now().UTC()
	}
	result := s.applyObservation(ctx, observation)
	s.rendererMu.Unlock()
	s.recordPlayerHistory(result.historyRecord)
	if result.revokePlayID != "" {
		s.media.Revoke(result.revokePlayID)
	}
	if result.queueChanged {
		s.notify("queue")
	}
	if result.playerChanged || result.positionChanged {
		s.notify("player")
	}
	if result.libraryChanged {
		s.notify("library")
	}
	if result.commandQueued {
		s.signalWorker()
	}
}

type observationResult struct {
	playerChanged   bool
	queueChanged    bool
	positionChanged bool
	commandQueued   bool
	libraryChanged  bool
	revokePlayID    string
	historyRecord   *playerHistoryRecord
}

const observationWarningThreshold = 3

type observationFailure struct {
	rendererID     string
	playID         string
	currentEntryID string
	count          int
	warning        StatusWarning
}

func (s *Service) applyObservationFailure(ctx context.Context, observationError error) (bool, *playerHistoryRecord) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.resetListeningInterval(s.listening.playID)
	s.terminal = terminalPositionEvidence{}
	st, err := s.loadState(ctx)
	if err != nil {
		return false, nil
	}
	if st.playID == "" || st.state == StateStopped {
		s.observationFailure = observationFailure{}
		return false, nil
	}
	previous := s.observationFailure
	next := previous
	newStreak := previous.count == 0 || previous.playID != st.playID || previous.rendererID != st.rendererID
	if newStreak {
		next = observationFailure{rendererID: st.rendererID, playID: st.playID, currentEntryID: st.currentEntryID}
		next.warning.ID = st.revision + 1
	}
	if next.count < observationWarningThreshold {
		next.count++
	}
	// Keep uncertainty immediate, but surface a warning only after consecutive
	// failed observations. This never revokes or restarts the existing stream.
	message := "Renderer status could not be confirmed. Existing playback has not been restarted."
	var actionError *output.ActionError
	if errors.As(observationError, &actionError) {
		message = fmt.Sprintf("Renderer status could not be confirmed (%s). Existing playback has not been restarted.", actionError)
	}
	next.warning.Message = message
	logTransition := newStreak || st.state != StateUnavailable ||
		(next.count >= observationWarningThreshold && previous.count < observationWarningThreshold)
	changed := newStreak || st.state != StateUnavailable ||
		(next.count >= observationWarningThreshold &&
			(previous.count < observationWarningThreshold || previous.warning.Message != message))
	if changed {
		if _, err := s.db.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,state='unavailable' WHERE singleton=1`); err != nil {
			return false, nil
		}
		if logTransition {
			s.logObservationFailure(st, next.count, diagnosticError(observationError, "observe_failed"), next.count >= observationWarningThreshold)
		}
	}
	s.observationFailure = next
	var historyRecord *playerHistoryRecord
	if next.count >= observationWarningThreshold && previous.count < observationWarningThreshold {
		info := diagnosticError(observationError, "observe_failed")
		stage := info.action
		if stage == "" {
			stage = "Observe"
		}
		code := info.category
		if info.code != 0 {
			code += ":" + strconv.Itoa(info.code)
		}
		position := st.positionMS
		historyRecord = &playerHistoryRecord{
			key:        observationHistoryKey(st.rendererID, st.playID, stage, next.warning.ID, "unknown"),
			receivedAt: s.now().UTC(), rendererID: st.rendererID, entryID: st.currentEntryID, playID: st.playID,
			stage: stage, code: code, message: message, outcome: "unknown", positionMS: &position,
			details: map[string]any{"category": info.category, "renderer_action": info.action, "renderer_code": info.code, "failure_count": next.count},
		}
	}
	return changed, historyRecord
}

func (s *Service) applyRendererUnavailable(ctx context.Context, message string) (string, bool) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	s.observationFailure = observationFailure{}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", false
	}
	defer tx.Rollback()
	var state, rendererID, currentEntryID, playID, oldMessage string
	var revision int64
	if err := tx.QueryRowContext(ctx, "SELECT revision,state,renderer_id,current_entry_id,play_id,error FROM player_state WHERE singleton=1").Scan(&revision, &state, &rendererID, &currentEntryID, &playID, &oldMessage); err != nil {
		return "", false
	}
	if state == StateStopped && playID == "" {
		return "", false
	}
	if state == StateUnavailable && oldMessage == message {
		return "", false
	}
	queueChanged := false
	if currentEntryID != "" {
		result, err := tx.ExecContext(ctx, "UPDATE player_queue SET status='pending' WHERE entry_id=? AND status='playing'", currentEntryID)
		if err != nil {
			return "", false
		}
		changed, _ := result.RowsAffected()
		queueChanged = changed > 0
	}
	queueIncrement := 0
	if queueChanged {
		queueIncrement = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,state='unavailable',resume_required=1,error=?,play_id='',current_uri='',current_seekable=0,control_action='',control_at='' WHERE singleton=1`, queueIncrement, message); err != nil {
		return "", false
	}
	if err := tx.Commit(); err != nil {
		return "", false
	}
	s.listening = listeningEvidence{}
	s.logRendererUnavailable(rendererID, playID, currentEntryID, state, "renderer_offline", revision+1)
	s.startup = startupObservationEvidence{}
	return playID, true
}

func (s *Service) applyObservation(ctx context.Context, observation output.Observation) (result observationResult) {
	s.opMu.Lock()
	defer s.opMu.Unlock()
	failure := s.observationFailure
	warningCleared := failure.count >= observationWarningThreshold
	s.observationFailure = observationFailure{}
	// A successful query clears its warning even when ownership reconciliation
	// produces no other state or position change.
	defer func() {
		if warningCleared {
			if !result.playerChanged && !result.positionChanged {
				if _, err := s.db.ExecContext(ctx, `UPDATE player_state SET revision=revision+1 WHERE singleton=1`); err == nil {
					result.playerChanged = true
				}
			}
		}
		if failure.count > 0 && result.playerChanged {
			s.logObservationRecovered(failure, observation)
		}
		if failure.count >= observationWarningThreshold && result.playerChanged && result.historyRecord == nil &&
			!strings.EqualFold(strings.TrimSpace(observation.TransportStatus), "ERROR_OCCURRED") {
			position := observation.PositionMS
			result.historyRecord = &playerHistoryRecord{
				key:        observationHistoryKey(failure.rendererID, failure.playID, "Observe", failure.warning.ID, "recovered"),
				receivedAt: s.now().UTC(), rendererID: failure.rendererID, entryID: failure.currentEntryID, playID: failure.playID,
				stage: "Observe", code: "observation_recovered", message: "Renderer status reporting recovered.",
				outcome: "recovered", positionMS: &position,
				details: map[string]any{"failure_count": failure.count, "observed_state": normalizeObservedState(observation.State), "transport_status": diagnosticTransportStatus(observation.TransportStatus)},
			}
		}
	}()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result
	}
	defer tx.Rollback()
	st, err := loadStateTx(ctx, tx)
	if err != nil || st.rendererID == "" {
		return result
	}
	observedAt := observation.ObservedAt.UTC().Format(time.RFC3339Nano)
	duration := observation.DurationMS
	if duration <= 0 {
		duration = st.durationMS
	}
	position := st.positionMS
	if observation.HasPosition {
		position = observation.PositionMS
	}
	if position < 0 {
		position = 0
	}
	if duration > 0 && position > duration {
		position = duration
	}

	var active int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_commands WHERE service_epoch=? AND status IN ('pending','running')", s.epoch).Scan(&active); err != nil {
		return result
	}
	terminalEnd := s.observeTerminalPosition(st, observation, active == 0)
	if active != 0 {
		result.positionChanged = observation.HasPosition && position != st.positionMS
		_, err = tx.ExecContext(ctx, observationEvidenceSQL, position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return observationResult{}
		}
		s.resetListeningInterval(st.playID)
		return result
	}

	if st.resumeRequired {
		result.positionChanged = observation.HasPosition && position != st.positionMS
		_, err = tx.ExecContext(ctx, observationEvidenceSQL, position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return observationResult{}
		}
		s.resetListeningInterval(st.playID)
		return result
	}

	if observation.PlayID != "" && observation.PlayID != st.playID {
		s.resetListeningInterval(st.playID)
		return result
	}

	normalized := normalizeObservedState(observation.State)
	if normalized == "unknown" && !observation.HasURI {
		result.positionChanged = observation.HasPosition && position != st.positionMS
		_, err = tx.ExecContext(ctx, observationEvidenceRevisionSQL, boolInt(result.positionChanged), position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return observationResult{}
		}
		s.resetListeningInterval(st.playID)
		return result
	}

	ownedURI := observation.HasURI && st.currentURI != "" && observation.URI == st.currentURI
	uriCleared := observation.HasURI && st.currentURI != "" && observation.URI == ""
	if observation.HasURI && st.currentURI != "" && observation.URI != "" && !ownedURI {
		return s.commitExternalObservation(ctx, tx, st, observation, position, duration, observedAt, "external_playback", "Renderer playback changed outside JaStreamer. Press Play to resume.")
	}
	if correlatedMediaFailure(st, observation) {
		return s.commitMediaFailureAdvance(ctx, tx, st, observation, position, duration, observedAt)
	}
	if terminalEnd {
		_, libraryChanged, listenErr := s.prepareListening(ctx, tx, st, observation, true)
		if listenErr != nil {
			return observationResult{}
		}
		return s.commitTerminalAdvance(ctx, tx, st, observation, observedAt, libraryChanged)
	}

	if (normalized == "stopped" || normalized == "unknown") && (!observation.HasURI || ownedURI || uriCleared) {
		if st.controlAction != "" && (st.state == StateStopped || !ownedURI) {
			query := observationEvidenceAndControlSQL
			if st.state == StateStopped {
				query = observationStoppedControlSQL
			}
			_, err = tx.ExecContext(ctx, query, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt)
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				return observationResult{}
			}
			s.resetListeningInterval(st.playID)
			return result
		}
		if normalized == "stopped" {
			if strings.EqualFold(strings.TrimSpace(observation.TransportStatus), "ERROR_OCCURRED") {
				return s.commitInterruptedObservation(ctx, tx, st, observation, position, duration, observedAt, StateError, "renderer_playback_error", "The renderer reported a playback error. Press Play to resume.")
			}
			if s.withinStartupGrace(st, observation) {
				return s.commitStartupStoppedObservation(ctx, tx, st, observation, position, duration, observedAt)
			}
		}
		if reliableNaturalEnd(st, observation) {
			_, libraryChanged, listenErr := s.prepareListening(ctx, tx, st, observation, true)
			if listenErr != nil {
				return observationResult{}
			}
			return s.commitNaturalEnd(ctx, tx, st, observation, position, duration, observedAt, libraryChanged)
		}
		if normalized == "stopped" {
			return s.commitInterruptedObservation(ctx, tx, st, observation, position, duration, observedAt, StateStopped, "renderer_stopped", "Playback stopped on the renderer. Press Play to resume.")
		}
		return s.commitExternalObservation(ctx, tx, st, observation, position, duration, observedAt, "media_cleared_without_completion", "The renderer cleared the current media without reliable end-of-track evidence. Press Play to resume.")
	}

	if normalized == "playing" || normalized == "paused" || normalized == "transitioning" {
		if observation.HasURI && st.currentURI != "" && !ownedURI {
			return s.commitExternalObservation(ctx, tx, st, observation, position, duration, observedAt, "uncorrelated_playback", "Renderer playback could not be correlated with the current track. Press Play to resume.")
		}
		state := StateStarting
		if normalized == "playing" {
			state = StatePlaying
		} else if normalized == "paused" {
			state = StatePaused
		}
		result.playerChanged = state != st.state || st.errorMessage != ""
		result.positionChanged = observation.HasPosition && (position != st.positionMS || duration != st.durationMS)
		queueChanged := false
		if st.currentEntryID != "" && normalized != "transitioning" {
			changed, updateErr := setQueueStatus(ctx, tx, st.currentEntryID, EntryPlaying)
			if updateErr != nil {
				return observationResult{}
			}
			queueChanged = changed
		}
		queueIncrement := 0
		if queueChanged {
			queueIncrement = 1
		}
		nextListening, libraryChanged, listenErr := s.prepareListening(ctx, tx, st, observation, false)
		if listenErr != nil {
			return observationResult{}
		}
		_, err = tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+?,queue_revision=queue_revision+?,state=?,position_ms=?,duration_ms=?,observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=?,error='',control_action='',control_at='' WHERE singleton=1`, boolInt(result.playerChanged || result.positionChanged), queueIncrement, state, position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return observationResult{}
		}
		s.listening = nextListening
		if normalized != "transitioning" {
			s.startup = startupObservationEvidence{}
		}
		result.queueChanged = queueChanged
		result.libraryChanged = libraryChanged
		return result
	}

	if normalized == "stopped" && st.currentURI == "" {
		result.playerChanged = st.state != StateStopped || st.errorMessage != ""
		result.positionChanged = observation.HasPosition && position != st.positionMS
		_, err = tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+?,state='stopped',position_ms=?,duration_ms=?,observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=?,error='',control_action='',control_at='' WHERE singleton=1`, boolInt(result.playerChanged || result.positionChanged), position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt)
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			return observationResult{}
		}
		s.listening = listeningEvidence{}
		return result
	}

	return s.commitExternalObservation(ctx, tx, st, observation, position, duration, observedAt, "unavailable_state", "The renderer reported an unavailable playback state. Press Play to resume.")
}

func (s *Service) withinStartupGrace(st storedState, observation output.Observation) bool {
	return s.startup.playID != "" && s.startup.playID == st.playID &&
		s.now().Before(s.startup.deadline) &&
		!observation.CompletionKnown &&
		normalizeObservedState(observation.State) == "stopped" &&
		strings.EqualFold(strings.TrimSpace(observation.TransportStatus), "OK") &&
		observation.HasURI && st.currentURI != "" && observation.URI == st.currentURI
}

func (s *Service) commitStartupStoppedObservation(ctx context.Context, tx *sql.Tx, st storedState, observation output.Observation, position, duration int64, observedAt string) observationResult {
	result := observationResult{
		positionChanged: observation.HasPosition && (position != st.positionMS || duration != st.durationMS),
	}
	_, err := tx.ExecContext(ctx, observationEvidenceRevisionSQL, boolInt(result.positionChanged), position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt)
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return observationResult{}
	}
	s.resetListeningInterval(st.playID)
	return result
}

const observationEvidenceSQL = `UPDATE player_state SET position_ms=?,duration_ms=?,observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=? WHERE singleton=1`
const observationEvidenceRevisionSQL = `UPDATE player_state SET revision=revision+?,position_ms=?,duration_ms=?,observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=? WHERE singleton=1`
const observationEvidenceAndControlSQL = `UPDATE player_state SET observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=?,control_action='',control_at='' WHERE singleton=1`
const observationStoppedControlSQL = `UPDATE player_state SET play_id='',current_uri='',current_seekable=0,observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=?,resume_required=0,error='',control_action='',control_at='' WHERE singleton=1`

func (s *Service) commitExternalObservation(ctx context.Context, tx *sql.Tx, st storedState, observation output.Observation, position, duration int64, observedAt, reason, message string) observationResult {
	return s.commitInterruptedObservation(ctx, tx, st, observation, position, duration, observedAt, StateUnavailable, reason, message)
}

func (s *Service) commitInterruptedObservation(ctx context.Context, tx *sql.Tx, st storedState, observation output.Observation, position, duration int64, observedAt, state, reason, message string) observationResult {
	result := observationResult{playerChanged: true, revokePlayID: st.playID}
	queueChanged := false
	if st.currentEntryID != "" {
		var err error
		queueChanged, err = setQueueStatus(ctx, tx, st.currentEntryID, EntryPending)
		if err != nil {
			return observationResult{}
		}
	}
	queueIncrement := 0
	if queueChanged {
		queueIncrement = 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,state=?,position_ms=?,duration_ms=?,observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=?,resume_required=1,error=?,play_id='',current_uri='',current_seekable=0,control_action='',control_at='' WHERE singleton=1`, queueIncrement, state, position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt, message); err != nil {
		return observationResult{}
	}
	if err := tx.Commit(); err != nil {
		return observationResult{}
	}
	s.listening = listeningEvidence{}
	s.logObservationInterrupted(st, observation, state, reason, position)
	s.startup = startupObservationEvidence{}
	result.queueChanged = queueChanged
	if reason == "renderer_playback_error" {
		value := position
		result.historyRecord = &playerHistoryRecord{
			key:        observationHistoryKey(st.rendererID, st.playID, "Observe", st.revision+1, "failed"),
			receivedAt: s.now().UTC(), rendererID: st.rendererID, entryID: st.currentEntryID, playID: st.playID,
			stage: "Observe", code: "renderer_playback_error", message: message, outcome: "failed", positionMS: &value,
			details: map[string]any{"observed_state": normalizeObservedState(observation.State), "transport_status": diagnosticTransportStatus(observation.TransportStatus)},
		}
	}
	return result
}

func correlatedMediaFailure(st storedState, observation output.Observation) bool {
	return observation.MediaFailed &&
		(st.state == StatePlaying || st.state == StateStarting) &&
		!st.resumeRequired &&
		st.playID != "" && st.currentEntryID != "" && st.currentURI != "" &&
		observation.PlayID == st.playID &&
		normalizeObservedState(observation.State) == "stopped" &&
		observation.HasURI && observation.URI == st.currentURI &&
		strings.EqualFold(strings.TrimSpace(observation.TransportStatus), "ERROR_OCCURRED")
}

func (s *Service) commitMediaFailureAdvance(ctx context.Context, tx *sql.Tx, st storedState, observation output.Observation, position, duration int64, observedAt string) observationResult {
	const message = "The media engine reported a terminal playback failure for this track."
	result := observationResult{playerChanged: true, revokePlayID: st.playID}
	changed, err := setQueueStatus(ctx, tx, st.currentEntryID, EntryError)
	if err != nil {
		return observationResult{}
	}
	result.queueChanged = changed
	var currentPosition int
	if err := tx.QueryRowContext(ctx, "SELECT position FROM player_queue WHERE entry_id=?", st.currentEntryID).Scan(&currentPosition); err != nil {
		return observationResult{}
	}
	var nextEntryID string
	if err := tx.QueryRowContext(ctx, "SELECT entry_id FROM player_queue WHERE position>? ORDER BY position LIMIT 1", currentPosition).Scan(&nextEntryID); err != nil && err != sql.ErrNoRows {
		return observationResult{}
	}
	state := StateError
	controlAction := ""
	playerMessage := message
	var commandID string
	if nextEntryID != "" {
		commandID, err = randomID("command")
		if err != nil {
			return observationResult{}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_commands(command_id,service_epoch,action,entry_id,position_ms,status,error,created_at,started_at,completed_at) VALUES(?,?, 'error_next',?,0,'pending','',?,'','')`, commandID, s.epoch, nextEntryID, observedAt); err != nil {
			return observationResult{}
		}
		state = StateStarting
		controlAction = "error_next"
		playerMessage = ""
		result.commandQueued = true
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,current_entry_id=CASE WHEN ?<>'' THEN ? ELSE current_entry_id END,state=?,play_id='',current_uri='',current_seekable=0,position_ms=?,duration_ms=?,observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=?,resume_required=0,error=?,control_action=?,control_at=? WHERE singleton=1`, boolInt(result.queueChanged), nextEntryID, nextEntryID, state, position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt, playerMessage, controlAction, observedAt); err != nil {
		return observationResult{}
	}
	if err := tx.Commit(); err != nil {
		return observationResult{}
	}
	s.listening = listeningEvidence{}
	s.logMediaFailureTransition(st, observation, commandID, nextEntryID, state, position)
	if commandID != "" {
		s.logCommandAccepted(commandID, "error_next", st.rendererID, st.playID, st.currentEntryID, nextEntryID, "media_failure", state, st.revision+1, 0)
	}
	s.startup = startupObservationEvidence{}
	value := position
	result.historyRecord = &playerHistoryRecord{
		key:        observationHistoryKey(st.rendererID, st.playID, "Playback", st.revision+1, "failed"),
		receivedAt: s.now().UTC(), rendererID: st.rendererID, entryID: st.currentEntryID, playID: st.playID,
		stage: "Playback", code: "media_failed", message: message, outcome: "failed", positionMS: &value,
		details: map[string]any{"observed_state": normalizeObservedState(observation.State), "transport_status": diagnosticTransportStatus(observation.TransportStatus)},
	}
	return result
}

func (s *Service) observeTerminalPosition(st storedState, observation output.Observation, idle bool) bool {
	if !idle || observation.CompletionKnown || st.resumeRequired || st.state != StatePlaying || st.playID == "" ||
		normalizeObservedState(observation.State) != "playing" ||
		!observation.HasURI || observation.URI != st.currentURI || !observation.HasPosition ||
		observation.DurationMS <= 0 || observation.PositionMS < observation.DurationMS ||
		observation.PositionMS > observation.DurationMS+1000 ||
		strings.EqualFold(strings.TrimSpace(observation.TransportStatus), "ERROR_OCCURRED") {
		s.terminal = terminalPositionEvidence{}
		return false
	}
	now := s.now()
	if s.terminal.playID != st.playID || s.terminal.durationMS != observation.DurationMS || now.Before(s.terminal.since) {
		s.terminal = terminalPositionEvidence{playID: st.playID, durationMS: observation.DurationMS, since: now}
		return false
	}
	// Some renderers retain PLAYING at EOF. Repeated owned terminal positions,
	// not elapsed track time alone, must survive whole-second quantization.
	return now.Sub(s.terminal.since) >= 2*time.Second
}

func (s *Service) commitTerminalAdvance(ctx context.Context, tx *sql.Tx, st storedState, observation output.Observation, observedAt string, libraryChanged bool) observationResult {
	insert, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO player_natural_ends(play_id,entry_id,ended_at) VALUES(?,?,?)", st.playID, st.currentEntryID, observedAt)
	if err != nil {
		return observationResult{}
	}
	inserted, err := insert.RowsAffected()
	if err != nil || inserted == 0 {
		return observationResult{}
	}
	commandID, err := randomID("command")
	if err != nil {
		return observationResult{}
	}
	// Keep the current entry and binding until the ordinary Next worker has
	// acknowledged Stop. A failed Stop must not complete or replay the track.
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_commands(command_id,service_epoch,action,entry_id,position_ms,status,error,created_at,started_at,completed_at) VALUES(?,?, 'next','',0,'pending','',?,'','')`, commandID, s.epoch, observedAt); err != nil {
		return observationResult{}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE player_state SET revision=revision+1 WHERE singleton=1"); err != nil {
		return observationResult{}
	}
	if err := tx.Commit(); err != nil {
		return observationResult{}
	}
	s.listening = listeningEvidence{}
	s.logNaturalTransition(st, observation, commandID, "next", "", st.state, "terminal_position", st.positionMS)
	s.logCommandAccepted(commandID, "next", st.rendererID, st.playID, st.currentEntryID, "", "terminal_position", st.state, st.revision+1, 0)
	return observationResult{playerChanged: true, commandQueued: true, libraryChanged: libraryChanged}
}

func (s *Service) commitNaturalEnd(ctx context.Context, tx *sql.Tx, st storedState, observation output.Observation, position, duration int64, observedAt string, libraryChanged bool) observationResult {
	result := observationResult{playerChanged: true, queueChanged: true, revokePlayID: st.playID, libraryChanged: libraryChanged}
	insert, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO player_natural_ends(play_id,entry_id,ended_at) VALUES(?,?,?)", st.playID, st.currentEntryID, observedAt)
	if err != nil {
		return observationResult{}
	}
	inserted, err := insert.RowsAffected()
	if err != nil || inserted == 0 {
		return observationResult{}
	}
	if _, err := tx.ExecContext(ctx, "UPDATE player_queue SET status='completed' WHERE entry_id=?", st.currentEntryID); err != nil {
		return observationResult{}
	}
	var currentPosition int
	if err := tx.QueryRowContext(ctx, "SELECT position FROM player_queue WHERE entry_id=?", st.currentEntryID).Scan(&currentPosition); err != nil {
		return observationResult{}
	}
	var nextEntryID string
	err = tx.QueryRowContext(ctx, "SELECT entry_id FROM player_queue WHERE position>? ORDER BY position LIMIT 1", currentPosition).Scan(&nextEntryID)
	if err != nil && err != sql.ErrNoRows {
		return observationResult{}
	}
	state := StateStopped
	var commandID string
	if nextEntryID != "" {
		var idErr error
		commandID, idErr = randomID("command")
		if idErr != nil {
			return observationResult{}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_commands(command_id,service_epoch,action,entry_id,position_ms,status,error,created_at,started_at,completed_at) VALUES(?,?, 'natural_next',?,0,'pending','',?,'','')`, commandID, s.epoch, nextEntryID, observedAt); err != nil {
			return observationResult{}
		}
		result.commandQueued = true
		state = StateStarting
	}
	if _, err := tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+1,current_entry_id=CASE WHEN ?<>'' THEN ? ELSE current_entry_id END,state=?,play_id='',current_uri='',current_seekable=0,position_ms=?,duration_ms=?,observed_at=?,last_observed_state=?,last_observed_uri=?,last_observed_position_ms=?,last_observed_duration_ms=?,last_observed_has_position=?,last_observed_at=?,error='',control_action='',control_at='' WHERE singleton=1`, nextEntryID, nextEntryID, state, position, duration, observedAt, observation.State, observation.URI, observation.PositionMS, observation.DurationMS, boolInt(observation.HasPosition), observedAt); err != nil {
		return observationResult{}
	}
	if err := tx.Commit(); err != nil {
		return observationResult{}
	}
	s.listening = listeningEvidence{}
	s.logNaturalTransition(st, observation, commandID, "natural_next", nextEntryID, state, "natural_end", position)
	if commandID != "" {
		s.logCommandAccepted(commandID, "natural_next", st.rendererID, st.playID, st.currentEntryID, nextEntryID, "natural_end", state, st.revision+1, 0)
	}
	s.startup = startupObservationEvidence{}
	return result
}

func reliableNaturalEnd(st storedState, observation output.Observation) bool {
	if observation.CompletionKnown {
		return observation.Completed && normalizeObservedState(observation.State) == "stopped" &&
			st.playID != "" && st.currentEntryID != "" && st.currentURI != "" &&
			observation.HasURI && observation.URI == st.currentURI &&
			!strings.EqualFold(strings.TrimSpace(observation.TransportStatus), "ERROR_OCCURRED")
	}
	if st.playID == "" || st.currentEntryID == "" || normalizeObservedState(st.lastObservedState) != "playing" || !st.lastObservedHasPosition {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(observation.TransportStatus), "ERROR_OCCURRED") {
		return false
	}
	duration := st.lastObservedDurationMS
	if duration <= 0 {
		duration = st.durationMS
	}
	if duration <= 0 || st.lastObservedPositionMS < 0 {
		return false
	}
	tolerance := duration / 20
	if tolerance < 2000 {
		tolerance = 2000
	}
	if tolerance > 5000 {
		tolerance = 5000
	}
	nearEnd := st.lastObservedPositionMS >= duration-tolerance && st.lastObservedPositionMS <= duration+30000
	if !nearEnd {
		return false
	}
	state := normalizeObservedState(observation.State)
	return state == "stopped" || (state == "unknown" && observation.HasURI && observation.URI == "")
}

func normalizeObservedState(state string) string {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "playing", "paused", "stopped", "transitioning", "unknown":
		return strings.ToLower(strings.TrimSpace(state))
	case "paused_playback":
		return "paused"
	default:
		return "unknown"
	}
}

func loadStateTx(ctx context.Context, tx *sql.Tx) (storedState, error) {
	var st storedState
	var seekable, hasPosition, resume int
	err := tx.QueryRowContext(ctx, `SELECT revision,queue_revision,renderer_id,current_entry_id,play_id,current_uri,current_seekable,state,position_ms,duration_ms,observed_at,last_observed_state,last_observed_uri,last_observed_position_ms,last_observed_duration_ms,last_observed_has_position,last_observed_at,resume_required,error,control_action,control_at FROM player_state WHERE singleton=1`).Scan(
		&st.revision, &st.queueRevision, &st.rendererID, &st.currentEntryID, &st.playID, &st.currentURI,
		&seekable, &st.state, &st.positionMS, &st.durationMS, &st.observedAt, &st.lastObservedState,
		&st.lastObservedURI, &st.lastObservedPositionMS, &st.lastObservedDurationMS, &hasPosition,
		&st.lastObservedAt, &resume, &st.errorMessage, &st.controlAction, &st.controlAt,
	)
	if err != nil {
		return storedState{}, fmt.Errorf("player: load transactional state: %w", err)
	}
	st.currentSeekable = seekable != 0
	st.lastObservedHasPosition = hasPosition != 0
	st.resumeRequired = resume != 0
	return st, nil
}

func setQueueStatus(ctx context.Context, tx *sql.Tx, entryID, status string) (bool, error) {
	result, err := tx.ExecContext(ctx, "UPDATE player_queue SET status=? WHERE entry_id=? AND status<>?", status, entryID, status)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}
