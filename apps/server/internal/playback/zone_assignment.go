package playback

import (
	"context"
	"errors"
	"fmt"
)

var ErrZoneActive = errors.New("playback: active zone cannot be reassigned")

type AssignmentRequest struct {
	ZoneID           ZoneID
	RendererID       RendererID
	ExpectedRevision Revision
}

type AssignmentResult struct {
	ZoneID     ZoneID
	RendererID RendererID
	Revision   Revision
}

func (store *Store) AssignRenderer(ctx context.Context, request AssignmentRequest) (AssignmentResult, error) {
	if request.ZoneID == "" {
		return AssignmentResult{}, ErrInvalidZone
	}
	result := AssignmentResult{}
	err := store.transaction(ctx, func(db *sqliteDB) error {
		zone, found, err := loadZoneInventory(db, request.ZoneID)
		if err != nil {
			return err
		}
		if !found {
			return ErrZoneNotFound
		}
		if zone.Revision != request.ExpectedRevision {
			return ErrRevisionConflict
		}
		result = AssignmentResult{ZoneID: request.ZoneID, RendererID: zone.RendererID, Revision: zone.Revision}
		if zone.RendererID == request.RendererID {
			return nil
		}
		if zone.Transport != TransportIdle {
			return ErrZoneActive
		}
		if request.RendererID != "" {
			if err := ensureAssignableRenderer(db, request); err != nil {
				return err
			}
		}
		revision := zone.Revision + 1
		if zone.RendererID != "" {
			if err := closeAssignment(db, request.ZoneID, revision); err != nil {
				return err
			}
		}
		if request.RendererID != "" {
			write := assignmentWrite{
				id:      AssignmentID(fmt.Sprintf("%s:assignment:%020d", request.ZoneID, revision)),
				request: request, revision: revision,
			}
			if err := insertAssignment(db, write); err != nil {
				return err
			}
		}
		if err := updateServerZoneRevision(db, request.ZoneID, revision); err != nil {
			return err
		}
		result = AssignmentResult{ZoneID: request.ZoneID, RendererID: request.RendererID, Revision: revision}
		return nil
	})
	return result, err
}

func ensureAssignableRenderer(db *sqliteDB, request AssignmentRequest) error {
	stmt, err := db.prepare("SELECT state FROM renderer_registry WHERE renderer_id=?")
	if err != nil {
		return err
	}
	if err := stmt.bindText(1, string(request.RendererID)); err != nil {
		stmt.close()
		return err
	}
	row, err := stmt.step()
	if err != nil || !row {
		stmt.close()
		if err != nil {
			return err
		}
		return ErrRendererNotFound
	}
	state := RendererState(stmt.text(0))
	stmt.close()
	if state == RendererRevoked {
		return ErrRendererNotFound
	}
	stmt, err = db.prepare("SELECT zone_id FROM renderer_assignments WHERE renderer_id=? AND unassigned_revision IS NULL")
	if err != nil {
		return err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(request.RendererID)); err != nil {
		return err
	}
	row, err = stmt.step()
	if err != nil {
		return err
	}
	if row && ZoneID(stmt.text(0)) != request.ZoneID {
		return ErrRendererAssigned
	}
	return nil
}
