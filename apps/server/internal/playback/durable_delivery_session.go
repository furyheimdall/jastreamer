package playback

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

func (store *Store) OpenRendererSession(ctx context.Context, request RendererSessionRequest) (RendererSessionState, error) {
	if request.RendererID == "" || request.LastServerSequence < 0 || request.ConnectedAt.IsZero() {
		return RendererSessionState{}, ErrInvalidRequest
	}
	var result RendererSessionState
	err := store.transaction(ctx, func(db *sqliteDB) error {
		renderer, found, err := loadRenderer(db, request.RendererID)
		if err != nil {
			return err
		}
		if !found || renderer.Kind != RendererKindCustom || renderer.State == RendererRevoked {
			return ErrRendererNotFound
		}
		highest, err := rendererHighestSequence(db, request.RendererID)
		if err != nil {
			return err
		}
		if request.LastServerSequence > highest {
			return ErrCommandSequenceGap
		}
		generation, persistedNext, existed, err := rendererSessionCounters(db, request.RendererID)
		if err != nil {
			return err
		}
		if existed {
			if err := suspendAssignedRenderer(db, request.RendererID); err != nil {
				return err
			}
		}
		generation++
		next := max(highest+1, persistedNext)
		digest := sha256.Sum256([]byte(request.RendererID))
		epoch := SessionEpoch(fmt.Sprintf("%x-%020d", digest[:8], generation))
		connectedAt := request.ConnectedAt.UTC().Format(time.RFC3339Nano)
		if err := upsertRendererSession(db, rendererSessionWrite{
			rendererID: request.RendererID, epoch: epoch, generation: generation,
			nextSequence: next, cursor: request.LastServerSequence, connectedAt: connectedAt,
		}); err != nil {
			return err
		}
		if err := setRendererConnectionState(db, rendererConnectionUpdate{
			rendererID: request.RendererID, state: RendererConnected, observedAt: connectedAt,
		}); err != nil {
			return err
		}
		result = RendererSessionState{
			RendererID: request.RendererID, Epoch: epoch, Generation: generation,
			NextSequence: next, LastServerSequence: request.LastServerSequence,
			ConnectedAt: request.ConnectedAt.UTC(),
		}
		return nil
	})
	return result, err
}

func (store *Store) CloseRendererSession(ctx context.Context, closing RendererSessionClose) error {
	if closing.RendererID == "" || closing.Epoch == "" || closing.DisconnectedAt.IsZero() {
		return ErrInvalidRequest
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		current, err := currentRendererEpoch(db, closing.RendererID)
		if err != nil {
			return err
		}
		if current != closing.Epoch {
			return nil
		}
		disconnectedAt := closing.DisconnectedAt.UTC().Format(time.RFC3339Nano)
		if err := execBound(db, `UPDATE renderer_session_state
			SET connection_state='disconnected',disconnected_at=?
			WHERE renderer_id=? AND connection_state<>'revoked'`, func(stmt *sqliteStmt) error {
			if err := stmt.bindText(1, disconnectedAt); err != nil {
				return err
			}
			return stmt.bindText(2, string(closing.RendererID))
		}); err != nil {
			return err
		}
		if err := setRendererConnectionState(db, rendererConnectionUpdate{
			rendererID: closing.RendererID, state: RendererAvailable, observedAt: disconnectedAt,
		}); err != nil {
			return err
		}
		return suspendAssignedRenderer(db, closing.RendererID)
	})
}

func (store *Store) RendererSessionTruth(ctx context.Context, rendererID RendererID) (RendererSessionTruth, error) {
	var truth RendererSessionTruth
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT s.connection_state,s.current_epoch,s.observed_play_id,
			s.observed_state,s.observed_at,COALESCE(a.zone_id,''),COALESCE(p.session_id,''),
			COALESCE(p.current_play_id,''),COALESCE(p.transport,'idle')
			FROM renderer_session_state s
			LEFT JOIN renderer_assignments a ON a.renderer_id=s.renderer_id AND a.unassigned_revision IS NULL
			LEFT JOIN playback_zones p ON p.zone_id=a.zone_id WHERE s.renderer_id=?`)
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(rendererID)); err != nil {
			return err
		}
		row, err := stmt.step()
		if err != nil {
			return err
		}
		if !row {
			return ErrRendererNotFound
		}
		truth = RendererSessionTruth{
			RendererID: rendererID, ConnectionState: stmt.text(0), Epoch: SessionEpoch(stmt.text(1)),
			ObservedPlayID: PlayID(stmt.text(2)), ObservedState: stmt.text(3), ZoneID: ZoneID(stmt.text(5)),
			IntentSessionID: SessionID(stmt.text(6)), IntentPlayID: PlayID(stmt.text(7)),
			IntentTransport: Transport(stmt.text(8)),
		}
		truth.ObservedAt, err = parseStoredTime(stmt.text(4))
		return err
	})
	return truth, err
}

func assertRendererEpoch(db *sqliteDB, rendererID RendererID, epoch SessionEpoch) error {
	stmt, err := db.prepare(`SELECT s.current_epoch,s.connection_state,r.state
		FROM renderer_session_state s JOIN renderer_registry r ON r.renderer_id=s.renderer_id
		WHERE s.renderer_id=?`)
	if err != nil {
		return err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return err
	}
	row, err := stmt.step()
	if err != nil {
		return err
	}
	if !row {
		return ErrRendererNotFound
	}
	if SessionEpoch(stmt.text(0)) != epoch || stmt.text(1) != "connected" ||
		RendererState(stmt.text(2)) == RendererRevoked {
		return ErrStaleRendererEpoch
	}
	return nil
}

func currentRendererEpoch(db *sqliteDB, rendererID RendererID) (SessionEpoch, error) {
	stmt, err := db.prepare("SELECT current_epoch FROM renderer_session_state WHERE renderer_id=?")
	if err != nil {
		return "", err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return "", err
	}
	row, err := stmt.step()
	if err != nil {
		return "", err
	}
	if !row {
		return "", ErrRendererNotFound
	}
	return SessionEpoch(stmt.text(0)), nil
}

func suspendAssignedRenderer(db *sqliteDB, rendererID RendererID) error {
	zoneID, assigned, err := assignedZone(db, rendererID)
	if err != nil || !assigned {
		return err
	}
	zone, err := loadZone(db, zoneID)
	if err != nil {
		return err
	}
	if zone.transport == TransportIdle || zone.transport == TransportSuspended {
		return nil
	}
	return execBound(db, "UPDATE playback_zones SET revision=?,transport='suspended' WHERE zone_id=?", func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(zone.revision+1)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zoneID))
	})
}
