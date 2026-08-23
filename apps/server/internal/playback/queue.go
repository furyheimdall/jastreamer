package playback

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

func (store *Store) Enqueue(ctx context.Context, request EnqueueRequest) (EnqueueResult, error) {
	if request.ZoneID == "" || request.IdempotencyKey == "" || len(request.Tracks) == 0 {
		return EnqueueResult{}, ErrInvalidRequest
	}
	if len(request.Tracks) > 10_000 {
		return EnqueueResult{}, ErrQueueLimit
	}
	for _, track := range request.Tracks {
		if track.ID == "" {
			return EnqueueResult{}, ErrInvalidRequest
		}
	}
	hash, err := hashEnqueue(request)
	if err != nil {
		return EnqueueResult{}, err
	}
	result := EnqueueResult{}
	err = store.transaction(ctx, func(db *sqliteDB) error {
		if err := ensureZone(db, request.ZoneID); err != nil {
			return err
		}
		replayed, replay, err := loadIdempotency(db, request, hash)
		if err != nil {
			return err
		}
		if replayed {
			result = replay
			return nil
		}
		zone, err := loadZone(db, request.ZoneID)
		if err != nil {
			return err
		}
		if zone.revision != request.ExpectedRevision {
			return ErrRevisionConflict
		}
		result.Revision = zone.revision + 1
		if err := cancelAutomaticPreviews(db, request.ZoneID, result.Revision); err != nil {
			return err
		}
		result.EntryIDs = make([]QueueEntryID, len(request.Tracks))
		stmt, err := db.prepare("INSERT INTO playback_queue(entry_id, zone_id, position, track_id, available, state, created_revision) VALUES (?, ?, ?, ?, ?, 'pending', ?)")
		if err != nil {
			return err
		}
		defer stmt.close()
		for index, track := range request.Tracks {
			if index%256 == 0 {
				if err := context.Cause(ctx); err != nil {
					return err
				}
			}
			position := zone.queueSequence + int64(index) + 1
			entryID := QueueEntryID(fmt.Sprintf("%s:q:%020d", request.ZoneID, position))
			available := int64(0)
			if track.Available {
				available = 1
			}
			if err := stmt.bindText(1, string(entryID)); err != nil {
				return err
			}
			if err := stmt.bindText(2, string(request.ZoneID)); err != nil {
				return err
			}
			if err := stmt.bindInt64(3, position); err != nil {
				return err
			}
			if err := stmt.bindText(4, string(track.ID)); err != nil {
				return err
			}
			if err := stmt.bindInt64(5, available); err != nil {
				return err
			}
			if err := stmt.bindInt64(6, int64(result.Revision)); err != nil {
				return err
			}
			if _, err := stmt.step(); err != nil {
				return err
			}
			if err := stmt.reset(); err != nil {
				return err
			}
			result.EntryIDs[index] = entryID
		}
		if err := execBound(db, "UPDATE playback_zones SET revision = ?, queue_sequence = ? WHERE zone_id = ?", func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(result.Revision)); err != nil {
				return err
			}
			if err := stmt.bindInt64(2, zone.queueSequence+int64(len(request.Tracks))); err != nil {
				return err
			}
			return stmt.bindText(3, string(request.ZoneID))
		}); err != nil {
			return err
		}
		return execBound(db, "INSERT INTO playback_idempotency(zone_id, idempotency_key, operation, request_hash, result_revision) VALUES (?, ?, 'enqueue', ?, ?)", func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, string(request.ZoneID)); err != nil {
				return err
			}
			if err := stmt.bindText(2, request.IdempotencyKey); err != nil {
				return err
			}
			if err := stmt.bindText(3, hash); err != nil {
				return err
			}
			return stmt.bindInt64(4, int64(result.Revision))
		})
	})
	return result, err
}

func hashEnqueue(request EnqueueRequest) (string, error) {
	hash := sha256.New()
	var revision [8]byte
	binary.BigEndian.PutUint64(revision[:], uint64(request.ExpectedRevision))
	if _, err := hash.Write(revision[:]); err != nil {
		return "", fmt.Errorf("hash enqueue revision: %w", err)
	}
	for _, track := range request.Tracks {
		if _, err := hash.Write([]byte(track.ID)); err != nil {
			return "", fmt.Errorf("hash enqueue track: %w", err)
		}
		if _, err := hash.Write([]byte{0}); err != nil {
			return "", fmt.Errorf("hash enqueue separator: %w", err)
		}
		if track.Available {
			if _, err := hash.Write([]byte{1}); err != nil {
				return "", fmt.Errorf("hash enqueue availability: %w", err)
			}
		} else {
			if _, err := hash.Write([]byte{2}); err != nil {
				return "", fmt.Errorf("hash enqueue availability: %w", err)
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func loadIdempotency(db *sqliteDB, request EnqueueRequest, hash string) (bool, EnqueueResult, error) {
	stmt, err := db.prepare("SELECT request_hash, result_revision FROM playback_idempotency WHERE zone_id = ? AND idempotency_key = ?")
	if err != nil {
		return false, EnqueueResult{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(request.ZoneID)); err != nil {
		return false, EnqueueResult{}, err
	}
	if err := stmt.bindText(2, request.IdempotencyKey); err != nil {
		return false, EnqueueResult{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return false, EnqueueResult{}, err
	}
	if !row {
		return false, EnqueueResult{}, nil
	}
	if stmt.text(0) != hash {
		return false, EnqueueResult{}, ErrIdempotencyConflict
	}
	revision := Revision(stmt.int64(1))
	entries, err := queueIDsAtRevision(db, request.ZoneID, revision)
	return true, EnqueueResult{Revision: revision, EntryIDs: entries}, err
}

func queueIDsAtRevision(db *sqliteDB, zoneID ZoneID, revision Revision) ([]QueueEntryID, error) {
	stmt, err := db.prepare("SELECT entry_id FROM playback_queue WHERE zone_id = ? AND created_revision = ? ORDER BY position")
	if err != nil {
		return nil, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(zoneID)); err != nil {
		return nil, err
	}
	if err := stmt.bindInt64(2, int64(revision)); err != nil {
		return nil, err
	}
	var entries []QueueEntryID
	for {
		row, err := stmt.step()
		if err != nil {
			return nil, err
		}
		if !row {
			break
		}
		entries = append(entries, QueueEntryID(stmt.text(0)))
	}
	return entries, nil
}

func (store *Store) Snapshot(ctx context.Context, zoneID ZoneID) (ZoneSnapshot, error) {
	snapshot := ZoneSnapshot{ZoneID: zoneID, Transport: TransportIdle}
	err := store.read(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		snapshot.Revision = zone.revision
		snapshot.Transport = zone.transport
		snapshot.SessionID = zone.sessionID
		snapshot.SessionSeed = zone.seed
		snapshot.CurrentPlay = zone.currentPlay
		stmt, err := db.prepare("SELECT entry_id, track_id, state, position FROM playback_queue WHERE zone_id = ? AND state != 'removed' ORDER BY position")
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(zoneID)); err != nil {
			return err
		}
		for {
			row, err := stmt.step()
			if err != nil {
				return err
			}
			if !row {
				break
			}
			snapshot.Queue = append(snapshot.Queue, QueueEntry{ID: QueueEntryID(stmt.text(0)), TrackID: TrackID(stmt.text(1)), State: QueueState(stmt.text(2)), Position: stmt.int64(3)})
		}
		return nil
	})
	return snapshot, err
}
