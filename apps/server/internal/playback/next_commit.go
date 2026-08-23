package playback

import (
	"context"

	"github.com/jakestreamer/jstreamer-server/internal/decision"
)

func (store *Store) CommitNext(ctx context.Context, request NextRequest) (Decision, error) {
	if request.ZoneID == "" || request.Boundary.ID == "" {
		return Decision{}, ErrInvalidRequest
	}
	committed := Decision{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		if err := ensureZone(db, request.ZoneID); err != nil {
			return err
		}
		zone, err := loadZone(db, request.ZoneID)
		if err != nil {
			return err
		}
		resumeBlockedBoundary := false
		if zone.sessionID != "" {
			key := boundaryKey{zoneID: request.ZoneID, sessionID: zone.sessionID, boundaryID: request.Boundary.ID}
			found, existing, err := loadLatestBoundaryDecision(db, key, request.Boundary.PreviousPlayID)
			if err != nil {
				return err
			}
			if found {
				if existing.Kind == DecisionBlock && zone.transport == TransportSelecting {
					resumeBlockedBoundary = true
				} else {
					committed = existing
					return nil
				}
			}
		}
		if !resumeBlockedBoundary {
			if err := validateReserveState(zone, request.Boundary); err != nil {
				return err
			}
		}
		if zone.transport == TransportIdle {
			_, _, found, err := queueHead(db, request.ZoneID)
			if err != nil {
				return err
			}
			if !found {
				committed = Decision{
					ID:   string(request.ZoneID) + ":idle:" + string(request.Boundary.ID),
					Kind: DecisionStop, Reason: string(decision.ReasonStopModeOff), Revision: zone.revision,
				}
				return nil
			}
		}
		zone, err = ensureDecisionSession(db, request.ZoneID, zone)
		if err != nil {
			return err
		}
		revision := zone.revision + 1
		if request.Boundary.PreviousPlayID != "" && !resumeBlockedBoundary {
			if err := completePlay(db, playCompletion{
				zoneID: request.ZoneID, playID: request.Boundary.PreviousPlayID, revision: revision,
			}); err != nil {
				return err
			}
		}
		key := boundaryKey{zoneID: request.ZoneID, sessionID: zone.sessionID, boundaryID: request.Boundary.ID}
		snapshot, err := authoritativeSnapshot(db, key, request.Snapshot)
		if err != nil {
			return err
		}
		outcome := decision.DecideNext(snapshot, decision.Boundary{ID: decision.BoundaryID(request.Boundary.ID)})
		sequence := zone.decisionSequence + 1
		attempt, err := countBoundaryAttempts(db, key)
		if err != nil {
			return err
		}
		stored, err := makeDecisionAttempt(
			key, request.Boundary.PreviousPlayID, sequence, attempt+1, revision, outcome,
		)
		if err != nil {
			return err
		}
		if err := reserveOutcome(db, stored, revision); err != nil {
			return err
		}
		if err := insertDecisionAttempt(db, stored, sequence); err != nil {
			return err
		}
		if store.commitHook != nil {
			if err := store.commitHook(commitStageAfterDecision); err != nil {
				return err
			}
		}
		if stored.value.Kind == DecisionPlay {
			if err := insertPlayOutbox(db, stored); err != nil {
				return err
			}
		}
		if !resumeBlockedBoundary {
			if err := commitMatchingPreview(db, stored); err != nil {
				return err
			}
		}
		if err := updateZoneDecision(db, zoneDecisionUpdate{
			zoneID: request.ZoneID, sessionID: zone.sessionID, seed: zone.seed,
			playID: stored.value.PlayID, revision: revision, sequence: sequence,
			transport: transportForDecision(stored.value.Kind),
		}); err != nil {
			return err
		}
		committed = stored.value
		return nil
	})
	return committed, err
}

func reserveOutcome(db *sqliteDB, stored storedDecisionAttempt, revision Revision) error {
	switch stored.value.Kind {
	case DecisionPlay:
		return insertReservedPlay(db, playWrite{
			zoneID: stored.zoneID, sessionID: stored.sessionID, boundaryID: stored.boundaryID,
			playID: stored.value.PlayID, queueEntry: stored.value.QueueEntryID,
			trackID: stored.value.TrackID, revision: revision,
		})
	case DecisionBlock:
		return setQueueState(db, queueTransition{
			entryID: stored.value.QueueEntryID, state: QueueBlocked, revision: revision,
		})
	case DecisionStop:
		return nil
	default:
		return ErrInvalidObservation
	}
}

func transportForDecision(kind DecisionKind) Transport {
	switch kind {
	case DecisionPlay:
		return TransportStarting
	case DecisionBlock:
		return TransportBlocked
	case DecisionStop:
		return TransportIdle
	default:
		return TransportSuspended
	}
}
