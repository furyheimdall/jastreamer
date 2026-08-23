package playback

import (
	"context"
	"fmt"
)

type automaticPreview struct {
	zoneID       ZoneID
	boundaryID   BoundaryID
	previousPlay PlayID
	sessionID    SessionID
	trackID      TrackID
	state        string
	created      Revision
	terminal     Revision
	playID       PlayID
}

func (store *Store) PreviewAutomatic(ctx context.Context, request AutomaticPreviewRequest) (Revision, error) {
	if request.ZoneID == "" || request.Boundary.ID == "" || request.TrackID == "" {
		return 0, ErrInvalidRequest
	}
	var revision Revision
	err := store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, request.ZoneID)
		if err != nil {
			return err
		}
		found, preview, err := loadAutomaticPreview(db, request.ZoneID, request.Boundary.ID)
		if err != nil {
			return err
		}
		if found {
			if preview.trackID != request.TrackID || preview.previousPlay != request.Boundary.PreviousPlayID {
				return ErrAutomaticConflict
			}
			switch preview.state {
			case "preview":
				revision = preview.created
				return nil
			case "cancelled":
				return ErrAutomaticPreempted
			case "committed":
				revision = preview.terminal
				return nil
			}
		}
		if zone.revision != request.ExpectedRevision {
			return ErrRevisionConflict
		}
		if zone.transport != TransportPlaying || zone.sessionID == "" ||
			request.Boundary.PreviousPlayID == "" || request.Boundary.PreviousPlayID != zone.currentPlay {
			return ErrInvalidTransition
		}
		if _, _, found, err := queueHead(db, request.ZoneID); err != nil {
			return err
		} else if found {
			return ErrAutomaticPreempted
		}
		revision = zone.revision + 1
		if err := execBound(db, `
			INSERT INTO playback_automatic_previews(
				zone_id,boundary_id,previous_play_id,session_id,track_id,state,created_revision
			) VALUES (?,?,?,?,?,'preview',?)`, func(stmt *sqliteStmt) error {
			values := []string{
				string(request.ZoneID), string(request.Boundary.ID),
				string(request.Boundary.PreviousPlayID), string(zone.sessionID), string(request.TrackID),
			}
			for index, value := range values {
				if err := stmt.bindText(index+1, value); err != nil {
					return err
				}
			}
			return stmt.bindInt64(6, int64(revision))
		}); err != nil {
			return err
		}
		return updateZoneRevision(db, request.ZoneID, revision)
	})
	return revision, err
}

func (store *Store) CommitAutomatic(
	ctx context.Context,
	zoneID ZoneID,
	boundaryID BoundaryID,
	expected Revision,
) (Decision, error) {
	var decision Decision
	err := store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		found, preview, err := loadAutomaticPreview(db, zoneID, boundaryID)
		if err != nil {
			return err
		}
		if !found {
			return ErrInvalidRequest
		}
		switch preview.state {
		case "cancelled":
			return ErrAutomaticPreempted
		case "committed":
			found, existing, err := loadDecision(db, decisionKey{
				zoneID: zoneID, sessionID: preview.sessionID,
				boundaryID: boundaryID, previousPlay: preview.previousPlay,
			})
			if err != nil {
				return err
			}
			if !found {
				return ErrInvalidObservation
			}
			decision = existing
			return nil
		}
		if zone.revision != expected {
			return ErrRevisionConflict
		}
		if zone.transport != TransportPlaying || zone.sessionID != preview.sessionID ||
			zone.currentPlay != preview.previousPlay {
			return ErrInvalidTransition
		}
		if _, _, found, err := queueHead(db, zoneID); err != nil {
			return err
		} else if found {
			return ErrAutomaticPreempted
		}
		revision := zone.revision + 1
		if err := completePlay(db, playCompletion{
			zoneID: zoneID, playID: preview.previousPlay, revision: revision,
		}); err != nil {
			return err
		}
		sequence := zone.decisionSequence + 1
		decision = Decision{
			ID:   fmt.Sprintf("%s:d:%020d", zone.sessionID, sequence),
			Kind: DecisionPlay, Reason: ReasonPlayAutomatic,
			PlayID:  PlayID(fmt.Sprintf("%s:p:%020d", zone.sessionID, sequence)),
			TrackID: preview.trackID, Revision: revision,
		}
		if err := reserveAutomaticPlay(db, preview, decision); err != nil {
			return err
		}
		if err := insertDecision(db, decisionRecord{
			zoneID: zoneID, sessionID: zone.sessionID, boundaryID: boundaryID,
			previousPlayID: preview.previousPlay, sequence: sequence, decision: decision,
		}); err != nil {
			return err
		}
		if err := updateZoneDecision(db, zoneDecisionUpdate{
			zoneID: zoneID, sessionID: zone.sessionID, seed: zone.seed,
			playID: decision.PlayID, revision: revision, sequence: sequence,
			transport: TransportStarting,
		}); err != nil {
			return err
		}
		return execBound(db, `
			UPDATE playback_automatic_previews
			SET state='committed',terminal_revision=?,play_id=?
			WHERE zone_id=? AND boundary_id=? AND state='preview'`, func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(revision)); err != nil {
				return err
			}
			values := []string{string(decision.PlayID), string(zoneID), string(boundaryID)}
			for index, value := range values {
				if err := stmt.bindText(index+2, value); err != nil {
					return err
				}
			}
			return nil
		})
	})
	return decision, err
}
