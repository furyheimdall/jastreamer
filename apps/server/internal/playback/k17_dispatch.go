package playback

import (
	"context"
	"time"
)

type K17DispatchIdentity struct {
	ZoneID    ZoneID
	CommandID string
	PlayID    PlayID
}

type K17DispatchClaim string

const (
	K17DispatchClaimed   K17DispatchClaim = "claimed"
	K17DispatchInFlight  K17DispatchClaim = "in_flight"
	K17DispatchCompleted K17DispatchClaim = "completed"
)

type K17DispatchCompletion string

const (
	K17DispatchSucceeded     K17DispatchCompletion = "succeeded"
	K17DispatchAdapterFailed K17DispatchCompletion = "adapter_failed"
)

type k17DispatchRecord struct {
	state          string
	receiptState   string
	lastErrorCode  string
	failedRevision Revision
}

type k17DispatchTerminal struct {
	commandID  string
	terminalAt string
	revision   Revision
	errorCode  string
}

func (store *Store) ClaimK17TransportDispatch(ctx context.Context, identity K17DispatchIdentity) (K17DispatchClaim, error) {
	if identity.ZoneID == "" || identity.CommandID == "" || identity.PlayID == "" {
		return "", ErrCommandDeliveryConflict
	}
	claim := K17DispatchClaim("")
	err := store.transaction(ctx, func(db *sqliteDB) error {
		record, err := loadK17Dispatch(db, identity)
		if err != nil {
			return err
		}
		switch record.receiptState {
		case "terminal":
			claim = K17DispatchCompleted
			return nil
		case "pending":
			switch record.state {
			case "pending":
				claim = K17DispatchClaimed
				now := store.clock.Now().UTC().Format(time.RFC3339Nano)
				return execBound(db, `UPDATE renderer_outbox SET state='sent',attempts=attempts+1,
					last_attempt_at=? WHERE command_id=? AND state='pending' AND receipt_state='pending'`, func(stmt *sqliteStmt) error {
					if err := stmt.bindText(1, now); err != nil {
						return err
					}
					return stmt.bindText(2, identity.CommandID)
				})
			case "sent":
				claim = K17DispatchInFlight
				return nil
			default:
				return ErrCommandDeliveryConflict
			}
		default:
			return ErrCommandDeliveryConflict
		}
	})
	return claim, err
}

func (store *Store) CompleteK17TransportDispatch(
	ctx context.Context,
	identity K17DispatchIdentity,
	completion K17DispatchCompletion,
) (Revision, error) {
	if completion != K17DispatchSucceeded && completion != K17DispatchAdapterFailed {
		return 0, ErrCommandDeliveryConflict
	}
	var revision Revision
	err := store.transaction(ctx, func(db *sqliteDB) error {
		record, err := loadK17Dispatch(db, identity)
		if err != nil {
			return err
		}
		if record.receiptState == "terminal" {
			if (completion == K17DispatchSucceeded && record.lastErrorCode == "") ||
				(completion == K17DispatchAdapterFailed && record.lastErrorCode == "ADAPTER_FAILURE") {
				revision = record.failedRevision
				return nil
			}
			return ErrCommandDeliveryConflict
		}
		if record.receiptState != "pending" || record.state != "sent" {
			return ErrCommandDeliveryConflict
		}
		now := store.clock.Now().UTC().Format(time.RFC3339Nano)
		if completion == K17DispatchSucceeded {
			return terminalizeK17Dispatch(db, k17DispatchTerminal{
				commandID: identity.CommandID, terminalAt: now,
			})
		}
		zone, err := loadZone(db, identity.ZoneID)
		if err != nil {
			return err
		}
		if zone.transport != TransportStarting || zone.currentPlay != identity.PlayID {
			return ErrCommandDeliveryConflict
		}
		revision = zone.revision + 1
		if err := terminalizeK17Dispatch(db, k17DispatchTerminal{
			commandID: identity.CommandID, terminalAt: now, revision: revision, errorCode: "ADAPTER_FAILURE",
		}); err != nil {
			return err
		}
		return updateTransportIntent(db, transportIntent{
			zoneID: identity.ZoneID, revision: revision, transport: TransportSuspended,
		})
	})
	return revision, err
}

func loadK17Dispatch(db *sqliteDB, identity K17DispatchIdentity) (k17DispatchRecord, error) {
	stmt, err := db.prepare(`SELECT o.zone_id,o.play_id,o.state,o.receipt_state,o.last_error_code,
		o.failed_revision,COALESCE(NULLIF(o.transport_kind,''),o.command_type),r.kind,z.current_play_id
		FROM renderer_outbox o
		JOIN playback_zones z ON z.zone_id=o.zone_id
		JOIN renderer_assignments a ON a.zone_id=o.zone_id AND a.unassigned_revision IS NULL
		JOIN renderer_registry r ON r.renderer_id=a.renderer_id
		WHERE o.command_id=?`)
	if err != nil {
		return k17DispatchRecord{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, identity.CommandID); err != nil {
		return k17DispatchRecord{}, err
	}
	row, err := stmt.step()
	if err != nil {
		return k17DispatchRecord{}, err
	}
	if !row || ZoneID(stmt.text(0)) != identity.ZoneID || PlayID(stmt.text(1)) != identity.PlayID ||
		stmt.text(6) != "play" || RendererKind(stmt.text(7)) != RendererKindK17 || PlayID(stmt.text(8)) != identity.PlayID {
		return k17DispatchRecord{}, ErrCommandDeliveryConflict
	}
	record := k17DispatchRecord{
		state: stmt.text(2), receiptState: stmt.text(3), lastErrorCode: stmt.text(4),
	}
	if !stmt.isNull(5) {
		record.failedRevision = Revision(stmt.int64(5))
	}
	return record, nil
}

func terminalizeK17Dispatch(db *sqliteDB, terminal k17DispatchTerminal) error {
	return execBound(db, `UPDATE renderer_outbox SET state='confirmed',receipt_state='terminal',
		failed_revision=?,last_error_code=?,terminal_at=? WHERE command_id=?`, func(stmt *sqliteStmt) error {
		if terminal.revision == 0 {
			if err := stmt.bind(1, nil); err != nil {
				return err
			}
		} else if err := stmt.bindInt64(1, int64(terminal.revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, terminal.errorCode); err != nil {
			return err
		}
		if err := stmt.bindText(3, terminal.terminalAt); err != nil {
			return err
		}
		return stmt.bindText(4, terminal.commandID)
	})
}
