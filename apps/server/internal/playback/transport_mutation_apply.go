package playback

import "encoding/json"

type transportValidation struct {
	request         TransportMutationRequest
	zone            zoneRecord
	renderer        RendererInventory
	physicalCommand TransportCommand
}

func validateTransportMutation(db *sqliteDB, validation transportValidation) error {
	request, zone, renderer := validation.request, validation.zone, validation.renderer
	required := "command:" + string(request.Command)
	switch request.Command {
	case TransportStart:
		if zone.transport != TransportIdle {
			return ErrInvalidTransition
		}
		_, available, found, err := queueHead(db, request.ZoneID)
		if err != nil {
			return err
		}
		if !found {
			return ErrQueueEmpty
		}
		if !available {
			return ErrQueueBlocked
		}
		required = "command:play"
	case TransportPause:
		if zone.transport != TransportPlaying {
			return ErrInvalidTransition
		}
	case TransportResume:
		if zone.transport != TransportPaused {
			return ErrInvalidTransition
		}
	case TransportSeek:
		if zone.transport != TransportPlaying && zone.transport != TransportPaused {
			return ErrInvalidTransition
		}
		required = "command:seek"
	case TransportPrevious:
		required = "command:" + string(validation.physicalCommand)
	case TransportStop:
		if zone.transport == TransportIdle {
			return ErrInvalidTransition
		}
	case TransportSkip:
		if zone.transport != TransportStarting && zone.transport != TransportPlaying && zone.transport != TransportPaused {
			return ErrInvalidTransition
		}
		required = "command:stop"
	default:
		return ErrInvalidRequest
	}
	if renderer.Kind == RendererKindCustom && !rendererHasCapability(renderer, required) {
		return ErrUnsupportedCapability
	}
	return nil
}

func rendererHasCapability(renderer RendererInventory, capability string) bool {
	for _, value := range renderer.Capabilities {
		if value == capability {
			return true
		}
	}
	return false
}

type transportInsertion struct {
	request TransportMutationRequest
	zone    zoneRecord
	result  TransportMutationResult
	plan    transportMutationPlan
}

func insertTransportMutation(db *sqliteDB, insertion transportInsertion) error {
	request, zone, result, plan := insertion.request, insertion.zone, insertion.result, insertion.plan
	kind := plan.physicalCommand
	backing := string(kind)
	position := (*int64)(nil)
	switch kind {
	case TransportSeek:
		backing = "pause"
		value := request.PositionMS
		if request.Command == TransportPrevious {
			value = 0
		}
		position = &value
	case TransportSkip:
		backing = "stop"
	}
	if plan.history != nil {
		if err := retireCurrentForPrevious(db, request.ZoneID, zone.currentPlay, result.Revision); err != nil {
			return err
		}
		if err := consumePreviousHistory(db, historyConsumption{
			entry: *plan.history, revision: result.Revision, replayPlayID: result.PlayID,
		}); err != nil {
			return err
		}
		if err := insertReservedPlay(db, playWrite{
			zoneID: request.ZoneID, sessionID: zone.sessionID, boundaryID: BoundaryID(result.CommandID),
			playID: result.PlayID, trackID: result.TrackID, revision: result.Revision,
		}); err != nil {
			return err
		}
	}
	payload, err := json.Marshal(storedRendererCommandPayload{
		ZoneID: request.ZoneID, SessionID: zone.sessionID, PlayID: result.PlayID,
		TrackID: result.TrackID, Kind: string(kind), PositionMS: position,
	})
	if err != nil {
		return err
	}
	if err := execBound(db, `INSERT INTO renderer_outbox(
		command_id,zone_id,play_id,command_type,transport_kind,payload_json,created_revision,media_ready
	) VALUES (?,?,?,?,?,?,?,?)`, func(stmt *sqliteStmt) error {
		values := []string{result.CommandID, string(request.ZoneID), string(result.PlayID), backing, string(kind), string(payload)}
		for index, value := range values {
			if err := stmt.bindText(index+1, value); err != nil {
				return err
			}
		}
		if err := stmt.bindInt64(7, int64(result.Revision)); err != nil {
			return err
		}
		mediaReady := int64(1)
		if kind == TransportPlay {
			mediaReady = 0
		}
		return stmt.bindInt64(8, mediaReady)
	}); err != nil {
		return err
	}
	switch request.Command {
	case TransportPause:
		return updateTransportIntent(db, transportIntent{zoneID: request.ZoneID, revision: result.Revision, transport: TransportPaused})
	case TransportResume:
		return updateTransportIntent(db, transportIntent{zoneID: request.ZoneID, revision: result.Revision, transport: TransportPlaying})
	case TransportSeek:
		return updateTransportIntent(db, transportIntent{zoneID: request.ZoneID, revision: result.Revision, transport: zone.transport})
	case TransportPrevious:
		if plan.history == nil {
			return updateTransportIntent(db, transportIntent{zoneID: request.ZoneID, revision: result.Revision, transport: zone.transport})
		}
		return updatePreviousTransportIntent(db, request.ZoneID, result.Revision, result.PlayID)
	case TransportSkip:
		return updateTransportIntent(db, transportIntent{zoneID: request.ZoneID, revision: result.Revision, transport: TransportSuspended})
	case TransportStop:
		return stopTransportIntent(db, stopIntent{zoneID: request.ZoneID, zone: zone, revision: result.Revision})
	case TransportStart:
		return ErrInvalidRequest
	default:
		return ErrInvalidRequest
	}
}

type transportIntent struct {
	zoneID    ZoneID
	revision  Revision
	transport Transport
}

func updateTransportIntent(db *sqliteDB, intent transportIntent) error {
	zoneID, revision, transport := intent.zoneID, intent.revision, intent.transport
	return execBound(db, "UPDATE playback_zones SET revision=?,transport=? WHERE zone_id=?", func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(transport)); err != nil {
			return err
		}
		return stmt.bindText(3, string(zoneID))
	})
}

type stopIntent struct {
	zoneID   ZoneID
	zone     zoneRecord
	revision Revision
}

func stopTransportIntent(db *sqliteDB, intent stopIntent) error {
	zoneID, zone, revision := intent.zoneID, intent.zone, intent.revision
	if err := execBound(db, `UPDATE playback_plays SET state='stopped',terminal_revision=?
		WHERE play_id=? AND state IN ('reserved','playing')`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zone.currentPlay))
	}); err != nil {
		return err
	}
	if err := execBound(db, `UPDATE playback_queue SET
		state=CASE state WHEN 'reserved' THEN 'pending' WHEN 'playing' THEN 'completed' ELSE state END,
		reserved_play_id=NULL,terminal_revision=CASE state WHEN 'playing' THEN ? ELSE terminal_revision END
		WHERE reserved_play_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zone.currentPlay))
	}); err != nil {
		return err
	}
	if zone.sessionID != "" {
		if err := endSession(db, sessionEnd{sessionID: zone.sessionID, revision: revision, reason: "stop"}); err != nil {
			return err
		}
	}
	return execBound(db, `UPDATE playback_zones SET revision=?,transport='idle',session_id=NULL,
		session_seed=NULL,current_play_id=NULL WHERE zone_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindInt64(1, int64(revision)); err != nil {
			return err
		}
		return stmt.bindText(2, string(zoneID))
	})
}
