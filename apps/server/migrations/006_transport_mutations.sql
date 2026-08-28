ALTER TABLE renderer_outbox ADD COLUMN transport_kind TEXT NOT NULL DEFAULT ''
    CHECK (transport_kind IN ('','play','pause','resume','stop','seek','skip','previous'));
ALTER TABLE renderer_outbox ADD COLUMN media_ready INTEGER NOT NULL DEFAULT 1 CHECK (media_ready IN (0,1));
CREATE TRIGGER renderer_outbox_immutable_transport_kind
BEFORE UPDATE OF transport_kind ON renderer_outbox
WHEN OLD.transport_kind<>NEW.transport_kind
BEGIN
    SELECT RAISE(ABORT, 'renderer outbox transport kind is immutable');
END;
UPDATE playback_schema SET version=6, minimum_reader_version=6 WHERE singleton=1;
PRAGMA user_version = 6;
