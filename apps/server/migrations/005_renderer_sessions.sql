ALTER TABLE renderer_outbox ADD COLUMN session_id TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_outbox ADD COLUMN deadline TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_outbox ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 8 CHECK (max_attempts > 0 AND max_attempts <= 32);
ALTER TABLE renderer_outbox ADD COLUMN next_attempt_at TEXT;
ALTER TABLE renderer_outbox ADD COLUMN ack_status TEXT NOT NULL DEFAULT ''
    CHECK (ack_status IN ('','received','duplicate','rejected'));
ALTER TABLE renderer_outbox ADD COLUMN ack_error_json TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(ack_error_json));
ALTER TABLE renderer_outbox ADD COLUMN superseded_at TEXT;
ALTER TABLE renderer_outbox ADD COLUMN superseded_by TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_outbox ADD COLUMN result_ack_at TEXT;
UPDATE renderer_outbox
SET session_id=COALESCE((SELECT session_id FROM playback_plays WHERE playback_plays.play_id=renderer_outbox.play_id),'')
WHERE session_id='';

CREATE TRIGGER renderer_outbox_immutable_session
BEFORE UPDATE OF session_id,deadline,max_attempts
ON renderer_outbox
WHEN OLD.renderer_id<>'' AND (
    OLD.session_id<>NEW.session_id OR OLD.deadline<>NEW.deadline OR OLD.max_attempts<>NEW.max_attempts
)
BEGIN
    SELECT RAISE(ABORT, 'renderer outbox session identity is immutable');
END;

ALTER TABLE renderer_command_results ADD COLUMN result_id TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_command_results ADD COLUMN wire_status TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_command_results ADD COLUMN observed_state TEXT NOT NULL DEFAULT '';
ALTER TABLE renderer_command_results ADD COLUMN position_ms INTEGER CHECK (position_ms IS NULL OR position_ms >= 0);
ALTER TABLE renderer_command_results ADD COLUMN acknowledged_at TEXT;
CREATE UNIQUE INDEX renderer_command_results_result_id
    ON renderer_command_results(renderer_id,result_id) WHERE result_id<>'';

CREATE TABLE renderer_session_state (
    renderer_id TEXT PRIMARY KEY REFERENCES renderer_registry(renderer_id) ON DELETE CASCADE,
    generation INTEGER NOT NULL DEFAULT 0 CHECK (generation >= 0),
    current_epoch TEXT NOT NULL DEFAULT '',
    connection_state TEXT NOT NULL DEFAULT 'disconnected'
        CHECK (connection_state IN ('connected','disconnected','revoked')),
    next_sequence INTEGER NOT NULL DEFAULT 1 CHECK (next_sequence > 0),
    reconnect_cursor INTEGER NOT NULL DEFAULT 0 CHECK (reconnect_cursor >= 0),
    observed_play_id TEXT NOT NULL DEFAULT '',
    observed_state TEXT NOT NULL DEFAULT 'unknown',
    observed_position_ms INTEGER CHECK (observed_position_ms IS NULL OR observed_position_ms >= 0),
    observed_at TEXT,
    connected_at TEXT,
    disconnected_at TEXT
) STRICT;

CREATE TABLE renderer_playback_events (
    event_id TEXT NOT NULL,
    renderer_id TEXT NOT NULL REFERENCES renderer_registry(renderer_id) ON DELETE CASCADE,
    session_epoch TEXT NOT NULL,
    play_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    position_ms INTEGER CHECK (position_ms IS NULL OR position_ms >= 0),
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    observed_at TEXT NOT NULL,
    recorded_at TEXT NOT NULL,
    handled_at TEXT,
    decision_id TEXT,
    PRIMARY KEY(renderer_id,event_id)
) STRICT;
CREATE INDEX renderer_playback_events_renderer_time
    ON renderer_playback_events(renderer_id,recorded_at,event_id);

UPDATE playback_schema SET version=5, minimum_reader_version=5 WHERE singleton=1;
PRAGMA user_version = 5;
