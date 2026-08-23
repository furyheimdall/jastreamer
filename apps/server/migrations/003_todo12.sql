ALTER TABLE playback_sessions
    ADD COLUMN continuation_mode TEXT NOT NULL DEFAULT 'stop'
    CHECK (continuation_mode IN ('stop','album','similar'));

ALTER TABLE renderer_outbox ADD COLUMN failed_revision INTEGER;
ALTER TABLE playback_automatic_previews ADD COLUMN source TEXT;
ALTER TABLE playback_automatic_previews ADD COLUMN reason TEXT;

CREATE TABLE playback_continuation_policies (
    zone_id TEXT PRIMARY KEY,
    mode TEXT NOT NULL DEFAULT 'stop' CHECK (mode IN ('stop','album','similar')),
    artist_gap INTEGER NOT NULL DEFAULT 4 CHECK (artist_gap >= 0 AND artist_gap <= 100),
    album_gap INTEGER NOT NULL DEFAULT 10 CHECK (album_gap >= 0 AND album_gap <= 100),
    session_override TEXT NOT NULL DEFAULT '' CHECK (session_override IN ('','stop','album','similar')),
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    FOREIGN KEY(zone_id) REFERENCES playback_zones(zone_id)
) STRICT;

CREATE TABLE playback_decision_attempts (
    decision_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    boundary_id TEXT NOT NULL,
    previous_play_id TEXT NOT NULL DEFAULT '',
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    sequence INTEGER NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('play','stop','block')),
    reason TEXT NOT NULL,
    source TEXT NOT NULL DEFAULT '' CHECK (source IN ('','explicit','album','similar')),
    play_id TEXT,
    queue_entry_id TEXT,
    track_id TEXT NOT NULL DEFAULT '',
    recording_key TEXT NOT NULL DEFAULT '',
    explanation_json TEXT NOT NULL DEFAULT '{}',
    recording_id TEXT NOT NULL DEFAULT '',
    album_id TEXT NOT NULL DEFAULT '',
    order_disc_known INTEGER NOT NULL DEFAULT 0 CHECK (order_disc_known IN (0,1)),
    order_disc_value INTEGER NOT NULL DEFAULT 0,
    order_track_known INTEGER NOT NULL DEFAULT 0 CHECK (order_track_known IN (0,1)),
    order_track_value INTEGER NOT NULL DEFAULT 0,
    order_natural_path TEXT NOT NULL DEFAULT '',
    order_track_id TEXT NOT NULL DEFAULT '',
    committed_revision INTEGER NOT NULL,
    UNIQUE(zone_id, session_id, boundary_id, attempt),
    FOREIGN KEY(zone_id) REFERENCES playback_zones(zone_id),
    FOREIGN KEY(session_id) REFERENCES playback_sessions(session_id),
    FOREIGN KEY(play_id) REFERENCES playback_plays(play_id),
    FOREIGN KEY(queue_entry_id) REFERENCES playback_queue(entry_id)
) STRICT;
CREATE INDEX playback_decision_attempts_boundary
    ON playback_decision_attempts(zone_id, session_id, boundary_id, attempt DESC);
CREATE UNIQUE INDEX playback_decision_attempts_play
    ON playback_decision_attempts(play_id) WHERE play_id IS NOT NULL;

CREATE TABLE playback_start_failures (
    failed_play_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    session_id TEXT NOT NULL,
    boundary_id TEXT NOT NULL,
    track_id TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('explicit','album','similar')),
    failure_index INTEGER NOT NULL CHECK (failure_index > 0),
    result_decision_id TEXT NOT NULL,
    failed_revision INTEGER NOT NULL,
    UNIQUE(zone_id, session_id, boundary_id, track_id),
    FOREIGN KEY(failed_play_id) REFERENCES playback_plays(play_id),
    FOREIGN KEY(result_decision_id) REFERENCES playback_decision_attempts(decision_id)
) STRICT;
CREATE INDEX playback_start_failures_boundary
    ON playback_start_failures(zone_id, session_id, boundary_id, failure_index);

CREATE TABLE playback_album_state (
    session_id TEXT PRIMARY KEY,
    album_id TEXT NOT NULL,
    order_disc_known INTEGER NOT NULL CHECK (order_disc_known IN (0,1)),
    order_disc_value INTEGER NOT NULL,
    order_track_known INTEGER NOT NULL CHECK (order_track_known IN (0,1)),
    order_track_value INTEGER NOT NULL,
    order_natural_path TEXT NOT NULL,
    order_track_id TEXT NOT NULL,
    FOREIGN KEY(session_id) REFERENCES playback_sessions(session_id)
) STRICT;

CREATE TABLE playback_session_recordings (
    session_id TEXT NOT NULL,
    recording_key TEXT NOT NULL,
    recording_id TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(session_id, recording_key),
    FOREIGN KEY(session_id) REFERENCES playback_sessions(session_id)
) STRICT;

INSERT INTO playback_decision_attempts(
    decision_id,zone_id,session_id,boundary_id,previous_play_id,attempt,sequence,
    kind,reason,source,play_id,queue_entry_id,track_id,recording_key,committed_revision
)
SELECT d.decision_id,d.zone_id,d.session_id,d.boundary_id,d.previous_play_id,1,d.sequence,
       d.kind,d.reason,
       CASE d.reason WHEN 'PLAY_EXPLICIT' THEN 'explicit' ELSE '' END,
       d.play_id,d.queue_entry_id,COALESCE(p.track_id,''),
       CASE WHEN p.track_id IS NULL THEN '' ELSE 'track:' || p.track_id END,
       d.committed_revision
FROM playback_decisions d
LEFT JOIN playback_plays p ON p.play_id=d.play_id;

UPDATE playback_schema SET version=3, minimum_reader_version=3 WHERE singleton=1;
PRAGMA user_version = 3;
