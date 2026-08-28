package playback

import (
	"context"
	"strings"
	"time"
)

type K17LifecycleAction string

const (
	K17LifecycleIgnored    K17LifecycleAction = "ignored"
	K17LifecycleReconciled K17LifecycleAction = "reconciled"
	K17LifecycleSuspended  K17LifecycleAction = "suspended"
	K17LifecycleNaturalEnd K17LifecycleAction = "natural_end"
)

type K17LifecycleResult struct {
	Action         K17LifecycleAction
	RendererID     RendererID
	ZoneID         ZoneID
	PreviousPlayID PlayID
	BoundaryID     BoundaryID
	Decision       Decision
}

type k17ObservedIdentity struct {
	state  string
	playID PlayID
}

type k17TruthUpdate struct {
	observation K17Observation
	state       string
	observedAt  string
}

type k17PlayingApply struct {
	observation K17Observation
	zone        zoneRecord
}

type k17StoppedApply struct {
	observation K17Observation
	zone        zoneRecord
	prior       k17ObservedIdentity
}

func (store *Store) ApplyK17Observation(ctx context.Context, observation K17Observation) (K17LifecycleResult, error) {
	state, err := normalizedK17State(observation)
	if err != nil {
		return K17LifecycleResult{}, err
	}
	result := K17LifecycleResult{
		Action: K17LifecycleIgnored, RendererID: observation.RendererID, ZoneID: observation.ZoneID,
	}
	err = store.transaction(ctx, func(db *sqliteDB) error {
		renderer, found, err := loadRenderer(db, observation.RendererID)
		if err != nil {
			return err
		}
		if !found || renderer.Kind != RendererKindK17 {
			return ErrRendererNotFound
		}
		observedAt := observation.ObservedAt.UTC().Format(time.RFC3339Nano)
		if err := ensureK17Truth(db, observation.RendererID, observedAt); err != nil {
			return err
		}
		prior, err := loadK17ObservedIdentity(db, observation.RendererID)
		if err != nil {
			return err
		}
		if err := updateK17ObservedTruth(db, k17TruthUpdate{
			observation: observation, state: state, observedAt: observedAt,
		}); err != nil {
			return err
		}
		zone, err := loadZone(db, observation.ZoneID)
		if err != nil {
			return err
		}
		if !observation.Owned {
			result.Action = K17LifecycleSuspended
			return suspendK17Zone(db, observation.ZoneID, zone)
		}
		switch state {
		case "playing":
			return store.applyK17Playing(db, k17PlayingApply{
				observation: observation, zone: zone,
			}, &result)
		case "paused_playback":
			if zone.transport != TransportPaused {
				result.Action = K17LifecycleSuspended
				return suspendK17Zone(db, observation.ZoneID, zone)
			}
		case "stopped":
			return store.applyK17Stopped(db, k17StoppedApply{
				observation: observation, zone: zone, prior: prior,
			}, &result)
		case "transitioning", "unknown":
			result.Action = K17LifecycleSuspended
			return suspendK17Zone(db, observation.ZoneID, zone)
		}
		return nil
	})
	return result, err
}

func normalizedK17State(observation K17Observation) (string, error) {
	if observation.RendererID == "" || observation.ZoneID == "" || observation.Position < 0 || observation.ObservedAt.IsZero() {
		return "", ErrInvalidObservation
	}
	state := strings.ToLower(observation.Transport)
	switch state {
	case "playing", "paused_playback", "stopped", "transitioning", "unknown":
		return state, nil
	case "paused":
		return "paused_playback", nil
	default:
		return "", ErrInvalidObservation
	}
}

func loadK17ObservedIdentity(db *sqliteDB, rendererID RendererID) (k17ObservedIdentity, error) {
	stmt, err := db.prepare("SELECT observed_state,observed_play_id FROM renderer_session_state WHERE renderer_id=?")
	if err != nil {
		return k17ObservedIdentity{}, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(rendererID)); err != nil {
		return k17ObservedIdentity{}, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return k17ObservedIdentity{}, err
	}
	return k17ObservedIdentity{state: stmt.text(0), playID: PlayID(stmt.text(1))}, nil
}

func updateK17ObservedTruth(db *sqliteDB, update k17TruthUpdate) error {
	return execBound(db, `UPDATE renderer_session_state SET connection_state='connected',
		observed_state=?,observed_position_ms=?,observed_at=?,disconnected_at=NULL WHERE renderer_id=?`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, update.state); err != nil {
			return err
		}
		if err := stmt.bindInt64(2, update.observation.Position.Milliseconds()); err != nil {
			return err
		}
		if err := stmt.bindText(3, update.observedAt); err != nil {
			return err
		}
		return stmt.bindText(4, string(update.observation.RendererID))
	})
}

func (store *Store) applyK17Playing(db *sqliteDB, apply k17PlayingApply, result *K17LifecycleResult) error {
	observation, zone := apply.observation, apply.zone
	if zone.currentPlay == "" {
		result.Action = K17LifecycleSuspended
		return suspendK17Zone(db, observation.ZoneID, zone)
	}
	if zone.transport == TransportStarting || zone.transport == TransportSuspended {
		if err := reconcilePlaying(db, observation.ZoneID, zone); err != nil {
			return err
		}
		result.Action = K17LifecycleReconciled
	} else if zone.transport != TransportPlaying {
		result.Action = K17LifecycleSuspended
		return suspendK17Zone(db, observation.ZoneID, zone)
	}
	return execBound(db, "UPDATE renderer_session_state SET observed_play_id=? WHERE renderer_id=?", func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(zone.currentPlay)); err != nil {
			return err
		}
		return stmt.bindText(2, string(observation.RendererID))
	})
}

func (store *Store) applyK17Stopped(db *sqliteDB, apply k17StoppedApply, result *K17LifecycleResult) error {
	observation, zone, prior := apply.observation, apply.zone, apply.prior
	if zone.transport != TransportPlaying || zone.currentPlay == "" || prior.state != "playing" || prior.playID != zone.currentPlay {
		return nil
	}
	boundaryID := BoundaryID("k17-ended:" + zone.currentPlay)
	decision, err := store.commitNext(db, NextRequest{
		ZoneID:   observation.ZoneID,
		Boundary: Boundary{ID: boundaryID, PreviousPlayID: zone.currentPlay},
	})
	if err != nil {
		return err
	}
	result.Action = K17LifecycleNaturalEnd
	result.PreviousPlayID = zone.currentPlay
	result.BoundaryID = boundaryID
	result.Decision = decision
	return nil
}
