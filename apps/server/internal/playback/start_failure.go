package playback

import (
	"context"

	"github.com/jakestreamer/jstreamer-server/internal/catalog"
	"github.com/jakestreamer/jstreamer-server/internal/decision"
)

func (store *Store) HandleStartFailure(ctx context.Context, request StartFailureRequest) (Decision, error) {
	if request.ZoneID == "" || request.BoundaryID == "" || request.PlayID == "" {
		return Decision{}, ErrInvalidRequest
	}
	result := Decision{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		replayed, previous, err := loadFailureReplay(db, request)
		if err != nil {
			return err
		}
		if replayed {
			result = previous
			return nil
		}
		zone, err := loadZone(db, request.ZoneID)
		if err != nil {
			return err
		}
		failed, err := loadAttemptByPlay(db, request.ZoneID, request.PlayID)
		if err != nil {
			return err
		}
		if failed.boundaryID != request.BoundaryID || zone.currentPlay != request.PlayID ||
			failed.value.Kind != DecisionPlay {
			return ErrStartFailure
		}
		key := boundaryKey{
			zoneID: request.ZoneID, sessionID: failed.sessionID, boundaryID: failed.boundaryID,
		}
		snapshot, err := authoritativeSnapshot(db, key, request.Snapshot)
		if err != nil {
			return err
		}
		failureIndex := len(snapshot.FailedGenerated) + 1
		revision := zone.revision + 1
		if err := terminalizeFailedStart(db, failed, revision); err != nil {
			return err
		}
		outcome, err := outcomeAfterStartFailure(failed, snapshot)
		if err != nil {
			return err
		}
		sequence := zone.decisionSequence + 1
		attempt, err := countBoundaryAttempts(db, key)
		if err != nil {
			return err
		}
		stored, err := makeDecisionAttempt(
			key, failed.previousPlay, sequence, attempt+1, revision, outcome,
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
		if stored.value.Kind == DecisionPlay {
			if err := insertPlayOutbox(db, stored); err != nil {
				return err
			}
		}
		if err := insertStartFailure(db, failed, stored.value, failureIndex); err != nil {
			return err
		}
		if err := updateZoneDecision(db, zoneDecisionUpdate{
			zoneID: request.ZoneID, sessionID: zone.sessionID, seed: zone.seed,
			playID: stored.value.PlayID, revision: revision, sequence: sequence,
			transport: transportForDecision(stored.value.Kind),
		}); err != nil {
			return err
		}
		result = stored.value
		return nil
	})
	return result, err
}

func outcomeAfterStartFailure(
	failed storedDecisionAttempt,
	snapshot decision.Snapshot,
) (decision.Outcome, error) {
	switch failed.value.Source {
	case string(decision.SourceExplicit):
		return decision.Block{
			BoundaryID:   decision.BoundaryID(failed.boundaryID),
			TrackID:      catalog.TrackID(failed.value.TrackID),
			QueueEntryID: decision.QueueEntryID(failed.value.QueueEntryID),
			Reason:       decision.ReasonBlockExplicit,
		}, nil
	case string(decision.SourceAlbum), string(decision.SourceSimilar):
		snapshot.FailedGenerated = append(snapshot.FailedGenerated, catalog.TrackID(failed.value.TrackID))
		return decision.DecideNext(snapshot, decision.Boundary{ID: decision.BoundaryID(failed.boundaryID)}), nil
	default:
		return nil, ErrStartFailure
	}
}
