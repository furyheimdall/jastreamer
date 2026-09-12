package player

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

const commandTimeout = 20 * time.Second

type libraryAPI interface {
	Track(context.Context, string) (library.Track, error)
}

type deviceAPI interface {
	Device(string) (output.Device, bool)
	SetURI(context.Context, string, output.Resource) error
	Play(context.Context, string) error
	Pause(context.Context, string) error
	Stop(context.Context, string) error
	Seek(context.Context, string, int64) error
	Observe(context.Context, string) (output.Observation, error)
	Pair(context.Context, string, output.PairingRequest) (output.PairingStatus, error)
}

type mediaAPI interface {
	Prepare(context.Context, output.Device, library.Track, string) (output.Resource, error)
	Revoke(string)
}

type terminalPositionEvidence struct {
	playID     string
	durationMS int64
	since      time.Time
}

type Service struct {
	db                 *sql.DB
	lib                libraryAPI
	devices            deviceAPI
	media              mediaAPI
	pollInterval       time.Duration
	notify             func(string)
	epoch              string
	wake               chan struct{}
	observeWake        chan struct{}
	opMu               sync.Mutex
	rendererMu         sync.Mutex
	runMu              sync.Mutex
	running            bool
	stopped            bool
	now                func() time.Time
	terminal           terminalPositionEvidence // guarded by opMu; never restored after restart
	observationFailure observationFailure       // guarded by opMu; scoped to the current renderer/playback
}

type storedState struct {
	revision                int64
	queueRevision           int64
	rendererID              string
	currentEntryID          string
	playID                  string
	currentURI              string
	currentSeekable         bool
	state                   string
	positionMS              int64
	durationMS              int64
	observedAt              string
	lastObservedState       string
	lastObservedURI         string
	lastObservedPositionMS  int64
	lastObservedDurationMS  int64
	lastObservedHasPosition bool
	lastObservedAt          string
	resumeRequired          bool
	errorMessage            string
	controlAction           string
	controlAt               string
}

func New(ctx context.Context, db *sql.DB, lib *library.Service, outputs *output.Manager, pollInterval time.Duration, notify func(string)) (*Service, error) {
	return newService(ctx, db, lib, outputs, outputs, pollInterval, notify)
}

func newService(ctx context.Context, db *sql.DB, lib libraryAPI, devices deviceAPI, media mediaAPI, pollInterval time.Duration, notify func(string)) (*Service, error) {
	if db == nil || lib == nil || devices == nil || media == nil {
		return nil, errors.New("player: database, library, renderer manager, and media service are required")
	}
	if pollInterval <= 0 {
		return nil, errors.New("player: poll interval must be positive")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if notify == nil {
		notify = func(string) {}
	}
	epoch, err := randomID("epoch")
	if err != nil {
		return nil, fmt.Errorf("player: create service identity: %w", err)
	}
	s := &Service{
		db: db, lib: lib, devices: devices, media: media, pollInterval: pollInterval, notify: notify,
		epoch: epoch, wake: make(chan struct{}, 1), observeWake: make(chan struct{}, 1), now: time.Now,
	}
	if err := s.initialize(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) initialize(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS player_state (
  singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
  revision INTEGER NOT NULL,
  queue_revision INTEGER NOT NULL,
  renderer_id TEXT NOT NULL,
  current_entry_id TEXT NOT NULL,
  play_id TEXT NOT NULL,
  current_uri TEXT NOT NULL,
  current_seekable INTEGER NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('stopped','starting','playing','paused','unavailable','error')),
  position_ms INTEGER NOT NULL,
  duration_ms INTEGER NOT NULL,
  observed_at TEXT NOT NULL,
  last_observed_state TEXT NOT NULL,
  last_observed_uri TEXT NOT NULL,
  last_observed_position_ms INTEGER NOT NULL,
  last_observed_duration_ms INTEGER NOT NULL,
  last_observed_has_position INTEGER NOT NULL,
  last_observed_at TEXT NOT NULL,
  resume_required INTEGER NOT NULL,
  error TEXT NOT NULL,
  control_action TEXT NOT NULL,
  control_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS player_queue (
  entry_id TEXT PRIMARY KEY,
  position INTEGER NOT NULL UNIQUE,
  track_id TEXT NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','playing','completed','error'))
);
CREATE INDEX IF NOT EXISTS player_queue_position ON player_queue(position);
CREATE TABLE IF NOT EXISTS player_commands (
  command_id TEXT PRIMARY KEY,
  service_epoch TEXT NOT NULL,
  action TEXT NOT NULL,
  entry_id TEXT NOT NULL,
  position_ms INTEGER NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('pending','running','succeeded','failed','unknown')),
  error TEXT NOT NULL,
  created_at TEXT NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS player_commands_active ON player_commands(service_epoch, status, created_at);
CREATE TABLE IF NOT EXISTS player_natural_ends (
  play_id TEXT PRIMARY KEY,
  entry_id TEXT NOT NULL,
  ended_at TEXT NOT NULL
);`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("player: initialize storage: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("player: begin recovery: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO player_state(
 singleton,revision,queue_revision,renderer_id,current_entry_id,play_id,current_uri,current_seekable,state,
 position_ms,duration_ms,observed_at,last_observed_state,last_observed_uri,last_observed_position_ms,
 last_observed_duration_ms,last_observed_has_position,last_observed_at,resume_required,error,control_action,control_at
) VALUES(1,0,0,'','','','',0,'stopped',0,0,'','','',0,0,0,'',0,'','','')`); err != nil {
		return fmt.Errorf("player: initialize state: %w", err)
	}
	var active int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_commands WHERE status IN ('pending','running')").Scan(&active); err != nil {
		return fmt.Errorf("player: inspect interrupted commands: %w", err)
	}
	if active > 0 {
		now := s.now().UTC().Format(time.RFC3339Nano)
		if _, err := tx.ExecContext(ctx, `UPDATE player_commands SET status='unknown',error='Outcome unknown after server restart; command was not replayed.',completed_at=? WHERE status IN ('pending','running')`, now); err != nil {
			return fmt.Errorf("player: resolve interrupted commands: %w", err)
		}
	}
	var state, rendererID, currentEntryID string
	if err := tx.QueryRowContext(ctx, "SELECT state,renderer_id,current_entry_id FROM player_state WHERE singleton=1").Scan(&state, &rendererID, &currentEntryID); err != nil {
		return fmt.Errorf("player: read recovery state: %w", err)
	}
	if state == StatePlaying || state == StatePaused || state == StateStarting || active > 0 {
		message := "Playback requires explicit resume after server restart."
		nextState := StateStopped
		if rendererID != "" {
			nextState = StateUnavailable
		}
		if _, err := tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,state=?,resume_required=1,error=?,control_action='',control_at='' WHERE singleton=1`, nextState, message); err != nil {
			return fmt.Errorf("player: preserve interrupted playback: %w", err)
		}
		if currentEntryID != "" {
			result, updateErr := tx.ExecContext(ctx, "UPDATE player_queue SET status='pending' WHERE entry_id=? AND status='playing'", currentEntryID)
			if updateErr != nil {
				return fmt.Errorf("player: preserve interrupted queue cursor: %w", updateErr)
			}
			if changed, rowsErr := result.RowsAffected(); rowsErr != nil {
				return fmt.Errorf("player: inspect recovered queue cursor: %w", rowsErr)
			} else if changed > 0 {
				if _, err := tx.ExecContext(ctx, "UPDATE player_state SET queue_revision=queue_revision+1 WHERE singleton=1"); err != nil {
					return fmt.Errorf("player: advance recovered queue revision: %w", err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("player: commit recovery: %w", err)
	}
	return nil
}

func (s *Service) Run(ctx context.Context) error {
	s.runMu.Lock()
	if s.running {
		s.runMu.Unlock()
		return errors.New("player: service is already running")
	}
	if s.stopped {
		s.runMu.Unlock()
		return errors.New("player: service cannot be restarted")
	}
	s.running = true
	s.runMu.Unlock()

	workerDone := make(chan struct{})
	observerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		s.commandLoop(ctx)
	}()
	go func() {
		defer close(observerDone)
		s.observationLoop(ctx)
	}()
	s.signalWorker()
	<-ctx.Done()
	<-workerDone
	<-observerDone
	s.markInterruptedCommands()
	s.runMu.Lock()
	s.running = false
	s.stopped = true
	s.runMu.Unlock()
	return nil
}

func (s *Service) Snapshot(ctx context.Context) (State, error) {
	s.opMu.Lock()
	st, err := s.loadState(ctx)
	var warning *StatusWarning
	failure := s.observationFailure
	if err == nil && st.state != StateStopped && st.playID != "" &&
		failure.playID == st.playID && failure.rendererID == st.rendererID &&
		failure.count >= observationWarningThreshold {
		copy := failure.warning
		warning = &copy
	}
	s.opMu.Unlock()
	if err != nil {
		return State{}, err
	}
	result := State{
		Revision: st.revision, State: st.state, RendererID: st.rendererID,
		CurrentEntryID: st.currentEntryID, PositionMS: st.positionMS,
		DurationMS: st.durationMS, ObservedAt: st.observedAt, Error: st.errorMessage,
		StatusWarning: warning,
	}
	if st.rendererID != "" {
		if device, ok := s.devices.Device(st.rendererID); ok {
			result.Capabilities = device.Capabilities
			result.Capabilities.Seek = result.Capabilities.Seek && st.currentSeekable
		}
	}
	if st.currentEntryID != "" {
		var trackID string
		err := s.db.QueryRowContext(ctx, "SELECT track_id FROM player_queue WHERE entry_id=?", st.currentEntryID).Scan(&trackID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return State{}, fmt.Errorf("player: load current queue entry: %w", err)
		}
		if trackID != "" {
			track, trackErr := s.lib.Track(ctx, trackID)
			if trackErr != nil {
				if ctx.Err() != nil {
					return State{}, ctx.Err()
				}
				track = library.Track{ID: trackID, Genres: []string{}, Available: false}
			}
			result.Track = &track
		}
	}
	if err := s.db.QueryRowContext(ctx, `SELECT CASE action WHEN 'natural_next' THEN 'next' ELSE action END FROM player_commands WHERE service_epoch=? AND status IN ('pending','running') ORDER BY created_at LIMIT 1`, s.epoch).Scan(&result.PendingCommand); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return State{}, fmt.Errorf("player: load pending command: %w", err)
	}
	return result, nil
}

func (s *Service) SelectOutput(ctx context.Context, rendererID string) (State, error) {
	if rendererID == "" {
		return State{}, fault.New(400, "INVALID_OUTPUT", "A renderer is required.")
	}
	s.rendererMu.Lock()
	device, found := s.devices.Device(rendererID)
	if !found {
		s.rendererMu.Unlock()
		return State{}, fault.New(404, "OUTPUT_NOT_FOUND", "The selected renderer is no longer available.")
	}
	s.opMu.Lock()
	st, err := s.loadState(ctx)
	if err == nil && st.state != StateStopped {
		err = fault.New(409, "PLAYER_NOT_STOPPED", "Stop playback before changing the renderer.")
	}
	if err == nil {
		var active int
		err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_commands WHERE service_epoch=? AND status IN ('pending','running')", s.epoch).Scan(&active)
		if err == nil && active != 0 {
			err = fault.New(409, "COMMAND_IN_PROGRESS", "Wait for the active playback command to finish.")
		}
	}
	if err == nil {
		state := StateStopped
		message := ""
		if !device.Online {
			state = StateUnavailable
			message = "The selected renderer is offline."
		}
		_, err = s.db.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,renderer_id=?,play_id='',current_uri='',current_seekable=0,state=?,resume_required=0,error=?,position_ms=0,duration_ms=0,observed_at='',last_observed_state='',last_observed_uri='',last_observed_position_ms=0,last_observed_duration_ms=0,last_observed_has_position=0,last_observed_at='',control_action='',control_at='' WHERE singleton=1`, rendererID, state, message)
		if err != nil {
			err = fmt.Errorf("player: select renderer: %w", err)
		}
	}
	s.opMu.Unlock()
	s.rendererMu.Unlock()
	if err != nil {
		return State{}, err
	}
	s.notify("player")
	return s.Snapshot(ctx)
}

func (s *Service) PairOutput(ctx context.Context, rendererID string, request output.PairingRequest) (output.PairingStatus, error) {
	if rendererID == "" {
		return output.PairingStatus{}, fault.New(400, "INVALID_OUTPUT", "A renderer is required.")
	}
	s.rendererMu.Lock()
	defer s.rendererMu.Unlock()
	device, found := s.devices.Device(rendererID)
	if !found {
		return output.PairingStatus{}, fault.New(404, "OUTPUT_NOT_FOUND", "The selected renderer is no longer available.")
	}
	if !device.Online {
		return output.PairingStatus{}, fault.New(409, "OUTPUT_OFFLINE", "The selected renderer is offline.")
	}
	s.opMu.Lock()
	defer s.opMu.Unlock()
	st, err := s.loadState(ctx)
	if err != nil {
		return output.PairingStatus{}, err
	}
	if st.state != StateStopped {
		return output.PairingStatus{}, fault.New(409, "PLAYER_NOT_STOPPED", "Stop playback before pairing a renderer.")
	}
	var active int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_commands WHERE service_epoch=? AND status IN ('pending','running')", s.epoch).Scan(&active); err != nil {
		return output.PairingStatus{}, fmt.Errorf("player: inspect command queue before pairing: %w", err)
	}
	if active != 0 {
		return output.PairingStatus{}, fault.New(409, "COMMAND_IN_PROGRESS", "Wait for the active playback command to finish.")
	}
	status, err := s.devices.Pair(ctx, rendererID, request)
	if err != nil {
		return output.PairingStatus{}, pairingError(err)
	}
	s.notify("renderers")
	return status, nil
}

func pairingError(err error) error {
	var action *output.ActionError
	if !errors.As(err, &action) {
		return err
	}
	switch action.Kind {
	case output.ErrorUnsupported:
		return fault.New(409, "PAIRING_UNSUPPORTED", "The selected renderer does not support pairing.")
	case output.ErrorResponse, output.ErrorFault:
		return fault.New(409, "PAIRING_REJECTED", "The renderer rejected the pairing information.")
	case output.ErrorTimeout:
		return fault.New(504, "PAIRING_TIMEOUT", "The renderer did not complete pairing in time.")
	case output.ErrorTransport:
		return fault.New(503, "PAIRING_UNAVAILABLE", "The renderer is unavailable for pairing.")
	case output.ErrorCancelled:
		return fault.New(408, "PAIRING_CANCELLED", "Pairing was cancelled before it completed.")
	default:
		return err
	}
}

func (s *Service) loadState(ctx context.Context) (storedState, error) {
	var st storedState
	var seekable, hasPosition, resume int
	err := s.db.QueryRowContext(ctx, `SELECT revision,queue_revision,renderer_id,current_entry_id,play_id,current_uri,current_seekable,state,
position_ms,duration_ms,observed_at,last_observed_state,last_observed_uri,last_observed_position_ms,last_observed_duration_ms,
last_observed_has_position,last_observed_at,resume_required,error,control_action,control_at FROM player_state WHERE singleton=1`).Scan(
		&st.revision, &st.queueRevision, &st.rendererID, &st.currentEntryID, &st.playID, &st.currentURI,
		&seekable, &st.state, &st.positionMS, &st.durationMS, &st.observedAt, &st.lastObservedState,
		&st.lastObservedURI, &st.lastObservedPositionMS, &st.lastObservedDurationMS, &hasPosition,
		&st.lastObservedAt, &resume, &st.errorMessage, &st.controlAction, &st.controlAt,
	)
	if err != nil {
		return storedState{}, fmt.Errorf("player: load state: %w", err)
	}
	st.currentSeekable = seekable != 0
	st.lastObservedHasPosition = hasPosition != 0
	st.resumeRequired = resume != 0
	return st, nil
}

func (s *Service) signalWorker() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Service) signalObserver() {
	select {
	case s.observeWake <- struct{}{}:
	default:
	}
}

func (s *Service) markInterruptedCommands() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	now := s.now().UTC().Format(time.RFC3339Nano)
	s.opMu.Lock()
	tx, err := s.db.BeginTx(ctx, nil)
	queueChanged := false
	playID := ""
	if err == nil {
		err = tx.QueryRowContext(ctx, "SELECT play_id FROM player_state WHERE singleton=1").Scan(&playID)
	}
	var interrupted int64
	if err == nil {
		result, updateErr := tx.ExecContext(ctx, `UPDATE player_commands SET status='unknown',error='Outcome unknown because the server stopped; command will not be replayed.',completed_at=? WHERE service_epoch=? AND status IN ('pending','running')`, now, s.epoch)
		err = updateErr
		if err == nil {
			interrupted, err = result.RowsAffected()
		}
	}
	if err == nil && interrupted > 0 {
		result, updateErr := tx.ExecContext(ctx, "UPDATE player_queue SET status='pending' WHERE status='playing'")
		err = updateErr
		if err == nil {
			var changed int64
			changed, err = result.RowsAffected()
			queueChanged = changed > 0
		}
		if err == nil {
			queueIncrement := 0
			if queueChanged {
				queueIncrement = 1
			}
			_, err = tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,queue_revision=queue_revision+?,state=CASE WHEN renderer_id='' THEN 'stopped' ELSE 'unavailable' END,play_id='',current_uri='',current_seekable=0,resume_required=1,error='Playback requires explicit resume after the server restarts.',control_action='',control_at='' WHERE singleton=1`, queueIncrement)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil && tx != nil {
		_ = tx.Rollback()
	}
	s.opMu.Unlock()
	if err == nil && interrupted > 0 {
		if playID != "" {
			s.media.Revoke(playID)
		}
		if queueChanged {
			s.notify("queue")
		}
		s.notify("player")
	}
}

func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}
