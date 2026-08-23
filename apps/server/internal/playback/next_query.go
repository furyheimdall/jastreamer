package playback

import "context"

type PreviewState struct {
	Decision    Decision
	Active      bool
	Replaceable bool
	Committed   bool
}

func (store *Store) LatestDecision(ctx context.Context, zoneID ZoneID) (Decision, bool, error) {
	value := Decision{}
	found := false
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT ` + decisionAttemptColumns + `
			FROM playback_decision_attempts WHERE zone_id=?
			ORDER BY committed_revision DESC,attempt DESC LIMIT 1`)
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(zoneID)); err != nil {
			return err
		}
		row, err := stmt.step()
		if err != nil || !row {
			return err
		}
		value, err = decisionFromRow(stmt)
		found = err == nil
		return err
	})
	return value, found, err
}

func (store *Store) AutomaticPreview(ctx context.Context, zoneID ZoneID) (PreviewState, bool, error) {
	state := PreviewState{}
	found := false
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`
			SELECT track_id,state,created_revision,terminal_revision,play_id,
				COALESCE(source,''),COALESCE(reason,'')
			FROM playback_automatic_previews WHERE zone_id=?
			ORDER BY created_revision DESC LIMIT 1`)
		if err != nil {
			return err
		}
		defer stmt.close()
		if err := stmt.bindText(1, string(zoneID)); err != nil {
			return err
		}
		row, err := stmt.step()
		if err != nil || !row {
			return err
		}
		found = true
		previewState := stmt.text(1)
		state = PreviewState{
			Decision: Decision{
				Kind: DecisionPlay, TrackID: TrackID(stmt.text(0)), Revision: Revision(stmt.int64(2)),
				PlayID: PlayID(stmt.text(4)), Source: stmt.text(5), Reason: stmt.text(6),
			},
			Active: previewState != "cancelled", Replaceable: previewState == "preview",
			Committed: previewState == "committed",
		}
		return nil
	})
	return state, found, err
}
