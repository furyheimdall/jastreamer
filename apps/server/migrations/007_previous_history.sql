CREATE TABLE playback_previous_history (
    history_id TEXT PRIMARY KEY,
    zone_id TEXT NOT NULL,
    source_play_id TEXT NOT NULL UNIQUE,
    source_queue_entry_id TEXT,
    track_id TEXT NOT NULL,
    completed_revision INTEGER NOT NULL,
    consumed_revision INTEGER,
    replay_play_id TEXT UNIQUE,
    FOREIGN KEY(zone_id) REFERENCES playback_zones(zone_id)
) STRICT;
CREATE INDEX playback_previous_history_available
    ON playback_previous_history(zone_id, consumed_revision, completed_revision DESC);
UPDATE playback_schema SET version=7, minimum_reader_version=7 WHERE singleton=1;
PRAGMA user_version = 7;
