package playback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrQueueEntryNotFound = errors.New("playback: queue entry not found")
	ErrQueueEntryActive   = errors.New("playback: active queue entry cannot be mutated")
	ErrQueueHeadState     = errors.New("playback: queue head is not blocked")
)

type QueueCommand string

const (
	QueueAppend       QueueCommand = "append"
	QueueInsert       QueueCommand = "insert"
	QueueRemove       QueueCommand = "remove"
	QueueMove         QueueCommand = "move"
	QueueClear        QueueCommand = "clear"
	QueueRetryBlocked QueueCommand = "retry_blocked"
	QueueSkipBlocked  QueueCommand = "skip_blocked"
)

type QueueMutationRequest struct {
	ZoneID           ZoneID
	IdempotencyKey   string
	ExpectedRevision Revision
	Command          QueueCommand
	Tracks           []QueueTrack
	EntryID          QueueEntryID
	BeforeEntryID    QueueEntryID
}

type QueueMutationResult struct {
	Revision Revision
	EntryIDs []QueueEntryID
	Replayed bool
}

type queueMutationHashInput struct {
	Revision      Revision     `json:"revision"`
	Command       QueueCommand `json:"command"`
	Tracks        []QueueTrack `json:"tracks,omitempty"`
	EntryID       QueueEntryID `json:"entry_id,omitempty"`
	BeforeEntryID QueueEntryID `json:"before_entry_id,omitempty"`
}

func (store *Store) MutateQueue(ctx context.Context, request QueueMutationRequest) (QueueMutationResult, error) {
	if err := validQueueMutation(request); err != nil {
		return QueueMutationResult{}, err
	}
	encoded, err := json.Marshal(queueMutationHashInput{
		Revision: request.ExpectedRevision, Command: request.Command, Tracks: request.Tracks,
		EntryID: request.EntryID, BeforeEntryID: request.BeforeEntryID,
	})
	if err != nil {
		return QueueMutationResult{}, fmt.Errorf("encode queue mutation: %w", err)
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	result := QueueMutationResult{}
	err = store.transaction(ctx, func(db *sqliteDB) error {
		if err := ensureZone(db, request.ZoneID); err != nil {
			return err
		}
		replay := mutationReplayQuery{
			zoneID: request.ZoneID, key: request.IdempotencyKey, operation: request.Command, hash: hash,
		}
		replayed, revision, err := loadMutationReplay(db, replay)
		if err != nil {
			return err
		}
		if replayed {
			result = QueueMutationResult{Revision: revision, Replayed: true}
			result.EntryIDs, err = queueIDsAtRevision(db, request.ZoneID, revision)
			return err
		}
		zone, err := loadZone(db, request.ZoneID)
		if err != nil {
			return err
		}
		if zone.revision != request.ExpectedRevision {
			return ErrRevisionConflict
		}
		entries, err := activeQueueEntries(db, request.ZoneID)
		if err != nil {
			return err
		}
		if len(entries)+len(request.Tracks) > 10_000 {
			return ErrQueueLimit
		}
		result.Revision = zone.revision + 1
		mutation := queueMutationApply{
			request: request, entries: entries, revision: result.Revision,
			sequence: zone.queueSequence, result: &result,
		}
		if err := applyQueueMutation(db, mutation); err != nil {
			return err
		}
		if err := normalizeQueuePositions(db, request.ZoneID); err != nil {
			return err
		}
		if err := execBound(db, "UPDATE playback_zones SET revision=?,queue_sequence=? WHERE zone_id=?", func(stmt *sqliteStmt) error {
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
		return recordQueueMutation(db, queueMutationRecord{request: request, hash: hash, revision: result.Revision})
	})
	return result, err
}

func validQueueMutation(request QueueMutationRequest) error {
	if request.ZoneID == "" || request.IdempotencyKey == "" {
		return ErrInvalidRequest
	}
	switch request.Command {
	case QueueAppend, QueueInsert:
		if len(request.Tracks) == 0 {
			return ErrInvalidRequest
		}
		for _, track := range request.Tracks {
			if track.ID == "" {
				return ErrInvalidRequest
			}
		}
	case QueueRemove, QueueMove:
		if request.EntryID == "" {
			return ErrInvalidRequest
		}
	case QueueClear, QueueRetryBlocked, QueueSkipBlocked:
	default:
		return ErrInvalidRequest
	}
	return nil
}

type mutationReplayQuery struct {
	zoneID    ZoneID
	key       string
	operation QueueCommand
	hash      string
}

func loadMutationReplay(db *sqliteDB, query mutationReplayQuery) (bool, Revision, error) {
	stmt, err := db.prepare("SELECT operation,request_hash,result_revision FROM playback_idempotency WHERE zone_id=? AND idempotency_key=?")
	if err != nil {
		return false, 0, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(query.zoneID)); err != nil {
		return false, 0, err
	}
	if err := stmt.bindText(2, query.key); err != nil {
		return false, 0, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return false, 0, err
	}
	if stmt.text(0) != string(query.operation) || stmt.text(1) != query.hash {
		return false, 0, ErrIdempotencyConflict
	}
	return true, Revision(stmt.int64(2)), nil
}

type queueMutationRecord struct {
	request  QueueMutationRequest
	hash     string
	revision Revision
}

func recordQueueMutation(db *sqliteDB, record queueMutationRecord) error {
	request := record.request
	return execBound(db, "INSERT INTO playback_idempotency(zone_id,idempotency_key,operation,request_hash,result_revision) VALUES (?,?,?,?,?)", func(stmt *sqliteStmt) error {
		values := []string{string(request.ZoneID), request.IdempotencyKey, string(request.Command), record.hash}
		for index, value := range values {
			if err := stmt.bindText(index+1, value); err != nil {
				return err
			}
		}
		return stmt.bindInt64(5, int64(record.revision))
	})
}
