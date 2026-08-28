package playback

import (
	"context"
	"errors"
	"sort"
	"time"
)

var (
	ErrInvalidRenderer          = errors.New("playback: invalid renderer")
	ErrRendererNotFound         = errors.New("playback: renderer not found")
	ErrRendererAssigned         = errors.New("playback: renderer is assigned to another zone")
	ErrRendererIdentityConflict = errors.New("playback: renderer identity kind conflicts with existing inventory")
)

const RendererKindCustom RendererKind = RendererKindJake

type K17Renderer struct {
	ID                          RendererID
	DisplayName                 string
	State                       RendererState
	UDN                         string
	Model                       string
	FirmwareVersion             string
	DescriptionURL              string
	AVTransportControlURL       string
	ConnectionManagerControlURL string
	ProtocolInfo                string
	Protocols                   []string
	LastSeenAt                  time.Time
}

type CustomRenderer struct {
	ID                  RendererID
	DisplayName         string
	State               RendererState
	ProtocolMajor       int
	EndpointFingerprint string
	Capabilities        []string
	LastSeenAt          time.Time
}

type K17RendererIdentity struct {
	UDN                         string
	Model                       string
	DescriptionURL              string
	AVTransportControlURL       string
	ConnectionManagerControlURL string
	ProtocolInfo                string
}

type RendererInventory struct {
	Renderer
	Capabilities []string
	LastSeenAt   time.Time
	K17          *K17RendererIdentity
}

func (store *Store) UpsertK17Renderer(ctx context.Context, input K17Renderer) (RendererInventory, error) {
	if input.ID == "" || input.DisplayName == "" || input.Model == "" || !validRendererState(input.State) {
		return RendererInventory{}, ErrInvalidRenderer
	}
	capabilities := map[string][]string{
		"k17.udn": {input.UDN}, "k17.model": {input.Model},
		"k17.description_url":                {input.DescriptionURL},
		"k17.av_transport_control_url":       {input.AVTransportControlURL},
		"k17.connection_manager_control_url": {input.ConnectionManagerControlURL},
		"k17.protocol_info":                  {input.ProtocolInfo},
		"k17.protocol":                       append([]string(nil), input.Protocols...),
	}
	err := store.upsertRenderer(ctx, rendererWrite{
		id: input.ID, kind: RendererKindK17, displayName: input.DisplayName, state: input.State,
		firmware: input.FirmwareVersion, lastSeenAt: input.LastSeenAt, capabilities: capabilities,
	})
	if err != nil {
		return RendererInventory{}, err
	}
	return store.Renderer(ctx, input.ID)
}

func (store *Store) UpsertCustomRenderer(ctx context.Context, input CustomRenderer) (RendererInventory, error) {
	if input.ID == "" || input.DisplayName == "" || input.ProtocolMajor < 0 || !validRendererState(input.State) {
		return RendererInventory{}, ErrInvalidRenderer
	}
	capabilities := append([]string(nil), input.Capabilities...)
	sort.Strings(capabilities)
	for index, capability := range capabilities {
		if capability == "" || (index > 0 && capability == capabilities[index-1]) {
			return RendererInventory{}, ErrInvalidRenderer
		}
	}
	err := store.upsertRenderer(ctx, rendererWrite{
		id: input.ID, kind: RendererKindCustom, displayName: input.DisplayName, state: input.State,
		protocolMajor: input.ProtocolMajor, endpointFingerprint: input.EndpointFingerprint,
		lastSeenAt: input.LastSeenAt, capabilities: map[string][]string{"custom.capability": capabilities},
	})
	if err != nil {
		return RendererInventory{}, err
	}
	return store.Renderer(ctx, input.ID)
}

func validRendererState(state RendererState) bool {
	switch state {
	case RendererUnavailable, RendererAvailable, RendererConnected, RendererIncompatible, RendererRevoked:
		return true
	default:
		return false
	}
}

type rendererWrite struct {
	id                  RendererID
	kind                RendererKind
	displayName         string
	state               RendererState
	protocolMajor       int
	firmware            string
	endpointFingerprint string
	lastSeenAt          time.Time
	capabilities        map[string][]string
}

func (store *Store) upsertRenderer(ctx context.Context, input rendererWrite) error {
	return store.transaction(ctx, func(db *sqliteDB) error {
		revision, kind, found, err := rendererRevision(db, input.id)
		if err != nil {
			return err
		}
		if found && kind != input.kind {
			return ErrRendererIdentityConflict
		}
		revision++
		observedAt := input.lastSeenAt.UTC().Format(time.RFC3339Nano)
		if err := execBound(db, `INSERT INTO renderer_registry(
			renderer_id,kind,display_name,state,protocol_major,firmware_version,endpoint_fingerprint,revision,created_at,updated_at
		) VALUES (?,?,?,?,?,?,?,?,?,?) ON CONFLICT(renderer_id) DO UPDATE SET
			display_name=excluded.display_name,state=excluded.state,protocol_major=excluded.protocol_major,
			firmware_version=excluded.firmware_version,endpoint_fingerprint=excluded.endpoint_fingerprint,
			revision=excluded.revision,updated_at=excluded.updated_at`, func(stmt *sqliteStmt) error {
			values := []string{string(input.id), string(input.kind), input.displayName, string(input.state)}
			for index, value := range values {
				if err := stmt.bindText(index+1, value); err != nil {
					return err
				}
			}
			if input.protocolMajor == 0 {
				if err := stmt.bind(5, nil); err != nil {
					return err
				}
			} else if err := stmt.bindInt64(5, int64(input.protocolMajor)); err != nil {
				return err
			}
			for index, value := range []string{input.firmware, input.endpointFingerprint} {
				if err := stmt.bindText(index+6, value); err != nil {
					return err
				}
			}
			if err := stmt.bindInt64(8, int64(revision)); err != nil {
				return err
			}
			if err := stmt.bindText(9, observedAt); err != nil {
				return err
			}
			return stmt.bindText(10, observedAt)
		}); err != nil {
			return err
		}
		if err := execBound(db, "DELETE FROM renderer_capabilities WHERE renderer_id=?", func(stmt *sqliteStmt) error { return stmt.bindText(1, string(input.id)) }); err != nil {
			return err
		}
		for name, values := range input.capabilities {
			for _, value := range values {
				write := rendererCapabilityWrite{
					rendererID: input.id, name: name, value: value,
					revision: revision, observedAt: observedAt,
				}
				if err := insertRendererCapability(db, write); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func rendererRevision(db *sqliteDB, id RendererID) (Revision, RendererKind, bool, error) {
	stmt, err := db.prepare("SELECT revision,kind FROM renderer_registry WHERE renderer_id=?")
	if err != nil {
		return 0, "", false, err
	}
	defer stmt.close()
	if err := stmt.bindText(1, string(id)); err != nil {
		return 0, "", false, err
	}
	row, err := stmt.step()
	if err != nil || !row {
		return 0, "", false, err
	}
	return Revision(stmt.int64(0)), RendererKind(stmt.text(1)), true, nil
}

type rendererCapabilityWrite struct {
	rendererID RendererID
	name       string
	value      string
	revision   Revision
	observedAt string
}

func insertRendererCapability(db *sqliteDB, write rendererCapabilityWrite) error {
	return execBound(db, `INSERT INTO renderer_capabilities(renderer_id,capability,capability_value,observed_revision,observed_at) VALUES (?,?,?,?,?)`, func(stmt *sqliteStmt) error {
		for index, item := range []string{string(write.rendererID), write.name, write.value} {
			if err := stmt.bindText(index+1, item); err != nil {
				return err
			}
		}
		if err := stmt.bindInt64(4, int64(write.revision)); err != nil {
			return err
		}
		return stmt.bindText(5, write.observedAt)
	})
}
