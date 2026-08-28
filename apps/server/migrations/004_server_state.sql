CREATE TABLE server_settings (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    settings_json TEXT NOT NULL CHECK (
        json_valid(settings_json)
        AND json_type(settings_json, '$.setup_secret') IS NULL
        AND json_type(settings_json, '$.token') IS NULL
        AND json_type(settings_json, '$.tls_private_key') IS NULL
    ),
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE server_catalog_roots (
    root_id TEXT PRIMARY KEY,
    display_name TEXT NOT NULL,
    canonical_path TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE server_catalog_scan_jobs (
    job_id TEXT PRIMARY KEY,
    root_id TEXT NOT NULL REFERENCES server_catalog_roots(root_id),
    requested_revision INTEGER NOT NULL CHECK (requested_revision >= 0),
    status TEXT NOT NULL CHECK (status IN ('queued','running','complete','failed','cancelled')),
    requested_at TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    catalog_revision INTEGER CHECK (catalog_revision IS NULL OR catalog_revision >= 0),
    error_code TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX server_catalog_scan_jobs_work
    ON server_catalog_scan_jobs(status,requested_at,job_id);

CREATE TABLE renderer_registry (
    renderer_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('k17','jake')),
    display_name TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('unavailable','available','connected','incompatible','revoked')),
    protocol_major INTEGER CHECK (protocol_major IS NULL OR protocol_major > 0),
    firmware_version TEXT NOT NULL DEFAULT '',
    endpoint_fingerprint TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE renderer_capabilities (
    renderer_id TEXT NOT NULL REFERENCES renderer_registry(renderer_id) ON DELETE CASCADE,
    capability TEXT NOT NULL,
    capability_value TEXT NOT NULL DEFAULT '',
    observed_revision INTEGER NOT NULL CHECK (observed_revision >= 0),
    observed_at TEXT NOT NULL,
    PRIMARY KEY (renderer_id,capability,capability_value)
) STRICT;

CREATE TABLE server_zones (
    zone_id TEXT PRIMARY KEY REFERENCES playback_zones(zone_id),
    display_name TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

CREATE TABLE renderer_assignments (
    assignment_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL REFERENCES server_zones(zone_id),
    renderer_id TEXT NOT NULL REFERENCES renderer_registry(renderer_id),
    assigned_revision INTEGER NOT NULL CHECK (assigned_revision >= 0),
    assigned_at TEXT NOT NULL,
    unassigned_revision INTEGER CHECK (unassigned_revision IS NULL OR unassigned_revision >= assigned_revision),
    unassigned_at TEXT
) STRICT;
CREATE UNIQUE INDEX renderer_assignments_active_zone
    ON renderer_assignments(zone_id) WHERE unassigned_revision IS NULL;
CREATE UNIQUE INDEX renderer_assignments_active_renderer
    ON renderer_assignments(renderer_id) WHERE unassigned_revision IS NULL;

CREATE TABLE media_signing_keys (
    key_id TEXT PRIMARY KEY,
    key_digest TEXT NOT NULL UNIQUE,
    key_ciphertext BLOB NOT NULL CHECK (length(key_ciphertext) > 0),
    key_nonce BLOB NOT NULL CHECK (length(key_nonce) > 0),
    wrapping_key_id TEXT NOT NULL CHECK (wrapping_key_id <> ''),
    created_at TEXT NOT NULL,
    retired_at TEXT
) STRICT;
CREATE UNIQUE INDEX media_signing_keys_one_active
    ON media_signing_keys((1)) WHERE retired_at IS NULL;

CREATE TABLE server_event_epoch (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    epoch INTEGER NOT NULL CHECK (epoch > 0),
    revision INTEGER NOT NULL CHECK (revision >= 0),
    updated_at TEXT NOT NULL
) STRICT;
INSERT INTO server_event_epoch(singleton,epoch,revision,updated_at)
VALUES (1,1,0,'');

ALTER TABLE renderer_outbox ADD COLUMN renderer_id TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_outbox ADD COLUMN sequence INTEGER NOT NULL DEFAULT 0 CHECK (sequence >= 0);
ALTER TABLE renderer_outbox ADD COLUMN payload_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(payload_json));
ALTER TABLE renderer_outbox ADD COLUMN receipt_state TEXT NOT NULL DEFAULT 'pending'
    CHECK (receipt_state IN ('pending','received','terminal'));
ALTER TABLE renderer_outbox ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0);
ALTER TABLE renderer_outbox ADD COLUMN last_error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_outbox ADD COLUMN last_error_detail TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_outbox ADD COLUMN result_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(result_json));
ALTER TABLE renderer_outbox ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_outbox ADD COLUMN last_attempt_at TEXT;
ALTER TABLE renderer_outbox ADD COLUMN received_at TEXT;
ALTER TABLE renderer_outbox ADD COLUMN terminal_at TEXT;
UPDATE renderer_outbox SET sequence=created_revision WHERE sequence=0;
CREATE UNIQUE INDEX renderer_outbox_renderer_sequence
    ON renderer_outbox(renderer_id,sequence) WHERE renderer_id<>'';
CREATE TRIGGER renderer_outbox_immutable_delivery
BEFORE UPDATE OF command_id,zone_id,play_id,command_type,renderer_id,sequence,payload_json,created_revision,created_at
ON renderer_outbox
WHEN OLD.renderer_id<>'' AND (
    OLD.command_id<>NEW.command_id OR OLD.zone_id<>NEW.zone_id OR OLD.play_id<>NEW.play_id
    OR OLD.command_type<>NEW.command_type OR OLD.renderer_id<>NEW.renderer_id
    OR OLD.sequence<>NEW.sequence OR OLD.payload_json<>NEW.payload_json
    OR OLD.created_revision<>NEW.created_revision OR OLD.created_at<>NEW.created_at
)
BEGIN
    SELECT RAISE(ABORT, 'renderer outbox delivery identity is immutable');
END;

CREATE TABLE renderer_command_results (
    command_id TEXT PRIMARY KEY REFERENCES renderer_outbox(command_id) ON DELETE CASCADE,
    renderer_id TEXT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence >= 0),
    outcome TEXT NOT NULL CHECK (outcome IN ('succeeded','failed','unsupported','cancelled')),
    result_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(result_json)),
    error_code TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    recorded_at TEXT NOT NULL,
    UNIQUE(renderer_id,sequence)
) STRICT;

CREATE TABLE ffmpeg_probe_status (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    configured_path TEXT NOT NULL,
    executable_fingerprint TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('unconfigured','available','unavailable','incompatible')),
    version TEXT NOT NULL DEFAULT '',
    codecs_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(codecs_json) AND json_type(codecs_json)='array'),
    error_code TEXT NOT NULL DEFAULT '',
    error_detail TEXT NOT NULL DEFAULT '',
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    probed_at TEXT NOT NULL
) STRICT;
INSERT INTO ffmpeg_probe_status(singleton,configured_path,status,probed_at)
VALUES (1,'','unconfigured','');

UPDATE playback_schema SET version=4, minimum_reader_version=4 WHERE singleton=1;
PRAGMA user_version = 4;
