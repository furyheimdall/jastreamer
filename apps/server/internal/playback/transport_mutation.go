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
	ErrRendererOffline       = errors.New("playback: assigned renderer is offline")
	ErrRendererRequired      = errors.New("playback: zone has no assigned renderer")
	ErrQueueEmpty            = errors.New("playback: queue is empty")
	ErrQueueBlocked          = errors.New("playback: queue head is unavailable")
	ErrUnsupportedCapability = errors.New("playback: renderer capability is unsupported")
)

type TransportCommand string

const (
	TransportStart    TransportCommand = "start"
	TransportPlay     TransportCommand = "play"
	TransportPause    TransportCommand = "pause"
	TransportResume   TransportCommand = "resume"
	TransportStop     TransportCommand = "stop"
	TransportSeek     TransportCommand = "seek"
	TransportSkip     TransportCommand = "skip"
	TransportPrevious TransportCommand = "previous"
)

type TransportMutationStatus string

const TransportMutationPending TransportMutationStatus = "pending"

type TransportMutationRequest struct {
	ZoneID           ZoneID
	IdempotencyKey   string
	ExpectedRevision Revision
	Command          TransportCommand
	PositionMS       int64
}

type TransportMutationResult struct {
	Revision        Revision
	CommandID       string
	PlayID          PlayID
	TrackID         TrackID
	QueueEntryID    QueueEntryID
	SourcePlayID    PlayID
	PhysicalCommand TransportCommand
	Status          TransportMutationStatus
	Replayed        bool
}

type transportHashInput struct {
	Revision   Revision         `json:"revision"`
	Command    TransportCommand `json:"command"`
	PositionMS int64            `json:"position_ms"`
}

func (store *Store) MutateTransport(ctx context.Context, request TransportMutationRequest) (TransportMutationResult, error) {
	if request.ZoneID == "" || request.IdempotencyKey == "" || request.PositionMS < 0 || !validTransportCommand(request.Command) {
		return TransportMutationResult{}, ErrInvalidRequest
	}
	encoded, err := json.Marshal(transportHashInput{Revision: request.ExpectedRevision, Command: request.Command, PositionMS: request.PositionMS})
	if err != nil {
		return TransportMutationResult{}, fmt.Errorf("encode transport mutation: %w", err)
	}
	digest := sha256.Sum256(encoded)
	hash := hex.EncodeToString(digest[:])
	result := TransportMutationResult{Status: TransportMutationPending}
	err = store.transaction(ctx, func(db *sqliteDB) error {
		replay := mutationReplayQuery{
			zoneID: request.ZoneID, key: request.IdempotencyKey,
			operation: QueueCommand("transport:" + request.Command), hash: hash,
		}
		replayed, revision, err := loadMutationReplay(db, replay)
		if err != nil {
			return err
		}
		if replayed {
			result.Revision, result.Replayed = revision, true
			return loadTransportReplayResult(db, request, &result)
		}
		zone, renderer, err := transportTarget(db, request.ZoneID)
		if err != nil {
			return err
		}
		if zone.revision != request.ExpectedRevision {
			return ErrRevisionConflict
		}
		plan, err := planTransportMutation(db, request, zone)
		if err != nil {
			return err
		}
		validation := transportValidation{request: request, zone: zone, renderer: renderer, physicalCommand: plan.physicalCommand}
		if err := validateTransportMutation(db, validation); err != nil {
			return err
		}
		if request.Command == TransportStart {
			decision, err := store.commitNext(db, NextRequest{
				ZoneID:   request.ZoneID,
				Boundary: Boundary{ID: BoundaryID("start:" + request.IdempotencyKey)},
			})
			if err != nil {
				return err
			}
			result.Revision, result.PlayID, result.TrackID = decision.Revision, decision.PlayID, decision.TrackID
			result.CommandID, _, _, err = transportResultAtRevision(db, transportResultQuery{
				zoneID: request.ZoneID, revision: result.Revision, command: TransportStart,
			})
			if err != nil {
				return err
			}
			if err := execBound(db, "UPDATE renderer_outbox SET media_ready=0 WHERE command_id=?", func(stmt *sqliteStmt) error {
				return stmt.bindText(1, result.CommandID)
			}); err != nil {
				return err
			}
		} else {
			result.Revision = zone.revision + 1
			result.PlayID = zone.currentPlay
			result.PhysicalCommand = plan.physicalCommand
			result.CommandID = fmt.Sprintf("%s:%s:%020d", zone.sessionID, request.Command, result.Revision)
			if plan.history != nil {
				result.PlayID = PlayID(fmt.Sprintf("%s:previous:%020d", zone.sessionID, result.Revision))
				result.TrackID = plan.history.trackID
				result.QueueEntryID = plan.history.sourceQueue
				result.SourcePlayID = plan.history.sourcePlay
			}
			insertion := transportInsertion{request: request, zone: zone, result: result, plan: plan}
			if err := insertTransportMutation(db, insertion); err != nil {
				return err
			}
		}
		record := transportMutationRecord{request: request, hash: hash, revision: result.Revision}
		return recordTransportMutation(db, record)
	})
	return result, err
}

func validTransportCommand(command TransportCommand) bool {
	switch command {
	case TransportStart, TransportPause, TransportResume, TransportStop, TransportSeek, TransportSkip, TransportPrevious:
		return true
	default:
		return false
	}
}

func transportTarget(db *sqliteDB, zoneID ZoneID) (zoneRecord, RendererInventory, error) {
	zone, err := loadZone(db, zoneID)
	if err != nil {
		return zoneRecord{}, RendererInventory{}, err
	}
	inventory, found, err := loadZoneInventory(db, zoneID)
	if err != nil {
		return zoneRecord{}, RendererInventory{}, err
	}
	if !found || inventory.RendererID == "" {
		return zoneRecord{}, RendererInventory{}, ErrRendererRequired
	}
	renderer, found, err := loadRenderer(db, inventory.RendererID)
	if err != nil {
		return zoneRecord{}, RendererInventory{}, err
	}
	if !found {
		return zoneRecord{}, RendererInventory{}, ErrRendererRequired
	}
	if (renderer.Kind == RendererKindCustom && renderer.State != RendererConnected) ||
		(renderer.Kind == RendererKindK17 && renderer.State != RendererAvailable) {
		return zoneRecord{}, RendererInventory{}, ErrRendererOffline
	}
	return zone, renderer, nil
}

type transportMutationRecord struct {
	request  TransportMutationRequest
	hash     string
	revision Revision
}

func recordTransportMutation(db *sqliteDB, record transportMutationRecord) error {
	request := record.request
	queueRequest := QueueMutationRequest{
		ZoneID: request.ZoneID, IdempotencyKey: request.IdempotencyKey,
		Command: QueueCommand("transport:" + request.Command),
	}
	return recordQueueMutation(db, queueMutationRecord{request: queueRequest, hash: record.hash, revision: record.revision})
}
