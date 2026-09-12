package player

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
)

type queueRecord struct {
	id       string
	trackID  string
	status   string
	position int
}

func (s *Service) Queue(ctx context.Context) (Queue, error) {
	var revision int64
	if err := s.db.QueryRowContext(ctx, "SELECT queue_revision FROM player_state WHERE singleton=1").Scan(&revision); err != nil {
		return Queue{}, fmt.Errorf("player: load queue revision: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT entry_id,track_id,status FROM player_queue ORDER BY position")
	if err != nil {
		return Queue{}, fmt.Errorf("player: load queue: %w", err)
	}
	var records []queueRecord
	for rows.Next() {
		var record queueRecord
		if err := rows.Scan(&record.id, &record.trackID, &record.status); err != nil {
			rows.Close()
			return Queue{}, fmt.Errorf("player: read queue entry: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return Queue{}, fmt.Errorf("player: iterate queue: %w", err)
	}
	if err := rows.Close(); err != nil {
		return Queue{}, fmt.Errorf("player: close queue: %w", err)
	}
	entries := make([]Entry, 0, len(records))
	for _, record := range records {
		track, trackErr := s.lib.Track(ctx, record.trackID)
		if trackErr != nil {
			if ctx.Err() != nil {
				return Queue{}, ctx.Err()
			}
			track = library.Track{ID: record.trackID, Genres: []string{}, Available: false}
		}
		entries = append(entries, Entry{ID: record.id, TrackID: record.trackID, Track: track, Status: record.status})
	}
	return Queue{Revision: revision, Entries: entries}, nil
}

func (s *Service) MutateQueue(ctx context.Context, mutation QueueMutation) (Queue, error) {
	if err := validQueueMutation(mutation); err != nil {
		return Queue{}, err
	}
	if mutation.Action == "append" || mutation.Action == "next" || mutation.Action == "replace" {
		for _, trackID := range mutation.TrackIDs {
			track, err := s.lib.Track(ctx, trackID)
			if err != nil || !track.Available {
				return Queue{}, fault.New(409, "TRACK_UNAVAILABLE", "One or more selected tracks are unavailable.")
			}
		}
	}

	s.opMu.Lock()
	err := s.mutateQueueLocked(ctx, mutation)
	s.opMu.Unlock()
	if err != nil {
		return Queue{}, err
	}
	s.notify("queue")
	if mutation.Action == "replace" {
		s.notify("player")
	}
	return s.Queue(ctx)
}

func validQueueMutation(mutation QueueMutation) error {
	if mutation.Revision < 0 {
		return fault.New(400, "INVALID_QUEUE_MUTATION", "The queue revision is invalid.")
	}
	switch mutation.Action {
	case "append", "next", "replace":
		if len(mutation.TrackIDs) == 0 {
			return fault.New(400, "INVALID_QUEUE_MUTATION", "At least one track is required.")
		}
		if len(mutation.TrackIDs) > 10000 {
			return fault.New(400, "QUEUE_LIMIT", "The queue cannot contain more than 10,000 entries.")
		}
		for _, id := range mutation.TrackIDs {
			if id == "" {
				return fault.New(400, "INVALID_QUEUE_MUTATION", "A track identifier is missing.")
			}
		}
	case "remove", "move":
		if mutation.EntryID == "" {
			return fault.New(400, "INVALID_QUEUE_MUTATION", "A queue entry is required.")
		}
	case "clear":
	default:
		return fault.New(400, "INVALID_QUEUE_MUTATION", "The queue action is not supported.")
	}
	return nil
}

func (s *Service) mutateQueueLocked(ctx context.Context, mutation QueueMutation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("player: begin queue mutation: %w", err)
	}
	defer tx.Rollback()
	var revision int64
	var state, currentEntryID string
	if err := tx.QueryRowContext(ctx, "SELECT queue_revision,state,current_entry_id FROM player_state WHERE singleton=1").Scan(&revision, &state, &currentEntryID); err != nil {
		return fmt.Errorf("player: load queue mutation state: %w", err)
	}
	if revision != mutation.Revision {
		return fault.New(409, "REVISION_CONFLICT", "The queue changed; refresh it and try again.")
	}
	var active int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_commands WHERE service_epoch=? AND status IN ('pending','running')", s.epoch).Scan(&active); err != nil {
		return fmt.Errorf("player: inspect active queue command: %w", err)
	}
	if active != 0 {
		return fault.New(409, "COMMAND_IN_PROGRESS", "Wait for the active playback command to finish.")
	}
	records, err := loadQueueRecords(ctx, tx)
	if err != nil {
		return err
	}
	if len(records)+len(mutation.TrackIDs) > 10000 && mutation.Action != "replace" {
		return fault.New(409, "QUEUE_LIMIT", "The queue cannot contain more than 10,000 entries.")
	}

	switch mutation.Action {
	case "append":
		records, err = s.insertQueueRecords(ctx, tx, records, len(records), mutation.TrackIDs)
	case "next":
		index := 0
		if currentEntryID != "" {
			index = queueRecordIndex(records, currentEntryID)
			if index < 0 {
				return fault.New(409, "QUEUE_CURSOR_INVALID", "The current queue entry is no longer present.")
			}
			index++
		}
		records, err = s.insertQueueRecords(ctx, tx, records, index, mutation.TrackIDs)
	case "replace":
		if state != StateStopped {
			return fault.New(409, "PLAYER_NOT_STOPPED", "Stop playback before replacing the queue.")
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM player_queue"); err == nil {
			records, err = s.insertQueueRecords(ctx, tx, nil, 0, mutation.TrackIDs)
		}
		if err == nil {
			_, err = tx.ExecContext(ctx, `UPDATE player_state SET revision=revision+1,current_entry_id='',play_id='',current_uri='',current_seekable=0,position_ms=0,duration_ms=0,observed_at='',resume_required=0,error='',control_action='',control_at='' WHERE singleton=1`)
		}
	case "remove":
		if mutation.EntryID == currentEntryID {
			return fault.New(409, "CURRENT_ENTRY_IMMUTABLE", "The current queue entry cannot be removed.")
		}
		index := queueRecordIndex(records, mutation.EntryID)
		if index < 0 {
			return fault.New(404, "QUEUE_ENTRY_NOT_FOUND", "The queue entry no longer exists.")
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM player_queue WHERE entry_id=?", mutation.EntryID); err == nil {
			records = append(records[:index], records[index+1:]...)
		}
	case "move":
		if mutation.EntryID == currentEntryID {
			return fault.New(409, "CURRENT_ENTRY_IMMUTABLE", "The current queue entry cannot be moved.")
		}
		if mutation.Index < 0 || mutation.Index >= len(records) {
			return fault.New(400, "INVALID_QUEUE_INDEX", "The destination queue position is invalid.")
		}
		index := queueRecordIndex(records, mutation.EntryID)
		if index < 0 {
			return fault.New(404, "QUEUE_ENTRY_NOT_FOUND", "The queue entry no longer exists.")
		}
		record := records[index]
		records = append(records[:index], records[index+1:]...)
		destination := mutation.Index
		if destination > len(records) {
			destination = len(records)
		}
		records = append(records, queueRecord{})
		copy(records[destination+1:], records[destination:])
		records[destination] = record
	case "clear":
		if currentEntryID == "" {
			_, err = tx.ExecContext(ctx, "DELETE FROM player_queue")
			records = nil
		} else {
			index := queueRecordIndex(records, currentEntryID)
			if index < 0 {
				return fault.New(409, "QUEUE_CURSOR_INVALID", "The current queue entry is no longer present.")
			}
			_, err = tx.ExecContext(ctx, "DELETE FROM player_queue WHERE position>?", records[index].position)
			records = records[:index+1]
		}
	}
	if err != nil {
		return fmt.Errorf("player: apply queue mutation: %w", err)
	}
	if err := rewriteQueuePositions(ctx, tx, records); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE player_state SET queue_revision=queue_revision+1 WHERE singleton=1"); err != nil {
		return fmt.Errorf("player: advance queue revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("player: commit queue mutation: %w", err)
	}
	return nil
}

func loadQueueRecords(ctx context.Context, tx *sql.Tx) ([]queueRecord, error) {
	rows, err := tx.QueryContext(ctx, "SELECT entry_id,track_id,status,position FROM player_queue ORDER BY position")
	if err != nil {
		return nil, fmt.Errorf("player: load queue records: %w", err)
	}
	defer rows.Close()
	var records []queueRecord
	for rows.Next() {
		var record queueRecord
		if err := rows.Scan(&record.id, &record.trackID, &record.status, &record.position); err != nil {
			return nil, fmt.Errorf("player: read queue record: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("player: iterate queue records: %w", err)
	}
	return records, nil
}

func (s *Service) insertQueueRecords(ctx context.Context, tx *sql.Tx, records []queueRecord, index int, trackIDs []string) ([]queueRecord, error) {
	inserted := make([]queueRecord, len(trackIDs))
	for i, trackID := range trackIDs {
		entryID, err := randomID("entry")
		if err != nil {
			return nil, fmt.Errorf("player: create queue entry identity: %w", err)
		}
		inserted[i] = queueRecord{id: entryID, trackID: trackID, status: EntryPending}
		if _, err := tx.ExecContext(ctx, "INSERT INTO player_queue(entry_id,position,track_id,status) VALUES(?,? ,?,'pending')", entryID, -(i + 1), trackID); err != nil {
			return nil, fmt.Errorf("player: insert queue entry: %w", err)
		}
	}
	result := make([]queueRecord, 0, len(records)+len(inserted))
	result = append(result, records[:index]...)
	result = append(result, inserted...)
	result = append(result, records[index:]...)
	return result, nil
}

func rewriteQueuePositions(ctx context.Context, tx *sql.Tx, records []queueRecord) error {
	if _, err := tx.ExecContext(ctx, "UPDATE player_queue SET position=-1000000-position"); err != nil {
		return fmt.Errorf("player: stage queue positions: %w", err)
	}
	for index, record := range records {
		if _, err := tx.ExecContext(ctx, "UPDATE player_queue SET position=? WHERE entry_id=?", index, record.id); err != nil {
			return fmt.Errorf("player: store queue position: %w", err)
		}
	}
	return nil
}

func queueRecordIndex(records []queueRecord, entryID string) int {
	for index := range records {
		if records[index].id == entryID {
			return index
		}
	}
	return -1
}
