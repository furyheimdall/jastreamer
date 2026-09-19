package library

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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

func TestPlaylistFromLikesUsesEveryAvailableLikedTrackAndPreservesSnapshot(t *testing.T) {
	service := newTestService(t, nil)
	if _, err := service.PlaylistFromLikes(t.Context(), "Empty"); !isInvalidRequest(err) {
		t.Fatalf("empty likes error = %#v", err)
	}
	if _, err := service.PlaylistFromLikes(t.Context(), strings.Repeat("x", 201)); !isInvalidRequest(err) {
		t.Fatalf("long name error = %#v", err)
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

	firstPage, err := service.Browse(t.Context(), Query{Kind: "tracks", Liked: true, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if firstPage.Total != likedCount || len(firstPage.Items.([]Track)) != 500 {
		t.Fatalf("liked first page total=%d items=%d", firstPage.Total, len(firstPage.Items.([]Track)))
	}
	playlist, err := service.PlaylistFromLikes(t.Context(), "All likes")
	if err != nil {
		t.Fatal(err)
	}
	if len(playlist.TrackIDs) != likedCount || len(playlist.Tracks) != likedCount {
		t.Fatalf("playlist tracks=%d hydrated=%d", len(playlist.TrackIDs), len(playlist.Tracks))
	}
	seen := make(map[string]bool, likedCount)
	for index, id := range playlist.TrackIDs {
		track := playlist.Tracks[index]
		if !expected[id] || seen[id] || track.ID != id || !track.Available || !track.Liked {
			t.Fatalf("unexpected playlist item %d: id=%q track=%+v", index, id, track)
		}
		seen[id] = true
	}
	if len(seen) != len(expected) {
		t.Fatalf("playlist membership=%d, want %d", len(seen), len(expected))
	}

	removedID := playlist.TrackIDs[0]
	if _, err = service.SetLiked(t.Context(), removedID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetLiked(t.Context(), fmt.Sprintf("track-%04d", likedCount+1), true); err != nil {
		t.Fatal(err)
	}
	saved, err := service.Playlist(t.Context(), playlist.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.TrackIDs) != len(playlist.TrackIDs) {
		t.Fatalf("saved snapshot length=%d, want %d", len(saved.TrackIDs), len(playlist.TrackIDs))
	}
	for index := range saved.TrackIDs {
		if saved.TrackIDs[index] != playlist.TrackIDs[index] {
			t.Fatalf("saved snapshot changed at %d: %q != %q", index, saved.TrackIDs[index], playlist.TrackIDs[index])
		}
	}
}

func isInvalidRequest(err error) bool {
	var public *fault.Error
	return errors.As(err, &public) && public.Status == 400 && public.Code == "INVALID_REQUEST"
}
