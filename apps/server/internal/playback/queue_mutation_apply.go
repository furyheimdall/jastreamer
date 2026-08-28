package playback

import "fmt"

func activeQueueEntries(db *sqliteDB, zoneID ZoneID) ([]QueueEntry, error) {
	stmt, err := db.prepare(`SELECT entry_id,track_id,state,position FROM playback_queue
		WHERE zone_id=? AND state IN ('pending','blocked','reserved','playing') ORDER BY position`)
	if err != nil {
		return nil, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return nil, err
	}
	entries := []QueueEntry{}
	for {
		row, err := stmt.step()
		if err != nil {
			return nil, err
		}
		if !row {
			return entries, nil
		}
		entries = append(entries, QueueEntry{
			ID: QueueEntryID(stmt.text(0)), TrackID: TrackID(stmt.text(1)),
			State: QueueState(stmt.text(2)), Position: stmt.int64(3),
		})
	}
}

type queueMutationApply struct {
	request  QueueMutationRequest
	entries  []QueueEntry
	revision Revision
	sequence int64
	result   *QueueMutationResult
}

func applyQueueMutation(db *sqliteDB, mutation queueMutationApply) error {
	request, entries, revision := mutation.request, mutation.entries, mutation.revision
	switch request.Command {
	case QueueAppend, QueueInsert:
		return insertQueueTracks(db, mutation)
	case QueueRemove:
		entry, found := findQueueEntry(entries, request.EntryID)
		if !found {
			return ErrQueueEntryNotFound
		}
		if entry.State == QueueReserved || entry.State == QueuePlaying {
			return ErrQueueEntryActive
		}
		return removeQueueEntry(db, queueRemoval{zoneID: request.ZoneID, entryID: request.EntryID, revision: revision})
	case QueueMove:
		entry, found := findQueueEntry(entries, request.EntryID)
		if !found || (request.BeforeEntryID != "" && !queueContains(entries, request.BeforeEntryID)) {
			return ErrQueueEntryNotFound
		}
		if entry.State == QueueReserved || entry.State == QueuePlaying {
			return ErrQueueEntryActive
		}
		return reorderQueue(db, request.ZoneID, movedEntries(entries, request.EntryID, request.BeforeEntryID))
	case QueueClear:
		for _, entry := range entries {
			if entry.State == QueueReserved || entry.State == QueuePlaying {
				return ErrQueueEntryActive
			}
		}
		return clearQueue(db, request.ZoneID, revision)
	case QueueRetryBlocked, QueueSkipBlocked:
		if len(entries) == 0 || entries[0].State != QueueBlocked {
			return ErrQueueHeadState
		}
		if request.Command == QueueRetryBlocked {
			return setQueueState(db, queueTransition{entryID: entries[0].ID, state: QueuePending, revision: revision})
		}
		return removeQueueEntry(db, queueRemoval{zoneID: request.ZoneID, entryID: entries[0].ID, revision: revision})
	default:
		return ErrInvalidRequest
	}
}

func insertQueueTracks(db *sqliteDB, mutation queueMutationApply) error {
	request, entries := mutation.request, mutation.entries
	revision, sequence, result := mutation.revision, mutation.sequence, mutation.result
	before := len(entries)
	if request.Command == QueueInsert && request.BeforeEntryID != "" {
		before = -1
		for index, entry := range entries {
			if entry.ID == request.BeforeEntryID {
				before = index
				break
			}
		}
		if before < 0 {
			return ErrQueueEntryNotFound
		}
	}
	result.EntryIDs = make([]QueueEntryID, len(request.Tracks))
	inserted := make([]QueueEntry, len(request.Tracks))
	stmt, err := db.prepare(`INSERT INTO playback_queue(entry_id,zone_id,position,track_id,available,state,created_revision)
		VALUES (?,?,?,?,?,'pending',?)`)
	if err != nil {
		return err
	}
	defer stmt.close()
	for index, track := range request.Tracks {
		id := QueueEntryID(fmt.Sprintf("%s:q:%020d", request.ZoneID, sequence+int64(index)+1))
		result.EntryIDs[index] = id
		inserted[index] = QueueEntry{ID: id, TrackID: track.ID, State: QueuePending}
		values := []string{string(id), string(request.ZoneID), string(track.ID)}
		if err := stmt.bindText(1, values[0]); err != nil {
			return err
		}
		if err := stmt.bindText(2, values[1]); err != nil {
			return err
		}
		if err := stmt.bindInt64(3, -(sequence + int64(index) + 1)); err != nil {
			return err
		}
		if err := stmt.bindText(4, values[2]); err != nil {
			return err
		}
		available := int64(0)
		if track.Available {
			available = 1
		}
		if err := stmt.bindInt64(5, available); err != nil {
			return err
		}
		if err := stmt.bindInt64(6, int64(revision)); err != nil {
			return err
		}
		if _, err := stmt.step(); err != nil {
			return err
		}
		if err := stmt.reset(); err != nil {
			return err
		}
	}
	ordered := append([]QueueEntry{}, entries[:before]...)
	ordered = append(ordered, inserted...)
	ordered = append(ordered, entries[before:]...)
	return reorderQueue(db, request.ZoneID, ordered)
}

func findQueueEntry(entries []QueueEntry, id QueueEntryID) (QueueEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return QueueEntry{}, false
}

func queueContains(entries []QueueEntry, id QueueEntryID) bool {
	_, found := findQueueEntry(entries, id)
	return found
}

func movedEntries(entries []QueueEntry, id, before QueueEntryID) []QueueEntry {
	ordered := make([]QueueEntry, 0, len(entries))
	moving, _ := findQueueEntry(entries, id)
	for _, entry := range entries {
		if entry.ID != id {
			ordered = append(ordered, entry)
		}
	}
	index := len(ordered)
	if before != "" {
		for candidate, entry := range ordered {
			if entry.ID == before {
				index = candidate
				break
			}
		}
	}
	ordered = append(ordered, QueueEntry{})
	copy(ordered[index+1:], ordered[index:])
	ordered[index] = moving
	return ordered
}

func reorderQueue(db *sqliteDB, zoneID ZoneID, entries []QueueEntry) error {
	for index, entry := range entries {
		if err := execBound(db, "UPDATE playback_queue SET position=? WHERE zone_id=? AND entry_id=?", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, -1_000_000+int64(index)); err != nil {
				return err
			}
			if err := stmt.bindText(2, string(zoneID)); err != nil {
				return err
			}
			return stmt.bindText(3, string(entry.ID))
		}); err != nil {
			return err
		}
	}
	return normalizeQueuePositions(db, zoneID)
}
