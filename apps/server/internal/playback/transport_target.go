package playback

import "context"

func (store *Store) AssignedRenderer(ctx context.Context, zoneID ZoneID) (RendererInventory, error) {
	var renderer RendererInventory
	err := store.read(ctx, func(db *sqliteDB) error {
		zone, found, err := loadZoneInventory(db, zoneID)
		if err != nil {
			return err
		}
		if !found || zone.RendererID == "" {
			return ErrRendererRequired
		}
		loaded, found, err := loadRenderer(db, zone.RendererID)
		if err != nil {
			return err
		}
		if !found {
			return ErrRendererRequired
		}
		renderer = loaded
		return nil
	})
	return renderer, err
}
