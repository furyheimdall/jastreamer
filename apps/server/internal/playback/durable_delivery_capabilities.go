package playback

import (
	"context"
	"sort"
	"time"
)

func (store *Store) ObserveRendererSession(
	ctx context.Context,
	observation RendererSessionObservation,
) error {
	if observation.RendererID == "" || observation.Epoch == "" || observation.ProtocolMajor <= 0 ||
		observation.ObservedAt.IsZero() || len(observation.Capabilities) > 128 {
		return ErrInvalidRequest
	}
	capabilities := append([]string(nil), observation.Capabilities...)
	sort.Strings(capabilities)
	for index, capability := range capabilities {
		if capability == "" || (index > 0 && capability == capabilities[index-1]) {
			return ErrInvalidRequest
		}
	}
	return store.transaction(ctx, func(db *sqliteDB) error {
		if err := assertRendererEpoch(db, observation.RendererID, observation.Epoch); err != nil {
			return err
		}
		revision, _, found, err := rendererRevision(db, observation.RendererID)
		if err != nil {
			return err
		}
		if !found {
			return ErrRendererNotFound
		}
		revision++
		observedAt := observation.ObservedAt.UTC().Format(time.RFC3339Nano)
		if err := execBound(db, `UPDATE renderer_registry SET state='connected',protocol_major=?,
			revision=?,updated_at=? WHERE renderer_id=? AND state<>'revoked'`, func(stmt *sqliteStmt) error {
			if err := stmt.bindInt64(1, int64(observation.ProtocolMajor)); err != nil {
				return err
			}
			if err := stmt.bindInt64(2, int64(revision)); err != nil {
				return err
			}
			if err := stmt.bindText(3, observedAt); err != nil {
				return err
			}
			return stmt.bindText(4, string(observation.RendererID))
		}); err != nil {
			return err
		}
		if err := execBound(db, `DELETE FROM renderer_capabilities
			WHERE renderer_id=? AND capability='custom.capability'`, func(stmt *sqliteStmt) error {
			return stmt.bindText(1, string(observation.RendererID))
		}); err != nil {
			return err
		}
		for _, capability := range capabilities {
			if err := insertRendererCapability(db, rendererCapabilityWrite{
				rendererID: observation.RendererID, name: "custom.capability", value: capability,
				revision: revision, observedAt: observedAt,
			}); err != nil {
				return err
			}
		}
		return nil
	})
}
