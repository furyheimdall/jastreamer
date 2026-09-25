package player

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"

	"github.com/jastreamer/jastreamer-server/internal/fault"
)

const (
	RepeatOff = "off"
	RepeatAll = "all"
	RepeatOne = "one"
)

type ModeUpdate struct {
	Shuffle    *bool
	RepeatMode *string
}

type playbackMode struct {
	shuffle    bool
	repeatMode string
}

func (s *Service) UpdateMode(ctx context.Context, update ModeUpdate) (State, error) {
	if update.Shuffle == nil && update.RepeatMode == nil {
		return State{}, fault.New(400, "INVALID_PLAYBACK_MODE", "At least one playback mode is required.")
	}
	if update.RepeatMode != nil && !validRepeatMode(*update.RepeatMode) {
		return State{}, fault.New(400, "INVALID_PLAYBACK_MODE", "The repeat mode is not supported.")
	}

	s.opMu.Lock()
	changed := false
	tx, err := s.db.BeginTx(ctx, nil)
	if err == nil {
		defer tx.Rollback()
		var active int
		err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM player_commands WHERE service_epoch=? AND status IN ('pending','running')", s.epoch).Scan(&active)
		if err == nil && active != 0 {
			err = fault.New(409, "COMMAND_IN_PROGRESS", "Wait for the active playback command to finish.")
		}
		mode := playbackMode{}
		if err == nil {
			mode, err = loadPlaybackModeTx(ctx, tx)
		}
		var currentEntryID string
		if err == nil {
			err = tx.QueryRowContext(ctx, "SELECT current_entry_id FROM player_state WHERE singleton=1").Scan(&currentEntryID)
		}
		shuffleChanged := false
		if err == nil && update.Shuffle != nil {
			shuffleChanged = mode.shuffle != *update.Shuffle
			changed = changed || shuffleChanged
			mode.shuffle = *update.Shuffle
		}
		if err == nil && update.RepeatMode != nil {
			changed = changed || mode.repeatMode != *update.RepeatMode
			mode.repeatMode = *update.RepeatMode
		}
		if err == nil && changed {
			_, err = tx.ExecContext(ctx, "UPDATE player_mode SET shuffle=?,repeat_mode=? WHERE singleton=1", boolInt(mode.shuffle), mode.repeatMode)
		}
		if err == nil && shuffleChanged {
			if mode.shuffle {
				var records []queueRecord
				records, err = loadQueueRecords(ctx, tx)
				if err == nil {
					if queueRecordIndex(records, currentEntryID) >= 0 {
						err = rebuildShuffleTraversalTx(ctx, tx, records, currentEntryID, false)
					} else {
						err = rebuildShuffleTraversalTx(ctx, tx, records, "", false)
						if err == nil && currentEntryID != "" {
							var binding currentBinding
							var found bool
							binding, found, err = loadCurrentBindingTx(ctx, tx, currentEntryID)
							if err == nil && found {
								binding.shuffleIndex = 0
								err = storeCurrentBindingTx(ctx, tx, binding)
							}
						}
					}
				}
			} else {
				_, err = tx.ExecContext(ctx, "DELETE FROM player_shuffle")
			}
		}
		if err == nil && changed {
			_, err = tx.ExecContext(ctx, "UPDATE player_state SET revision=revision+1 WHERE singleton=1")
		}
		if err == nil {
			err = tx.Commit()
		}
	}
	s.opMu.Unlock()
	if err != nil {
		if _, ok := err.(*fault.Error); ok {
			return State{}, err
		}
		return State{}, fmt.Errorf("player: update playback mode: %w", err)
	}
	if changed {
		s.notify("player")
	}
	return s.Snapshot(ctx)
}

func validRepeatMode(mode string) bool {
	return mode == RepeatOff || mode == RepeatAll || mode == RepeatOne
}

func loadPlaybackModeTx(ctx context.Context, tx *sql.Tx) (playbackMode, error) {
	var shuffle int
	var mode playbackMode
	if err := tx.QueryRowContext(ctx, "SELECT shuffle,repeat_mode FROM player_mode WHERE singleton=1").Scan(&shuffle, &mode.repeatMode); err != nil {
		return playbackMode{}, err
	}
	mode.shuffle = shuffle != 0
	return mode, nil
}

func (s *Service) adjacentEntry(ctx context.Context, current string, direction int, allowRepeatAll bool) (queueRecord, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return queueRecord{}, false, err
	}
	defer tx.Rollback()
	entry, found, err := s.adjacentEntryTx(ctx, tx, current, direction, allowRepeatAll)
	if err != nil {
		return queueRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return queueRecord{}, false, err
	}
	return entry, found, nil
}

func (s *Service) adjacentEntryTx(ctx context.Context, tx *sql.Tx, current string, direction int, allowRepeatAll bool) (queueRecord, bool, error) {
	mode, err := loadPlaybackModeTx(ctx, tx)
	if err != nil {
		return queueRecord{}, false, err
	}
	if !mode.shuffle {
		return adjacentStoredEntryTx(ctx, tx, current, direction, allowRepeatAll && mode.repeatMode == RepeatAll)
	}
	records, err := loadQueueRecords(ctx, tx)
	if err != nil {
		return queueRecord{}, false, err
	}
	if err := ensureShuffleTraversalTx(ctx, tx, records, current); err != nil {
		return queueRecord{}, false, err
	}
	ids, err := loadShuffleIDsTx(ctx, tx)
	if err != nil {
		return queueRecord{}, false, err
	}
	if len(ids) == 0 {
		return queueRecord{}, false, nil
	}
	index := -1
	detached := false
	if current != "" {
		index = stringIndex(ids, current)
		if index < 0 {
			binding, found, loadErr := loadCurrentBindingTx(ctx, tx, current)
			if loadErr != nil {
				return queueRecord{}, false, loadErr
			}
			if !found {
				return queueRecord{}, false, nil
			}
			index = binding.shuffleIndex
			detached = true
		}
	}
	targetIndex := index + 1
	if detached {
		targetIndex = index
	}
	if direction < 0 {
		targetIndex = index - 1
	}
	if targetIndex >= 0 && targetIndex < len(ids) {
		return entryByIDTx(ctx, tx, ids[targetIndex])
	}
	if direction < 0 || !allowRepeatAll || mode.repeatMode != RepeatAll {
		return queueRecord{}, false, nil
	}
	if detached {
		err = rebuildShuffleTraversalTx(ctx, tx, records, "", false)
	} else {
		err = rebuildShuffleTraversalTx(ctx, tx, records, current, true)
	}
	if err != nil {
		return queueRecord{}, false, err
	}
	ids, err = loadShuffleIDsTx(ctx, tx)
	if err != nil || len(ids) == 0 {
		return queueRecord{}, false, err
	}
	if detached {
		binding, found, loadErr := loadCurrentBindingTx(ctx, tx, current)
		if loadErr != nil {
			return queueRecord{}, false, loadErr
		}
		if found {
			binding.shuffleIndex = 0
			if err := storeCurrentBindingTx(ctx, tx, binding); err != nil {
				return queueRecord{}, false, err
			}
		}
	}
	return entryByIDTx(ctx, tx, ids[0])
}

func adjacentStoredEntryTx(ctx context.Context, tx *sql.Tx, current string, direction int, wrap bool) (queueRecord, bool, error) {
	if current == "" {
		return firstStoredEntryTx(ctx, tx)
	}
	var position int
	detached := false
	if err := tx.QueryRowContext(ctx, "SELECT position FROM player_queue WHERE entry_id=?", current).Scan(&position); err != nil {
		if err != sql.ErrNoRows {
			return queueRecord{}, false, err
		}
		binding, found, loadErr := loadCurrentBindingTx(ctx, tx, current)
		if loadErr != nil {
			return queueRecord{}, false, loadErr
		}
		if !found {
			return queueRecord{}, false, nil
		}
		position = binding.queueIndex
		detached = true
	}
	operator, order := ">", "ASC"
	if detached {
		operator = ">="
	}
	if direction < 0 {
		operator, order = "<", "DESC"
	}
	query := fmt.Sprintf("SELECT entry_id,track_id,status,position FROM player_queue WHERE position%s? ORDER BY position %s LIMIT 1", operator, order)
	var entry queueRecord
	err := tx.QueryRowContext(ctx, query, position).Scan(&entry.id, &entry.trackID, &entry.status, &entry.position)
	if err == nil {
		return entry, true, nil
	}
	if err != sql.ErrNoRows {
		return queueRecord{}, false, err
	}
	if direction > 0 && wrap {
		return firstStoredEntryTx(ctx, tx)
	}
	return queueRecord{}, false, nil
}

func firstStoredEntryTx(ctx context.Context, tx *sql.Tx) (queueRecord, bool, error) {
	var entry queueRecord
	err := tx.QueryRowContext(ctx, "SELECT entry_id,track_id,status,position FROM player_queue ORDER BY position LIMIT 1").Scan(&entry.id, &entry.trackID, &entry.status, &entry.position)
	if err == sql.ErrNoRows {
		return queueRecord{}, false, nil
	}
	return entry, err == nil, err
}

func entryByIDTx(ctx context.Context, tx *sql.Tx, entryID string) (queueRecord, bool, error) {
	var entry queueRecord
	err := tx.QueryRowContext(ctx, "SELECT entry_id,track_id,status,position FROM player_queue WHERE entry_id=?", entryID).Scan(&entry.id, &entry.trackID, &entry.status, &entry.position)
	if err == sql.ErrNoRows {
		return queueRecord{}, false, nil
	}
	return entry, err == nil, err
}

func currentEntryTx(ctx context.Context, tx *sql.Tx, entryID string) (queueRecord, bool, error) {
	entry, found, err := entryByIDTx(ctx, tx, entryID)
	if found || err != nil {
		return entry, found, err
	}
	binding, found, err := loadCurrentBindingTx(ctx, tx, entryID)
	if !found || err != nil {
		return queueRecord{}, false, err
	}
	return queueRecord{
		id: binding.entryID, trackID: binding.trackID, status: EntryPending, position: binding.queueIndex,
	}, true, nil
}

func ensureShuffleTraversalTx(ctx context.Context, tx *sql.Tx, records []queueRecord, current string) error {
	ids, err := loadShuffleIDsTx(ctx, tx)
	if err != nil {
		return err
	}
	queueIDs := make(map[string]struct{}, len(records))
	for _, record := range records {
		queueIDs[record.id] = struct{}{}
	}
	filtered := make([]string, 0, len(records))
	present := make(map[string]struct{}, len(records))
	for _, id := range ids {
		if _, ok := queueIDs[id]; ok {
			filtered = append(filtered, id)
			present[id] = struct{}{}
		}
	}
	if current != "" {
		if _, inQueue := queueIDs[current]; inQueue {
			if _, ok := present[current]; !ok {
				return rebuildShuffleTraversalTx(ctx, tx, records, current, false)
			}
		}
	}
	missing := make([]string, 0, len(records)-len(filtered))
	for _, record := range records {
		if _, ok := present[record.id]; !ok {
			missing = append(missing, record.id)
		}
	}
	shuffleStrings(missing)
	filtered = append(filtered, missing...)
	if len(filtered) != len(ids) || len(missing) != 0 {
		return rewriteShuffleTx(ctx, tx, filtered)
	}
	return nil
}

func rebuildShuffleTraversalTx(ctx context.Context, tx *sql.Tx, records []queueRecord, current string, currentLast bool) error {
	ids := make([]string, 0, len(records))
	currentFound := false
	for _, record := range records {
		if record.id == current {
			currentFound = true
			continue
		}
		ids = append(ids, record.id)
	}
	shuffleStrings(ids)
	if currentFound {
		if currentLast {
			ids = append(ids, current)
		} else {
			ids = append(ids, "")
			copy(ids[1:], ids[:len(ids)-1])
			ids[0] = current
		}
	}
	return rewriteShuffleTx(ctx, tx, ids)
}

func recordTraversalSelectionTx(ctx context.Context, tx *sql.Tx, current, target string) error {
	mode, err := loadPlaybackModeTx(ctx, tx)
	if err != nil || !mode.shuffle || target == "" || target == current {
		return err
	}
	records, err := loadQueueRecords(ctx, tx)
	if err != nil {
		return err
	}
	if err := ensureShuffleTraversalTx(ctx, tx, records, current); err != nil {
		return err
	}
	ids, err := loadShuffleIDsTx(ctx, tx)
	if err != nil {
		return err
	}
	targetIndex := stringIndex(ids, target)
	if targetIndex < 0 {
		return fmt.Errorf("shuffle target %q is absent", target)
	}
	currentIndex := stringIndex(ids, current)
	if currentIndex < 0 {
		binding, found, loadErr := loadCurrentBindingTx(ctx, tx, current)
		if loadErr != nil {
			return loadErr
		}
		if found {
			currentIndex = binding.shuffleIndex - 1
		} else {
			currentIndex = -1
		}
	}
	if targetIndex <= currentIndex+1 {
		return nil
	}
	ids = append(ids[:targetIndex], ids[targetIndex+1:]...)
	insertAt := currentIndex + 1
	ids = append(ids, "")
	copy(ids[insertAt+1:], ids[insertAt:])
	ids[insertAt] = target
	return rewriteShuffleTx(ctx, tx, ids)
}

func syncShuffleQueueMutationTx(ctx context.Context, tx *sql.Tx, action, current string, before, after []queueRecord, beforeIDs []string, binding *currentBinding) error {
	mode, err := loadPlaybackModeTx(ctx, tx)
	if err != nil || !mode.shuffle {
		return err
	}
	if action == "replace" {
		return rebuildShuffleTraversalTx(ctx, tx, after, "", false)
	}
	if action == "move" {
		return nil
	}
	ids, err := loadShuffleIDsTx(ctx, tx)
	if err != nil {
		return err
	}
	afterSet := make(map[string]struct{}, len(after))
	beforeSet := make(map[string]struct{}, len(before))
	for _, record := range before {
		beforeSet[record.id] = struct{}{}
	}
	for _, record := range after {
		afterSet[record.id] = struct{}{}
	}
	cursor := 0
	detached := binding != nil && queueRecordIndex(after, current) < 0
	if binding != nil {
		cursor = binding.shuffleIndex
		if index := stringIndex(beforeIDs, current); index >= 0 {
			cursor = index
		}
		if detached {
			for index, id := range beforeIDs {
				if id != current {
					if _, remains := afterSet[id]; !remains && index < cursor {
						cursor--
					}
				}
			}
		}
	}
	filtered := ids[:0]
	for _, id := range ids {
		if _, ok := afterSet[id]; ok {
			filtered = append(filtered, id)
		}
	}
	ids = filtered
	var inserted []string
	for _, record := range after {
		if _, ok := beforeSet[record.id]; !ok {
			inserted = append(inserted, record.id)
		}
	}
	if action == "next" {
		insertAt := stringIndex(ids, current) + 1
		if detached {
			insertAt = cursor
		}
		if insertAt < 0 {
			insertAt = 0
		}
		if insertAt > len(ids) {
			insertAt = len(ids)
		}
		result := make([]string, 0, len(ids)+len(inserted))
		result = append(result, ids[:insertAt]...)
		result = append(result, inserted...)
		result = append(result, ids[insertAt:]...)
		ids = result
	} else if len(inserted) != 0 {
		// Ordinary appends must not displace an explicitly scheduled play-next batch.
		shuffleStrings(inserted)
		ids = append(ids, inserted...)
	}
	if err := rewriteShuffleTx(ctx, tx, ids); err != nil {
		return err
	}
	if binding != nil {
		if index := stringIndex(ids, current); index >= 0 {
			binding.shuffleIndex = index
		} else {
			if cursor < 0 {
				cursor = 0
			}
			if cursor > len(ids) {
				cursor = len(ids)
			}
			binding.shuffleIndex = cursor
		}
	}
	return nil
}

func loadShuffleIDsTx(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT entry_id FROM player_shuffle ORDER BY ordinal")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func rewriteShuffleTx(ctx context.Context, tx *sql.Tx, ids []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM player_shuffle"); err != nil {
		return err
	}
	for ordinal, id := range ids {
		if _, err := tx.ExecContext(ctx, "INSERT INTO player_shuffle(entry_id,ordinal) VALUES(?,?)", id, ordinal); err != nil {
			return err
		}
	}
	return nil
}

func shuffleStrings(values []string) {
	rand.Shuffle(len(values), func(i, j int) {
		values[i], values[j] = values[j], values[i]
	})
}

func stringIndex(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
