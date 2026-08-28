package playback

import (
	"context"
	"time"
)

func (store *Store) recoverRendererSessions(ctx context.Context) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare(`SELECT s.renderer_id,r.state FROM renderer_session_state s
			JOIN renderer_registry r ON r.renderer_id=s.renderer_id
			WHERE s.connection_state='connected' ORDER BY s.renderer_id`)
		if err != nil {
			return err
		}
		type recovery struct {
			rendererID RendererID
			revoked    bool
		}
		pending := []recovery{}
		for {
			row, err := stmt.step()
			if err != nil {
				stmt.close()
				return err
			}
			if !row {
				break
			}
			pending = append(pending, recovery{
				rendererID: RendererID(stmt.text(0)), revoked: RendererState(stmt.text(1)) == RendererRevoked,
			})
		}
		stmt.close()
		recoveredAt := store.clock.Now().UTC().Format(time.RFC3339Nano)
		for _, item := range pending {
			state := "disconnected"
			if item.revoked {
				state = "revoked"
			}
			if err := execBound(db, `UPDATE renderer_session_state
				SET connection_state=?,disconnected_at=? WHERE renderer_id=?`, func(stmt *sqliteStmt) error {
				for index, value := range []string{state, recoveredAt, string(item.rendererID)} {
					if err := stmt.bindText(index+1, value); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if !item.revoked {
				if err := setRendererConnectionState(db, rendererConnectionUpdate{
					rendererID: item.rendererID, state: RendererAvailable, observedAt: recoveredAt,
				}); err != nil {
					return err
				}
			}
			if err := suspendAssignedRenderer(db, item.rendererID); err != nil {
				return err
			}
		}
		return nil
	})
}
