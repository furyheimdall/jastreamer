PRAGMA foreign_keys = ON;

CREATE TABLE catalog_roots (
    root_id TEXT PRIMARY KEY,
    canonical_path TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE catalog_scans (
    root_id TEXT NOT NULL REFERENCES catalog_roots(root_id),
    generation INTEGER NOT NULL CHECK (generation > 0),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    catalog_revision INTEGER NOT NULL CHECK (catalog_revision >= 0),
    PRIMARY KEY (root_id, generation)
) STRICT;

CREATE TABLE catalog_files (
    file_id TEXT PRIMARY KEY,
    root_id TEXT NOT NULL REFERENCES catalog_roots(root_id),
    relative_path TEXT NOT NULL,
    media_format TEXT NOT NULL CHECK (media_format IN ('flac', 'mp3', 'ogg-vorbis', 'opus', 'pcm-wav')),
    content_fingerprint TEXT NOT NULL,
    byte_size INTEGER NOT NULL CHECK (byte_size >= 0),
    modified_ns INTEGER NOT NULL,
    available INTEGER NOT NULL CHECK (available IN (0, 1)),
    first_generation INTEGER NOT NULL CHECK (first_generation > 0),
    last_generation INTEGER NOT NULL CHECK (last_generation >= first_generation),
    tombstoned_generation INTEGER,
    UNIQUE (root_id, relative_path)
) STRICT;

CREATE INDEX catalog_files_fingerprint_idx
    ON catalog_files(root_id, content_fingerprint);
CREATE INDEX catalog_files_generation_idx
    ON catalog_files(root_id, last_generation, available);

CREATE TABLE catalog_recordings (
    recording_id TEXT PRIMARY KEY,
    embedded_recording_id TEXT,
    fallback_fingerprint TEXT NOT NULL,
    normalized_title TEXT NOT NULL,
    normalized_primary_artist TEXT NOT NULL
) STRICT;

CREATE TABLE catalog_albums (
    album_id TEXT PRIMARY KEY,
    embedded_release_id TEXT,
    normalized_title TEXT NOT NULL,
    normalized_album_artist TEXT NOT NULL,
    directory_boundary TEXT NOT NULL
) STRICT;

CREATE TABLE catalog_tracks (
    track_id TEXT PRIMARY KEY,
    file_id TEXT NOT NULL UNIQUE REFERENCES catalog_files(file_id),
    recording_id TEXT NOT NULL REFERENCES catalog_recordings(recording_id),
    album_id TEXT NOT NULL REFERENCES catalog_albums(album_id),
    title TEXT NOT NULL,
    artist TEXT NOT NULL,
    album_title TEXT NOT NULL,
    album_artist TEXT NOT NULL,
    disc_number INTEGER CHECK (disc_number IS NULL OR disc_number > 0),
    track_number INTEGER CHECK (track_number IS NULL OR track_number > 0),
    natural_path_key TEXT NOT NULL,
    order_track_id TEXT NOT NULL,
    available INTEGER NOT NULL CHECK (available IN (0, 1)),
    last_generation INTEGER NOT NULL CHECK (last_generation > 0)
) STRICT;

CREATE INDEX catalog_tracks_album_order_idx
    ON catalog_tracks(
        album_id,
        disc_number IS NULL,
        disc_number,
        track_number IS NULL,
        track_number,
        natural_path_key,
        order_track_id
    );
CREATE INDEX catalog_tracks_recording_idx ON catalog_tracks(recording_id);

CREATE TABLE catalog_analysis (
    track_id TEXT PRIMARY KEY REFERENCES catalog_tracks(track_id),
    content_fingerprint TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'complete', 'failed')),
    analyzer_id TEXT NOT NULL DEFAULT '',
    analyzer_version TEXT NOT NULL DEFAULT '',
    feature_schema_version INTEGER NOT NULL DEFAULT 0 CHECK (feature_schema_version >= 0),
    normalizer_id TEXT NOT NULL DEFAULT '',
    normalizer_version TEXT NOT NULL DEFAULT '',
    failure_reason TEXT NOT NULL DEFAULT '',
    feature_vector BLOB NOT NULL DEFAULT X'',
    updated_at TEXT NOT NULL
) STRICT;

CREATE INDEX catalog_analysis_work_idx
    ON catalog_analysis(status, feature_schema_version, analyzer_id, analyzer_version, normalizer_id, normalizer_version);

CREATE TABLE catalog_scan_issues (
    root_id TEXT NOT NULL,
    generation INTEGER NOT NULL,
    relative_path TEXT NOT NULL,
    issue_code TEXT NOT NULL,
    detail TEXT NOT NULL,
    PRIMARY KEY (root_id, generation, relative_path, issue_code),
    FOREIGN KEY (root_id, generation) REFERENCES catalog_scans(root_id, generation)
) STRICT;
