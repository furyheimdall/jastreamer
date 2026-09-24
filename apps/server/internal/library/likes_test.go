package library

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	_ "modernc.org/sqlite"
)

func TestLikesPersistAcrossIdempotentUpdatesRescanAndRestart(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	databasePath := filepath.Join(t.TempDir(), "library.sqlite")
	cachePath := filepath.Join(t.TempDir(), "artwork")
	roots := []Root{{ID: "music", Name: "Music", Path: root}}

	open := func() (*sql.DB, *Service) {
		db, err := sql.Open("sqlite", databasePath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		service, err := New(t.Context(), db, roots, cachePath, nil)
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		return db, service
	}

	firstDB, service := open()
	job, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if job = waitScan(t, service, job.ID); job.Status != "complete" {
		t.Fatalf("scan = %+v", job)
	}
	page, err := service.Browse(t.Context(), Query{Kind: "tracks", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	track := page.Items.([]Track)[0]
	if track.Liked {
		t.Fatal("newly scanned track is liked")
	}
	for range 2 {
		track, err = service.SetLiked(t.Context(), track.ID, true)
		if err != nil || !track.Liked {
			t.Fatalf("idempotent like = %+v, %v", track, err)
		}
	}
	writeTestWAV(t, filepath.Join(root, "song.wav"), 8_000)
	job, err = service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if job = waitScan(t, service, job.ID); job.Status != "complete" || job.Updated != 1 {
		t.Fatalf("metadata-changing rescan = %+v", job)
	}
	if track, err = service.Track(t.Context(), track.ID); err != nil || !track.Liked {
		t.Fatalf("liked track after rescan = %+v, %v", track, err)
	}
	if err := firstDB.Close(); err != nil {
		t.Fatal(err)
	}

	secondDB, restarted := open()
	t.Cleanup(func() { secondDB.Close() })
	if track, err = restarted.Track(t.Context(), track.ID); err != nil || !track.Liked {
		t.Fatalf("liked track after restart = %+v, %v", track, err)
	}
	for range 2 {
		track, err = restarted.SetLiked(t.Context(), track.ID, false)
		if err != nil || track.Liked {
			t.Fatalf("idempotent unlike = %+v, %v", track, err)
		}
	}
	likedPage, err := restarted.Browse(t.Context(), Query{Kind: "tracks", Liked: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if likedPage.Total != 0 || len(likedPage.Items.([]Track)) != 0 {
		t.Fatalf("liked page after unlike = %#v", likedPage)
	}
}

func TestShuffledLikedTrackIDsSnapshotsEveryAvailableLikeWithoutPersistence(t *testing.T) {
	service := newTestService(t, nil)
	if trackIDs, err := service.ShuffledLikedTrackIDs(t.Context()); !isInvalidRequest(err) || trackIDs != nil {
		t.Fatalf("empty likes result=%v error=%#v", trackIDs, err)
	}

	tx, err := service.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	const likedCount = 503
	expected := make(map[string]bool, likedCount)
	for index := range likedCount + 2 {
		id := fmt.Sprintf("track-%04d", index)
		available := 1
		liked := index < likedCount
		if index == likedCount {
			liked = true
			available = 0
		}
		if _, err = tx.ExecContext(t.Context(), `INSERT INTO library_tracks(id,root_id,relative_path,title,artist,album,album_artist,album_id,disc,track_number,genres_json,duration_ms,format,mime,artwork_id,byte_size,modified_ns,modified_at,available,last_seen_scan) VALUES(?,?,?,?,?,?,?,?,0,0,'[]',1000,'wav','audio/wav','',100,0,'2026-01-01T00:00:00Z',?,'scan')`, id, "root", id+".wav", id, "artist", "album", "artist", "album", available); err != nil {
			t.Fatal(err)
		}
		if liked {
			if _, err = tx.ExecContext(t.Context(), `INSERT INTO library_track_likes(track_id,updated_at) VALUES(?,?)`, id, "2026-01-01T00:00:00Z"); err != nil {
				t.Fatal(err)
			}
		}
		if liked && available == 1 {
			expected[id] = true
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	saved, err := service.SavePlaylist(t.Context(), "", "Existing", []string{"track-0000", "track-0000"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	firstPage, err := service.Browse(t.Context(), Query{Kind: "tracks", Liked: true, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if firstPage.Total != likedCount || len(firstPage.Items.([]Track)) != 500 {
		t.Fatalf("liked first page total=%d items=%d", firstPage.Total, len(firstPage.Items.([]Track)))
	}
	snapshot, err := service.ShuffledLikedTrackIDs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != likedCount {
		t.Fatalf("snapshot tracks=%d, want %d", len(snapshot), likedCount)
	}
	seen := make(map[string]bool, likedCount)
	for index, id := range snapshot {
		if !expected[id] || seen[id] {
			t.Fatalf("unexpected snapshot item %d: %q", index, id)
		}
		seen[id] = true
	}
	if len(seen) != len(expected) {
		t.Fatalf("snapshot membership=%d, want %d", len(seen), len(expected))
	}

	removedID := snapshot[0]
	addedID := fmt.Sprintf("track-%04d", likedCount+1)
	if _, err = service.SetLiked(t.Context(), removedID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetLiked(t.Context(), addedID, true); err != nil {
		t.Fatal(err)
	}
	reloaded, err := service.ShuffledLikedTrackIDs(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	current := make(map[string]bool, len(reloaded))
	for _, id := range reloaded {
		current[id] = true
	}
	if !seen[removedID] || seen[addedID] || current[removedID] || !current[addedID] || len(current) != likedCount {
		t.Fatalf("snapshot changed or reload was stale: removed=%q added=%q snapshot=%v current=%v", removedID, addedID, seen, current)
	}
	playlists, err := service.Playlists(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(playlists) != 1 || playlists[0].ID != saved.ID || len(playlists[0].TrackIDs) != 2 || playlists[0].TrackIDs[0] != "track-0000" || playlists[0].TrackIDs[1] != "track-0000" {
		t.Fatalf("liked snapshots changed saved playlists: %#v", playlists)
	}
}

func TestShuffledLikedTrackIDsRejectsOverflowWithoutPartialResult(t *testing.T) {
	service := newTestService(t, nil)
	const insertTracks = `WITH digits(d) AS (VALUES(0),(1),(2),(3),(4),(5),(6),(7),(8),(9)),
numbers(n) AS (SELECT ones.d + 10*tens.d + 100*hundreds.d + 1000*thousands.d FROM digits ones CROSS JOIN digits tens CROSS JOIN digits hundreds CROSS JOIN digits thousands UNION ALL SELECT 10000)
INSERT INTO library_tracks(id,root_id,relative_path,title,artist,album,album_artist,album_id,disc,track_number,genres_json,duration_ms,format,mime,artwork_id,byte_size,modified_ns,modified_at,available,last_seen_scan)
SELECT printf('track-%05d',n),'root',printf('track-%05d.wav',n),printf('track-%05d',n),'artist','album','artist','album',0,0,'[]',1000,'wav','audio/wav','',100,0,'2026-01-01T00:00:00Z',1,'scan' FROM numbers`
	if _, err := service.db.ExecContext(t.Context(), insertTracks); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.ExecContext(t.Context(), `INSERT INTO library_track_likes(track_id,updated_at) SELECT id,'2026-01-01T00:00:00Z' FROM library_tracks`); err != nil {
		t.Fatal(err)
	}
	trackIDs, err := service.ShuffledLikedTrackIDs(t.Context())
	if !isInvalidRequest(err) || trackIDs != nil {
		t.Fatalf("overflow result=%v error=%#v", trackIDs, err)
	}
	playlists, listErr := service.Playlists(t.Context())
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(playlists) != 0 {
		t.Fatalf("overflow persisted playlists: %#v", playlists)
	}
}

func isInvalidRequest(err error) bool {
	var public *fault.Error
	return errors.As(err, &public) && public.Status == 400 && public.Code == "INVALID_REQUEST"
}
