package playback

import (
	"context"
	"time"
)

type pendingK17Dispatch struct {
	commandID      string
	zoneID         ZoneID
	playID         PlayID
	rendererID     RendererID
	boundaryID     BoundaryID
	previousPlayID PlayID
}

func (store *Store) ReconcileInterruptedK17Dispatches(ctx context.Context) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT o.command_id,o.zone_id,o.play_id
			FROM renderer_outbox o
			JOIN renderer_assignments a ON a.zone_id=o.zone_id AND a.unassigned_revision IS NULL
			JOIN renderer_registry r ON r.renderer_id=a.renderer_id
			WHERE o.state='sent' AND o.receipt_state='pending' AND o.failed_revision IS NULL
			AND COALESCE(NULLIF(o.transport_kind,''),o.command_type)='play' AND r.kind='k17'
			ORDER BY o.created_revision,o.command_id`)
		if err != nil {
			return err
		}
		interrupted := []K17DispatchIdentity{}
		for {
			row, err := stmt.step()
			if err != nil {
				stmt.close()
				return err
			}
			if !row {
				break
			}
			interrupted = append(interrupted, K17DispatchIdentity{
				CommandID: stmt.text(0), ZoneID: ZoneID(stmt.text(1)), PlayID: PlayID(stmt.text(2)),
			})
		}
		stmt.close()
		for _, identity := range interrupted {
			if _, err := loadK17Dispatch(db, identity); err != nil {
				return err
			}
			zone, err := loadZone(db, identity.ZoneID)
			if err != nil {
				return err
			}
			revision := zone.revision
			requiresSuspension := false
			switch zone.transport {
			case TransportStarting:
				revision++
				requiresSuspension = true
			case TransportSuspended:
			default:
				return ErrCommandDeliveryConflict
			}
			now := store.clock.Now().UTC().Format(time.RFC3339Nano)
			if err := terminalizeK17Dispatch(db, k17DispatchTerminal{
				commandID: identity.CommandID, terminalAt: now, revision: revision, errorCode: "ADAPTER_FAILURE",
			}); err != nil {
				return err
			}
			if requiresSuspension {
				if err := updateTransportIntent(db, transportIntent{
					zoneID: identity.ZoneID, revision: revision, transport: TransportSuspended,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (store *Store) PendingK17LifecycleDispatches(ctx context.Context) ([]K17LifecycleResult, error) {
	results := []K17LifecycleResult{}
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT o.command_id,o.zone_id,o.play_id,a.renderer_id,d.boundary_id,d.previous_play_id
			FROM renderer_outbox o
			JOIN playback_decision_attempts d ON d.decision_id=o.command_id
			JOIN renderer_assignments a ON a.zone_id=o.zone_id AND a.unassigned_revision IS NULL
			JOIN renderer_registry r ON r.renderer_id=a.renderer_id
			WHERE o.state='pending' AND o.receipt_state='pending' AND o.failed_revision IS NULL
			AND COALESCE(NULLIF(o.transport_kind,''),o.command_type)='play' AND r.kind='k17'
			AND d.boundary_id LIKE 'k17-ended:%' ORDER BY o.created_revision,o.command_id`)
		if err != nil {
			return err
		}
		pending := []pendingK17Dispatch{}
		for {
			row, err := stmt.step()
			if err != nil {
				stmt.close()
				return err
			}
			if !row {
				break
			}
			pending = append(pending, pendingK17Dispatch{
				commandID: stmt.text(0), zoneID: ZoneID(stmt.text(1)), playID: PlayID(stmt.text(2)),
				rendererID: RendererID(stmt.text(3)), boundaryID: BoundaryID(stmt.text(4)), previousPlayID: PlayID(stmt.text(5)),
			})
		}
		stmt.close()
		for _, identity := range pending {
			found, decision, err := loadDecisionByID(db, identity.commandID)
			if err != nil {
				return err
			}
			if !found || decision.PlayID != identity.playID {
				return ErrCorruptDatabase
			}
			results = append(results, K17LifecycleResult{
				Action: K17LifecycleNaturalEnd, RendererID: identity.rendererID, ZoneID: identity.zoneID,
				PreviousPlayID: identity.previousPlayID, BoundaryID: identity.boundaryID, Decision: decision,
			})
		}
		return nil
	})
	return results, err
}
