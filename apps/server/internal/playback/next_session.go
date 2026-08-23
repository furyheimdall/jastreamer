package playback

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/jakestreamer/jstreamer-server/internal/decision"
)

func ensureDecisionSession(db *sqliteDB, zoneID ZoneID, zone zoneRecord) (zoneRecord, error) {
	if zone.sessionID != "" && zone.transport != TransportIdle {
		return zone, nil
	}
	if zone.sessionID != "" {
		if err := endSession(db, sessionEnd{
			sessionID: zone.sessionID, revision: zone.revision + 1, reason: "new_idle_play",
		}); err != nil {
			return zoneRecord{}, err
		}
	}
	policy, err := loadContinuationPolicy(db, zoneID)
	if err != nil {
		return zoneRecord{}, err
	}
	seed := make([]byte, 16)
	if _, err := rand.Read(seed); err != nil {
		return zoneRecord{}, fmt.Errorf("generate session seed: %w", err)
	}
	zone.sessionID = SessionID(fmt.Sprintf("%s:s:%020d", zoneID, zone.revision+1))
	zone.seed = hex.EncodeToString(seed)
	mode := policy.effectiveMode()
	if err := insertPlaybackSession(db, zoneID, zone, mode); err != nil {
		return zoneRecord{}, err
	}
	return zone, nil
}

func insertPlaybackSession(db *sqliteDB, zoneID ZoneID, zone zoneRecord, mode decision.Policy) error {
	return execBound(db, `
		INSERT INTO playback_sessions(
			session_id,zone_id,seed,started_revision,continuation_mode
		) VALUES (?,?,?,?,?)`, func(stmt *sqliteStmt) error {
		if err := stmt.bindText(1, string(zone.sessionID)); err != nil {
			return err
		}
		if err := stmt.bindText(2, string(zoneID)); err != nil {
			return err
		}
		if err := stmt.bindText(3, zone.seed); err != nil {
			return err
		}
		if err := stmt.bindInt64(4, int64(zone.revision+1)); err != nil {
			return err
		}
		return stmt.bindText(5, string(mode))
	})
}

func sessionPolicy(db *sqliteDB, sessionID SessionID) (decision.Policy, error) {
	stmt, err := db.prepare("SELECT continuation_mode FROM playback_sessions WHERE session_id=?")
	if err != nil {
		return "", err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(sessionID)); err != nil {
		return "", err
	}
	row, err := stmt.step()
	if err != nil {
		return "", err
	}
	if !row {
		return "", ErrCorruptDatabase
	}
	mode := decision.Policy(stmt.text(0))
	if !mode.Valid() {
		return "", ErrCorruptDatabase
	}
	return mode, nil
}
