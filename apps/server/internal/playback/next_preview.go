package playback

import (
	"context"

	"github.com/jastreamer/jastreamer-server/internal/decision"
)

func (store *Store) PreviewNext(ctx context.Context, request NextRequest) (Decision, error) {
	if request.ZoneID == "" || request.Boundary.ID == "" {
		return Decision{}, ErrInvalidRequest
	}
	preview := Decision{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		zone, err := loadZone(db, request.ZoneID)
		if err != nil {
			return err
		}
		if zone.transport != TransportPlaying || zone.sessionID == "" ||
			request.Boundary.PreviousPlayID != zone.currentPlay {
			return ErrInvalidTransition
		}
		key := boundaryKey{zoneID: request.ZoneID, sessionID: zone.sessionID, boundaryID: request.Boundary.ID}
		found, committed, err := loadLatestBoundaryDecision(db, key, request.Boundary.PreviousPlayID)
		if err != nil {
			return err
		}
		if found {
			preview = committed
			return nil
		}
		found, storedPreview, err := loadAutomaticPreview(db, request.ZoneID, request.Boundary.ID)
		if err != nil {
			return err
		}
		if found {
			if storedPreview.previousPlay != request.Boundary.PreviousPlayID {
				return ErrAutomaticConflict
			}
			if storedPreview.state == "cancelled" {
				return ErrAutomaticPreempted
			}
			preview = Decision{
				Kind: DecisionPlay, Reason: storedPreview.reason, Source: storedPreview.source,
				TrackID: storedPreview.trackID, PlayID: storedPreview.playID,
				Revision: storedPreview.created,
			}
			return nil
		}
		snapshot, err := authoritativeSnapshot(db, key, request.Snapshot)
		if err != nil {
			return err
		}
		outcome := decision.DecideNext(snapshot, decision.Boundary{ID: decision.BoundaryID(request.Boundary.ID)})
		selected, generated := outcome.(decision.Play)
		if !generated || selected.Source == decision.SourceExplicit {
			preview = previewDecision(outcome, zone.revision)
			return nil
		}
		revision := zone.revision + 1
		preview = previewDecision(outcome, revision)
		if err := insertAutomaticPreview(db, request, zone, preview); err != nil {
			return err
		}
		return updateZoneRevision(db, request.ZoneID, revision)
	})
	return preview, err
}

func previewDecision(outcome decision.Outcome, revision Revision) Decision {
	switch selected := outcome.(type) {
	case decision.Play:
		return Decision{
			Kind: DecisionPlay, Reason: string(selected.Reason), Source: string(selected.Source),
			QueueEntryID: QueueEntryID(selected.QueueEntryID), TrackID: TrackID(selected.TrackID), Revision: revision,
		}
	case decision.Stop:
		return Decision{Kind: DecisionStop, Reason: string(selected.Reason), Revision: revision}
	case decision.Block:
		return Decision{
			Kind: DecisionBlock, Reason: string(selected.Reason), Source: string(decision.SourceExplicit),
			QueueEntryID: QueueEntryID(selected.QueueEntryID), TrackID: TrackID(selected.TrackID), Revision: revision,
		}
	default:
		return Decision{Kind: DecisionStop, Reason: string(decision.ReasonStopModeOff), Revision: revision}
	}
}

func insertAutomaticPreview(db *sqliteDB, request NextRequest, zone zoneRecord, preview Decision) error {
	return execBound(db, `
		INSERT INTO playback_automatic_previews(
			zone_id,boundary_id,previous_play_id,session_id,track_id,state,
			created_revision,source,reason
		) VALUES (?,?,?,?,?,'preview',?,?,?)`, func(stmt *sqliteStmt) error {
		values := []string{
			string(request.ZoneID), string(request.Boundary.ID), string(request.Boundary.PreviousPlayID),
			string(zone.sessionID), string(preview.TrackID), preview.Source, preview.Reason,
		}
		indexes := []int{1, 2, 3, 4, 5, 7, 8}
		for index, value := range values {
			if err := stmt.bindText(indexes[index], value); err != nil {
				return err
			}
		}
		return stmt.bindInt64(6, int64(preview.Revision))
	})
}
