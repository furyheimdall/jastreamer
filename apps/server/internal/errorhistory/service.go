package errorhistory

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const RetentionLimit = 5000

const (
	maxKeyLength      = 512
	maxShortLength    = 256
	maxMessageLength  = 1024
	maxRelativeLength = 4096
	maxDetailsLength  = 32 << 10
)
const storageTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

type Event struct {
	ID           int64           `json:"id"`
	ReceivedAt   time.Time       `json:"received_at"`
	Kind         string          `json:"kind"`
	RendererID   string          `json:"renderer_id"`
	RendererName string          `json:"renderer_name"`
	Protocol     string          `json:"protocol"`
	TrackID      string          `json:"track_id"`
	TrackTitle   string          `json:"track_title"`
	RootName     string          `json:"root_name"`
	RelativePath string          `json:"relative_path"`
	Stage        string          `json:"stage"`
	Code         string          `json:"code"`
	Message      string          `json:"message"`
	Outcome      string          `json:"outcome"`
	PlayID       string          `json:"play_id"`
	CommandID    string          `json:"command_id"`
	PositionMS   *int64          `json:"position_ms"`
	Details      json.RawMessage `json:"details"`
	Key          string          `json:"-"`
}

type Renderer struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

type ListOptions struct {
	Kind       string
	RendererID string
	Offset     int
	Limit      int
}

type ListResult struct {
	Items          []Event    `json:"items"`
	Total          int        `json:"total"`
	Offset         int        `json:"offset"`
	Limit          int        `json:"limit"`
	RetentionLimit int        `json:"retention_limit"`
	Renderers      []Renderer `json:"renderers"`
}

type Service struct {
	db     *sql.DB
	notify func(string)
}

func New(ctx context.Context, db *sql.DB, notify func(string)) (*Service, error) {
	if db == nil {
		return nil, errors.New("error history: database is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if notify == nil {
		notify = func(string) {}
	}
	const schema = `
CREATE TABLE IF NOT EXISTS error_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  event_key TEXT NOT NULL UNIQUE,
  received_at TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN ('renderer','integrity')),
  renderer_id TEXT NOT NULL,
  renderer_name TEXT NOT NULL,
  protocol TEXT NOT NULL,
  track_id TEXT NOT NULL,
  track_title TEXT NOT NULL,
  root_name TEXT NOT NULL,
  relative_path TEXT NOT NULL,
  stage TEXT NOT NULL,
  code TEXT NOT NULL,
  message TEXT NOT NULL,
  outcome TEXT NOT NULL CHECK(outcome IN ('failed','recovered','unknown')),
  play_id TEXT NOT NULL,
  command_id TEXT NOT NULL,
  position_ms INTEGER,
  details BLOB NOT NULL
) STRICT;
CREATE INDEX IF NOT EXISTS error_history_received ON error_history(received_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS error_history_kind_received ON error_history(kind,received_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS error_history_renderer_received ON error_history(renderer_id,received_at DESC,id DESC);`
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("error history: initialize storage: %w", err)
	}
	return &Service{db: db, notify: notify}, nil
}

func (s *Service) Record(ctx context.Context, event Event) error {
	if s == nil || s.db == nil {
		return errors.New("error history: service is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error history: begin record: %w", err)
	}
	defer tx.Rollback()
	inserted, err := s.RecordInTransaction(ctx, tx, event)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("error history: commit record: %w", err)
	}
	if inserted {
		s.notify("history")
	}
	return nil
}

// RecordInTransaction atomically stores history with the caller's state change.
// The caller owns the transaction and publishes "history" only after committing.
func (s *Service) RecordInTransaction(ctx context.Context, tx *sql.Tx, event Event) (bool, error) {
	if s == nil || s.db == nil || tx == nil {
		return false, errors.New("error history: storage transaction is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateEvent(&event); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO error_history(
 event_key,received_at,kind,renderer_id,renderer_name,protocol,track_id,track_title,root_name,relative_path,
 stage,code,message,outcome,play_id,command_id,position_ms,details
) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(event_key) DO NOTHING`,
		event.Key, event.ReceivedAt.UTC().Format(storageTimeFormat), event.Kind, event.RendererID, event.RendererName,
		event.Protocol, event.TrackID, event.TrackTitle, event.RootName, event.RelativePath, event.Stage, event.Code,
		event.Message, event.Outcome, event.PlayID, event.CommandID, event.PositionMS, []byte(event.Details))
	if err != nil {
		return false, fmt.Errorf("error history: insert record: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("error history: inspect record: %w", err)
	}
	if inserted == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM error_history WHERE id IN (
 SELECT id FROM error_history ORDER BY received_at DESC,id DESC LIMIT -1 OFFSET ?
)`, RetentionLimit); err != nil {
		return false, fmt.Errorf("error history: enforce retention: %w", err)
	}
	return true, nil
}

func (s *Service) List(ctx context.Context, options ListOptions) (ListResult, error) {
	result := ListResult{Items: []Event{}, Renderers: []Renderer{}, Offset: options.Offset, Limit: options.Limit, RetentionLimit: RetentionLimit}
	if s == nil || s.db == nil {
		return result, errors.New("error history: service is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if options.Offset < 0 || options.Limit < 1 || options.Limit > 100 || (options.Kind != "" && options.Kind != "renderer" && options.Kind != "integrity") || len(options.RendererID) > maxShortLength {
		return result, errors.New("error history: invalid list options")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, fmt.Errorf("error history: begin history snapshot: %w", err)
	}
	defer tx.Rollback()
	where := " WHERE 1=1"
	args := make([]any, 0, 4)
	if options.Kind != "" {
		where += " AND kind=?"
		args = append(args, options.Kind)
	}
	if options.RendererID != "" {
		where += " AND renderer_id=?"
		args = append(args, options.RendererID)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM error_history"+where, args...).Scan(&result.Total); err != nil {
		return result, fmt.Errorf("error history: count records: %w", err)
	}
	queryArgs := append(args, options.Limit, options.Offset)
	rows, err := tx.QueryContext(ctx, `SELECT id,received_at,kind,renderer_id,renderer_name,protocol,track_id,track_title,root_name,relative_path,stage,code,message,outcome,play_id,command_id,position_ms,details FROM error_history`+where+` ORDER BY received_at DESC,id DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return result, fmt.Errorf("error history: query records: %w", err)
	}
	for rows.Next() {
		var event Event
		var received string
		var position sql.NullInt64
		var details []byte
		if err := rows.Scan(&event.ID, &received, &event.Kind, &event.RendererID, &event.RendererName, &event.Protocol, &event.TrackID, &event.TrackTitle, &event.RootName, &event.RelativePath, &event.Stage, &event.Code, &event.Message, &event.Outcome, &event.PlayID, &event.CommandID, &position, &details); err != nil {
			rows.Close()
			return result, fmt.Errorf("error history: read record: %w", err)
		}
		event.ReceivedAt, err = time.Parse(time.RFC3339Nano, received)
		if err != nil {
			rows.Close()
			return result, fmt.Errorf("error history: parse record time: %w", err)
		}
		if position.Valid {
			value := position.Int64
			event.PositionMS = &value
		}
		event.Details = json.RawMessage(details)
		result.Items = append(result.Items, event)
	}
	if err := rows.Close(); err != nil {
		return result, fmt.Errorf("error history: close records: %w", err)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("error history: iterate records: %w", err)
	}
	rendererRows, err := tx.QueryContext(ctx, `SELECT h.renderer_id,h.renderer_name,h.protocol FROM error_history h
WHERE h.renderer_id<>'' AND h.id=(SELECT newer.id FROM error_history newer WHERE newer.renderer_id=h.renderer_id ORDER BY newer.received_at DESC,newer.id DESC LIMIT 1)
ORDER BY lower(CASE WHEN h.renderer_name='' THEN h.renderer_id ELSE h.renderer_name END),h.renderer_id`)
	if err != nil {
		return result, fmt.Errorf("error history: query renderers: %w", err)
	}
	for rendererRows.Next() {
		var renderer Renderer
		if err := rendererRows.Scan(&renderer.ID, &renderer.Name, &renderer.Protocol); err != nil {
			rendererRows.Close()
			return result, fmt.Errorf("error history: read renderer: %w", err)
		}
		result.Renderers = append(result.Renderers, renderer)
	}
	if err := rendererRows.Close(); err != nil {
		return result, fmt.Errorf("error history: close renderers: %w", err)
	}
	if err := rendererRows.Err(); err != nil {
		return result, fmt.Errorf("error history: iterate renderers: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return result, fmt.Errorf("error history: finish history snapshot: %w", err)
	}
	return result, nil
}

func validateEvent(event *Event) error {
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = time.Now().UTC()
	} else {
		event.ReceivedAt = event.ReceivedAt.UTC()
	}
	if event.Kind != "renderer" && event.Kind != "integrity" {
		return errors.New("error history: invalid event kind")
	}
	if event.Outcome != "failed" && event.Outcome != "recovered" && event.Outcome != "unknown" {
		return errors.New("error history: invalid event outcome")
	}
	if event.Key == "" || !validText(event.Key, maxKeyLength) {
		return errors.New("error history: invalid event key")
	}
	for _, value := range []*string{&event.RendererName, &event.TrackTitle, &event.RootName} {
		if !utf8.ValidString(*value) || strings.ContainsRune(*value, '\x00') {
			return errors.New("error history: invalid display text")
		}
		if len(*value) > maxShortLength {
			end := maxShortLength
			for !utf8.RuneStart((*value)[end]) {
				end--
			}
			*value = (*value)[:end]
		}
	}
	short := []string{event.RendererID, event.Protocol, event.TrackID, event.Stage, event.Code, event.Outcome, event.PlayID, event.CommandID}
	for _, value := range short {
		if !validText(value, maxShortLength) {
			return errors.New("error history: event field is invalid or too long")
		}
	}
	if !validText(event.Message, maxMessageLength) || !validText(event.RelativePath, maxRelativeLength) {
		return errors.New("error history: event text is invalid or too long")
	}
	if event.Kind == "renderer" && event.RendererID == "" {
		return errors.New("error history: renderer event requires a renderer identifier")
	}
	if event.Kind == "integrity" && event.RendererID != "" {
		return errors.New("error history: integrity event cannot name a renderer")
	}
	if event.RelativePath != "" && (!filepath.IsLocal(filepath.FromSlash(event.RelativePath)) || strings.Contains(event.RelativePath, "://")) {
		return errors.New("error history: invalid relative path")
	}
	if event.PositionMS != nil && *event.PositionMS < 0 {
		return errors.New("error history: invalid position")
	}
	if len(event.Details) == 0 {
		event.Details = json.RawMessage(`{}`)
	}
	if len(event.Details) > maxDetailsLength || !json.Valid(event.Details) {
		return errors.New("error history: invalid event details")
	}
	details := bytes.TrimSpace(event.Details)
	if details[0] != '{' && details[0] != '[' {
		return errors.New("error history: event details must be structured JSON")
	}
	return nil
}

func validText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}
