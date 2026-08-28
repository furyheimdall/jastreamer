package playback

import (
	"context"
	"sort"
	"time"
)

func (store *Store) Renderer(ctx context.Context, id RendererID) (RendererInventory, error) {
	var result RendererInventory
	err := store.read(ctx, func(db *sqliteDB) error {
		loaded, found, err := loadRenderer(db, id)
		if err != nil {
			return err
		}
		if !found {
			return ErrRendererNotFound
		}
		result = loaded
		return nil
	})
	return result, err
}

func (store *Store) Renderers(ctx context.Context) ([]RendererInventory, error) {
	result := []RendererInventory{}
	err := store.read(ctx, func(db *sqliteDB) error {
		stmt, err := db.prepare("SELECT renderer_id FROM renderer_registry ORDER BY renderer_id")
		if err != nil {
			return err
		}
		ids := []RendererID{}
		for {
			row, err := stmt.step()
			if err != nil {
				stmt.close()
				return err
			}
			if !row {
				break
			}
			ids = append(ids, RendererID(stmt.text(0)))
		}
		stmt.close()
		for _, id := range ids {
			loaded, found, err := loadRenderer(db, id)
			if err != nil {
				return err
			}
			if found {
				result = append(result, loaded)
			}
		}
		return nil
	})
	return result, err
}

func loadRenderer(db *sqliteDB, id RendererID) (RendererInventory, bool, error) {
	stmt, err := db.prepare(`SELECT kind,display_name,state,protocol_major,firmware_version,
		endpoint_fingerprint,revision,created_at,updated_at FROM renderer_registry WHERE renderer_id=?`)
	if err != nil {
		return RendererInventory{}, false, err
	}
	if err := stmt.bindText(1, string(id)); err != nil {
		stmt.close()
		return RendererInventory{}, false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		stmt.close()
		return RendererInventory{}, false, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, stmt.text(7))
	if err != nil {
		stmt.close()
		return RendererInventory{}, false, err
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, stmt.text(8))
	if err != nil {
		stmt.close()
		return RendererInventory{}, false, err
	}
	result := RendererInventory{Renderer: Renderer{
		ID: id, Kind: RendererKind(stmt.text(0)), DisplayName: stmt.text(1), State: RendererState(stmt.text(2)),
		ProtocolMajor: int(stmt.int64(3)), FirmwareVersion: stmt.text(4), EndpointFingerprint: stmt.text(5),
		Revision: Revision(stmt.int64(6)), CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, LastSeenAt: updatedAt}
	stmt.close()
	capabilities, err := loadRendererCapabilities(db, id)
	if err != nil {
		return RendererInventory{}, false, err
	}
	if result.Kind == RendererKindCustom {
		result.Capabilities = capabilities["custom.capability"]
		return result, true, nil
	}
	first := func(name string) string {
		if len(capabilities[name]) == 0 {
			return ""
		}
		return capabilities[name][0]
	}
	result.K17 = &K17RendererIdentity{
		UDN: first("k17.udn"), Model: first("k17.model"),
		DescriptionURL:              first("k17.description_url"),
		AVTransportControlURL:       first("k17.av_transport_control_url"),
		ConnectionManagerControlURL: first("k17.connection_manager_control_url"),
		ProtocolInfo:                first("k17.protocol_info"),
	}
	result.Capabilities = append([]string(nil), capabilities["k17.protocol"]...)
	if len(result.Capabilities) == 0 && result.K17.ProtocolInfo != "" {
		result.Capabilities = []string{result.K17.ProtocolInfo}
	}
	return result, true, nil
}

func loadRendererCapabilities(db *sqliteDB, id RendererID) (map[string][]string, error) {
	stmt, err := db.prepare("SELECT capability,capability_value FROM renderer_capabilities WHERE renderer_id=? ORDER BY capability,capability_value")
	if err != nil {
		return nil, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(id)); err != nil {
		return nil, err
	}
	values := map[string][]string{}
	for {
		row, err := stmt.step()
		if err != nil {
			return nil, err
		}
		if !row {
			break
		}
		name := stmt.text(0)
		values[name] = append(values[name], stmt.text(1))
	}
	for name := range values {
		sort.Strings(values[name])
	}
	return values, nil
}
