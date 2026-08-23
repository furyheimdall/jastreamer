CREATE TABLE playback_schema (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    version INTEGER NOT NULL,
    minimum_reader_version INTEGER NOT NULL
) STRICT;
INSERT INTO playback_schema(singleton, version, minimum_reader_version) VALUES (1, 2, 2);

CREATE TABLE playback_zones (
    zone_id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    transport TEXT NOT NULL DEFAULT 'idle' CHECK (transport IN ('idle','selecting','starting','playing','paused','blocked','suspended')),
    session_id TEXT,
    session_seed TEXT,
    decision_sequence INTEGER NOT NULL DEFAULT 0,
    queue_sequence INTEGER NOT NULL DEFAULT 0,
    current_play_id TEXT
) STRICT;
CREATE TABLE playback_sessions (
    session_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    seed TEXT NOT NULL,
    started_revision INTEGER NOT NULL,
    ended_revision INTEGER,
    end_reason TEXT,
    FOREIGN KEY(zone_id) REFERENCES playback_zones(zone_id)
) STRICT;
CREATE UNIQUE INDEX playback_sessions_one_active
    ON playback_sessions(zone_id) WHERE ended_revision IS NULL;
CREATE TABLE playback_queue (
    entry_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    position INTEGER NOT NULL,
    track_id TEXT NOT NULL,
    available INTEGER NOT NULL CHECK (available IN (0, 1)),
    state TEXT NOT NULL CHECK (state IN ('pending', 'reserved', 'playing', 'completed', 'blocked', 'removed')),
    reserved_play_id TEXT,
    created_revision INTEGER NOT NULL,
    terminal_revision INTEGER,
    UNIQUE(zone_id, position),
    FOREIGN KEY(zone_id) REFERENCES playback_zones(zone_id)
) STRICT;
CREATE INDEX playback_queue_head ON playback_queue(zone_id, state, position);
CREATE TABLE playback_plays (
    play_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    queue_entry_id TEXT,
    track_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('reserved', 'playing', 'completed', 'stopped', 'suspended')),
    boundary_id TEXT NOT NULL,
    started_revision INTEGER,
    terminal_revision INTEGER,
    FOREIGN KEY(zone_id) REFERENCES playback_zones(zone_id),
    FOREIGN KEY(session_id) REFERENCES playback_sessions(session_id),
    FOREIGN KEY(queue_entry_id) REFERENCES playback_queue(entry_id)
) STRICT;
CREATE UNIQUE INDEX playback_plays_one_active
    ON playback_plays(zone_id) WHERE state IN ('reserved','playing');
CREATE TABLE playback_decisions (
    decision_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    boundary_id TEXT NOT NULL,
    previous_play_id TEXT NOT NULL DEFAULT '',
    sequence INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('play', 'stop', 'block')),
    reason TEXT NOT NULL,
    play_id TEXT,
    queue_entry_id TEXT,
    committed_revision INTEGER NOT NULL,
    UNIQUE(zone_id, session_id, boundary_id)
) STRICT;
CREATE INDEX playback_decisions_boundary
    ON playback_decisions(zone_id, session_id, boundary_id);
CREATE TABLE playback_automatic_previews (
    zone_id TEXT NOT NULL,
    boundary_id TEXT NOT NULL,
    previous_play_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    track_id TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('preview', 'cancelled', 'committed')),
    created_revision INTEGER NOT NULL,
    terminal_revision INTEGER,
    play_id TEXT,
    PRIMARY KEY(zone_id, boundary_id),
    FOREIGN KEY(zone_id) REFERENCES playback_zones(zone_id),
    FOREIGN KEY(session_id) REFERENCES playback_sessions(session_id)
) STRICT;
CREATE UNIQUE INDEX playback_automatic_one_preview
    ON playback_automatic_previews(zone_id) WHERE state = 'preview';
CREATE TABLE renderer_outbox (
    command_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    play_id TEXT NOT NULL,
    command_type TEXT NOT NULL CHECK (command_type IN ('play', 'pause', 'resume', 'stop')),
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sent', 'confirmed')),
    created_revision INTEGER NOT NULL
) STRICT;
CREATE TABLE playback_idempotency (
    zone_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    operation TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    result_revision INTEGER NOT NULL,
    PRIMARY KEY(zone_id, idempotency_key)
) STRICT;
CREATE TABLE playback_tombstones (
    tombstone_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    revision INTEGER NOT NULL
) STRICT;
PRAGMA user_version = 2;
