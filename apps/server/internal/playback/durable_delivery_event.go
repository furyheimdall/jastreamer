package playback

import (
	"context"
	"time"
)

func (store *Store) HandleRendererPlaybackEvent(
	ctx context.Context,
	event RendererPlaybackEvent,
) (Decision, error) {
	if event.RendererID == "" || event.Epoch == "" || event.EventID == "" || event.PlayID == "" ||
		event.Kind == "" || event.ObservedAt.IsZero() || (event.PositionMS != nil && *event.PositionMS < 0) {
		return Decision{}, ErrInvalidRequest
	}
	result := Decision{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		if err := assertRendererEpoch(db, event.RendererID, event.Epoch); err != nil {
			return err
		}
		found, decisionID, err := matchingRendererEvent(db, event)
		if err != nil {
			return err
		}
		if found {
			if decisionID == "" {
				return nil
			}
			loaded, decision, err := loadDecisionByID(db, decisionID)
			if err != nil {
				return err
			}
			if !loaded {
				return ErrCorruptDatabase
			}
			result = decision
			return nil
		}
		zoneID, assigned, err := assignedZone(db, event.RendererID)
		if err != nil {
			return err
		}
		if !assigned {
			return ErrInvalidObservation
		}
		zone, err := loadZone(db, zoneID)
		if err != nil {
			return err
		}
		if zone.currentPlay != event.PlayID {
			return ErrInvalidObservation
		}
		if err := insertRendererEvent(db, event); err != nil {
			return err
		}
		if err := updateRendererObservation(db, event); err != nil {
			return err
		}
		switch event.Kind {
		case PlaybackEventEnded:
			terminal, err := playCommandIsTerminal(db, event.RendererID, event.PlayID)
			if err != nil {
				return err
			}
			if !terminal {
				return ErrInvalidObservation
			}
			result, err = store.commitNext(db, NextRequest{
				ZoneID:   zoneID,
				Boundary: Boundary{ID: BoundaryID(event.EventID), PreviousPlayID: event.PlayID},
			})
			if err != nil {
				return err
			}
			return execBound(db, `UPDATE renderer_playback_events SET handled_at=?,decision_id=?
				WHERE renderer_id=? AND event_id=?`, func(stmt *sqliteStmt) error {
				values := []string{
					event.ObservedAt.UTC().Format(time.RFC3339Nano), result.ID,
					string(event.RendererID), event.EventID,
				}
				for index, value := range values {
					if err := stmt.bindText(index+1, value); err != nil {
						return err
					}
				}
				return nil
			})
		case PlaybackEventPlaying:
			received, err := playCommandWasReceived(db, event.RendererID, event.PlayID)
			if err != nil {
				return err
			}
			if !received {
				return ErrInvalidObservation
			}
			return confirmStart(db, zoneID, event.PlayID)
		case PlaybackEventPaused:
			return nil
		case PlaybackEventFailed:
			return suspendAssignedRenderer(db, event.RendererID)
		default:
			return suspendAssignedRenderer(db, event.RendererID)
		}
	})
	return result, err
}
