package library

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	_ "modernc.org/sqlite"
)

func TestScanHydratesDurationArtworkAndRejectsSymlinkReplacement(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "Album", "song.wav"), 8_000)
	writeTestCover(t, filepath.Join(root, "Album", "folder.jpg"))
	service := newTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	job, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	job = waitScan(t, service, job.ID)
	if job.Status != "complete" || job.Discovered != 1 || job.Processed != 1 || job.Added != 1 || job.Errors != 0 {
		t.Fatalf("scan = %+v", job)
	}
	page, err := service.Browse(t.Context(), Query{Kind: "tracks", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	tracks, ok := page.Items.([]Track)
	if !ok || len(tracks) != 1 {
		t.Fatalf("items = %#v", page.Items)
	}
	track := tracks[0]
	if track.Path != "Album/song.wav" || track.DurationMS != 1_000 || track.Size != 8_044 || track.ArtworkID == "" || !track.Available {
		t.Fatalf("track = %+v", track)
	}
	artwork, mime, err := service.Artwork(t.Context(), track.ArtworkID)
	if err != nil {
		t.Fatal(err)
	}
	artwork.Close()
	if mime != "image/jpeg" {
		t.Fatalf("artwork mime = %q", mime)
	}
	opened, _, err := service.Open(t.Context(), track.ID)
	if err != nil {
		t.Fatal(err)
	}
	opened.Close()
	unchanged, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unchanged = waitScan(t, service, unchanged.ID)
	if unchanged.Status != "complete" || unchanged.Discovered != 1 || unchanged.Processed != 1 || unchanged.Added != 0 || unchanged.Updated != 0 {
		t.Fatalf("incremental scan = %+v", unchanged)
	}

	outside := filepath.Join(t.TempDir(), "outside.wav")
	writeTestWAV(t, outside, 8_000)
	inside := filepath.Join(root, "Album", "song.wav")
	if err := os.Remove(inside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, inside); err != nil {
		t.Fatal(err)
	}
	if file, _, err := service.Open(t.Context(), track.ID); err == nil {
		file.Close()
		t.Fatal("Open accepted a symlink replacement")
	}
}

func TestFullScanBypassesUnchangedMetadataFastPath(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "song.wav")
	writeTestWAV(t, path, 4_000)
	service := newTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	initial, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, initial.ID); completed.Status != "complete" {
		t.Fatalf("initial scan = %+v", completed)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteAt([]byte("NOPE"), 0); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	incremental, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, incremental.ID); completed.Status != "complete" || completed.Updated != 0 || completed.Errors != 0 {
		t.Fatalf("incremental scan = %+v", completed)
	}
	full, err := service.StartScanMode(t.Context(), ScanModeFull)
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, full.ID); completed.Status != "failed" || completed.Errors != 1 {
		t.Fatalf("full scan = %+v", completed)
	}
}

func TestChangedConfiguredRootPathCannotReuseMatchingFileIdentity(t *testing.T) {
	firstRoot := t.TempDir()
	firstPath := filepath.Join(firstRoot, "song.wav")
	writeTestWAV(t, firstPath, 4_000)
	service := newTestService(t, []Root{{ID: "music", Name: "Music", Path: firstRoot}})
	initial, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, initial.ID); completed.Status != "complete" {
		t.Fatalf("initial scan = %+v", completed)
	}
	firstInfo, err := os.Stat(firstPath)
	if err != nil {
		t.Fatal(err)
	}

	secondRoot := t.TempDir()
	secondPath := filepath.Join(secondRoot, "song.wav")
	writeTestWAV(t, secondPath, 4_000)
	file, err := os.OpenFile(secondPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteAt([]byte("NOPE"), 0); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Chtimes(secondPath, firstInfo.ModTime(), firstInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err = service.SetRoots([]Root{{ID: "music", Name: "Music", Path: secondRoot}}); err != nil {
		t.Fatal(err)
	}
	scan, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if completed := waitScan(t, service, scan.ID); completed.Status != "failed" || completed.Errors != 1 || completed.Updated != 0 {
		t.Fatalf("changed-root scan = %+v", completed)
	}
}

func TestStartScanRejectsInvalidMode(t *testing.T) {
	service := newTestService(t, nil)
	if _, err := service.StartScanMode(t.Context(), ScanMode("invalid")); !isInvalidRequest(err) {
		t.Fatalf("invalid scan mode error = %#v", err)
	}
	jobs, err := service.Scans(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("invalid mode created scan jobs: %+v", jobs)
	}
}

func TestFailedRootScanPreservesExistingAvailability(t *testing.T) {
	root := t.TempDir()
	writeTestWAV(t, filepath.Join(root, "song.wav"), 4_000)
	service := newTestService(t, []Root{{ID: "music", Name: "Music", Path: root}})
	first, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first = waitScan(t, service, first.ID)
	if first.Status != "complete" {
		t.Fatalf("first scan = %+v", first)
	}
	page, err := service.Browse(t.Context(), Query{Kind: "tracks", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	track := page.Items.([]Track)[0]
	if err := os.Rename(root, root+"-offline"); err != nil {
		t.Fatal(err)
	}
	second, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second = waitScan(t, service, second.ID)
	if second.Status != "failed" || second.Errors == 0 || second.Unavailable != 0 {
		t.Fatalf("failed scan = %+v", second)
	}
	preserved, err := service.Track(t.Context(), track.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !preserved.Available {
		t.Fatal("failed root scan marked an existing track unavailable")
	}
	if err := os.Rename(root+"-offline", root); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "song.wav")); err != nil {
		t.Fatal(err)
	}
	third, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	third = waitScan(t, service, third.ID)
	if third.Status != "complete" || third.Unavailable != 1 {
		t.Fatalf("successful removal scan = %+v", third)
	}
	missing, err := service.Track(t.Context(), track.ID)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Available {
		t.Fatal("successful scan did not mark removed track unavailable")
	}
}

func TestPlaylistPreservesDuplicatesUnknownTracksAndRevisionConflicts(t *testing.T) {
	service := newTestService(t, nil)
	created, err := service.SavePlaylist(t.Context(), "", "Duplicates", []string{"missing", "missing", "other"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || len(created.TrackIDs) != 3 || created.TrackIDs[0] != created.TrackIDs[1] || len(created.Tracks) != 3 || created.Tracks[0].Available {
		t.Fatalf("created playlist = %+v", created)
	}
	updated, err := service.SavePlaylist(t.Context(), created.ID, "Renamed", []string{"other", "missing", "missing"}, created.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.TrackIDs[1] != updated.TrackIDs[2] {
		t.Fatalf("updated playlist = %+v", updated)
	}
	_, err = service.SavePlaylist(t.Context(), created.ID, "Stale", nil, created.Revision)
	var public *fault.Error
	if !errors.As(err, &public) || public.Status != 409 || public.Code != "REVISION_CONFLICT" {
		t.Fatalf("stale save error = %#v", err)
	}
}

func TestBrowseTracksRecursiveSubtreeBoundariesAndDirectDefault(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writeTestWAV(t, filepath.Join(firstRoot, "Selected%_", "direct.wav"), 4_000)
	writeTestWAV(t, filepath.Join(firstRoot, "Selected%_", "Child", "nested.wav"), 4_000)
	writeTestWAV(t, filepath.Join(firstRoot, "Selected%_", "Child", "Deep", "deep.wav"), 4_000)
	writeTestWAV(t, filepath.Join(firstRoot, "Selected%_Else", "prefix-sibling.wav"), 4_000)
	writeTestWAV(t, filepath.Join(secondRoot, "Selected%_", "other-root.wav"), 4_000)
	service := newTestService(t, []Root{
		{ID: "first", Name: "First", Path: firstRoot},
		{ID: "second", Name: "Second", Path: secondRoot},
	})
	job, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if job = waitScan(t, service, job.ID); job.Status != "complete" {
		t.Fatalf("scan = %+v", job)
	}

	direct, err := service.Browse(t.Context(), Query{Kind: "tracks", RootID: "first", Path: "Selected%_", Sort: "path", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	directTracks := direct.Items.([]Track)
	if direct.Total != 1 || len(directTracks) != 1 || directTracks[0].Path != "Selected%_/direct.wav" {
		t.Fatalf("direct folder page = %+v", direct)
	}

	recursive, err := service.Browse(t.Context(), Query{Kind: "tracks", RootID: "first", Path: "Selected%_", Recursive: true, Sort: "path", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	recursiveTracks := recursive.Items.([]Track)
	got := make([]string, len(recursiveTracks))
	for index, track := range recursiveTracks {
		got[index] = track.Path
		if track.RootID != "first" {
			t.Fatalf("recursive track escaped selected root: %+v", track)
		}
	}
	want := []string{"Selected%_/Child/Deep/deep.wav", "Selected%_/Child/nested.wav", "Selected%_/direct.wav"}
	if recursive.Total != len(want) || !slices.Equal(got, want) {
		t.Fatalf("recursive folder paths = %#v, total=%d, want %#v", got, recursive.Total, want)
	}
}

func TestBrowseTracksRecursiveRootFiltersAndPaginates(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	for _, path := range []string{"root.wav", "Nested/Deep/last.wav", "Nested/match.wav", "Nested/other.wav", "unavailable.wav"} {
		writeTestWAV(t, filepath.Join(firstRoot, filepath.FromSlash(path)), 4_000)
	}
	writeTestWAV(t, filepath.Join(secondRoot, "other-root.wav"), 4_000)
	service := newTestService(t, []Root{
		{ID: "first", Name: "First", Path: firstRoot},
		{ID: "second", Name: "Second", Path: secondRoot},
	})
	job, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if job = waitScan(t, service, job.ID); job.Status != "complete" {
		t.Fatalf("scan = %+v", job)
	}
	if _, err = service.db.ExecContext(t.Context(), `UPDATE library_tracks SET available=0 WHERE root_id=? AND relative_path=?`, "first", "unavailable.wav"); err != nil {
		t.Fatal(err)
	}

	page, err := service.Browse(t.Context(), Query{Kind: "tracks", RootID: "first", Recursive: true, Sort: "path", Offset: 1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	tracks := page.Items.([]Track)
	got := make([]string, len(tracks))
	for index, track := range tracks {
		got[index] = track.Path
	}
	want := []string{"Nested/match.wav", "Nested/other.wav"}
	if page.Total != 4 || page.Offset != 1 || page.Limit != 2 || !slices.Equal(got, want) {
		t.Fatalf("recursive root page = %+v, paths=%#v", page, got)
	}
	if _, err = service.SetLiked(t.Context(), tracks[0].ID, true); err != nil {
		t.Fatal(err)
	}
	filtered, err := service.Browse(t.Context(), Query{Kind: "tracks", RootID: "first", Recursive: true, Search: "match", Liked: true, Sort: "path", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	filteredTracks := filtered.Items.([]Track)
	if filtered.Total != 1 || len(filteredTracks) != 1 || filteredTracks[0].Path != "Nested/match.wav" {
		t.Fatalf("filtered recursive root page = %+v", filtered)
	}
}

func TestBrowseRejectsInvalidRecursiveQueries(t *testing.T) {
	service := newTestService(t, nil)
	for _, query := range []Query{
		{Kind: "tracks", Recursive: true, Limit: 10},
		{Kind: "albums", RootID: "music", Recursive: true, Limit: 10},
	} {
		if _, err := service.Browse(t.Context(), query); !isInvalidRequest(err) {
			t.Fatalf("recursive query %+v error = %#v", query, err)
		}
	}
}

func TestDownloadFolderSnapshotIsRecursiveOrderedAndRootConfined(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writeTestWAV(t, filepath.Join(firstRoot, "Chosen", "direct.wav"), 4_000)
	writeTestWAV(t, filepath.Join(firstRoot, "Chosen", "Child", "nested.wav"), 4_000)
	writeTestWAV(t, filepath.Join(firstRoot, "ChosenElse", "prefix-sibling.wav"), 4_000)
	writeTestWAV(t, filepath.Join(firstRoot, "Sibling", "outside.wav"), 4_000)
	writeTestWAV(t, filepath.Join(secondRoot, "Chosen", "other-root.wav"), 4_000)
	service := newTestService(t, []Root{
		{ID: "first", Name: "First", Path: firstRoot},
		{ID: "second", Name: "Second", Path: secondRoot},
	})
	job, err := service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if job = waitScan(t, service, job.ID); job.Status != "complete" {
		t.Fatalf("scan = %+v", job)
	}

	snapshot, err := service.DownloadSnapshot(t.Context(), "folder", "", "first", "Chosen")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Kind != "folder" || snapshot.Title != "Chosen" || len(snapshot.Tracks) != 2 {
		t.Fatalf("folder snapshot = %+v", snapshot)
	}
	paths := []string{snapshot.Tracks[0].Path, snapshot.Tracks[1].Path}
	if paths[0] != "Chosen/Child/nested.wav" || paths[1] != "Chosen/direct.wav" {
		t.Fatalf("folder snapshot paths = %#v", paths)
	}
	for _, track := range snapshot.Tracks {
		if track.RootID != "first" || !strings.HasPrefix(track.Path, "Chosen/") {
			t.Fatalf("folder snapshot escaped its root or sibling boundary: %+v", track)
		}
	}
	if _, err := service.DownloadSnapshot(t.Context(), "folder", "", "first", "../Chosen"); !isInvalidRequest(err) {
		t.Fatalf("folder traversal error = %#v", err)
	}
	if _, err := service.DownloadSnapshot(t.Context(), "folder", "", "first", "Empty"); err == nil {
		t.Fatal("empty folder snapshot succeeded")
	}
}

func TestPlayCountsRankGloballyAndSurviveRescan(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writeTestWAV(t, filepath.Join(firstRoot, "Alpha.wav"), 4_000)
	writeTestWAV(t, filepath.Join(firstRoot, "Beta.wav"), 4_000)
	writeTestWAV(t, filepath.Join(firstRoot, "Zero.wav"), 4_000)
	writeTestWAV(t, filepath.Join(secondRoot, "Gamma.wav"), 4_000)
	service := newTestService(t, []Root{
		{ID: "first", Name: "First", Path: firstRoot},
		{ID: "second", Name: "Second", Path: secondRoot},
	})
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
	byPath := make(map[string]Track)
	for _, track := range page.Items.([]Track) {
		byPath[track.Path] = track
	}
	alpha, beta, gamma, zero := byPath["Alpha.wav"], byPath["Beta.wav"], byPath["Gamma.wav"], byPath["Zero.wav"]
	if alpha.ID == "" || beta.ID == "" || gamma.ID == "" || zero.ID == "" {
		t.Fatalf("scanned tracks = %#v", byPath)
	}
	if zero.PlayCount != 0 {
		t.Fatalf("uncounted track play count = %d, want 0", zero.PlayCount)
	}
	for _, track := range []Track{alpha, beta, gamma} {
		if _, err = service.SetLiked(t.Context(), track.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, count := range []struct {
		track Track
		plays int64
	}{
		{track: alpha, plays: 5},
		{track: beta, plays: 5},
		{track: gamma, plays: 9},
	} {
		if _, err = service.db.ExecContext(t.Context(), `INSERT INTO track_play_counts(track_id,play_count,last_play_id) VALUES(?,?,?)`, count.track.ID, count.plays, "play-"+count.track.ID); err != nil {
			t.Fatal(err)
		}
	}

	ranked, err := service.Browse(t.Context(), Query{Kind: "tracks", Played: true, Sort: "most_played", Offset: 1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	rankedTracks := ranked.Items.([]Track)
	if ranked.Total != 3 || len(rankedTracks) != 2 || rankedTracks[0].ID != alpha.ID || rankedTracks[1].ID != beta.ID {
		t.Fatalf("ranked page = %+v", ranked)
	}
	filtered, err := service.Browse(t.Context(), Query{Kind: "tracks", Search: "beta", RootID: "first", Liked: true, Played: true, Sort: "most_played", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	filteredTracks := filtered.Items.([]Track)
	if filtered.Total != 1 || len(filteredTracks) != 1 || filteredTracks[0].ID != beta.ID {
		t.Fatalf("filtered ranking = %+v", filtered)
	}
	loaded, err := service.Track(t.Context(), alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	info, err := service.Info(t.Context(), alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	playlist, err := service.SavePlaylist(t.Context(), "", "Played", []string{alpha.ID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PlayCount != 5 || info.Track.PlayCount != 5 || len(playlist.Tracks) != 1 || playlist.Tracks[0].PlayCount != 5 {
		t.Fatalf("count exposure: track=%d info=%d playlist=%+v", loaded.PlayCount, info.Track.PlayCount, playlist.Tracks)
	}

	job, err = service.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if job = waitScan(t, service, job.ID); job.Status != "complete" {
		t.Fatalf("rescan = %+v", job)
	}
	loaded, err = service.Track(t.Context(), alpha.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.PlayCount != 5 {
		t.Fatalf("play count after rescan = %d, want 5", loaded.PlayCount)
	}
}

func newTestService(t *testing.T, roots []Root) *Service {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	service, err := New(t.Context(), db, roots, filepath.Join(t.TempDir(), "artwork"), nil)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func waitScan(t *testing.T, service *Service, id string) ScanJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := service.Scans(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.ID == id && job.Status != "queued" && job.Status != "running" {
				return job
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("scan did not finish")
	return ScanJob{}
}

func writeTestWAV(t *testing.T, path string, samples int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 44+samples))
	buffer.WriteString("RIFF")
	binary.Write(buffer, binary.LittleEndian, uint32(36+samples))
	buffer.WriteString("WAVEfmt ")
	binary.Write(buffer, binary.LittleEndian, uint32(16))
	binary.Write(buffer, binary.LittleEndian, uint16(1))
	binary.Write(buffer, binary.LittleEndian, uint16(1))
	binary.Write(buffer, binary.LittleEndian, uint32(8_000))
	binary.Write(buffer, binary.LittleEndian, uint32(8_000))
	binary.Write(buffer, binary.LittleEndian, uint16(1))
	binary.Write(buffer, binary.LittleEndian, uint16(8))
	buffer.WriteString("data")
	binary.Write(buffer, binary.LittleEndian, uint32(samples))
	buffer.Write(make([]byte, samples))
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestCover(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	picture := image.NewRGBA(image.Rect(0, 0, 2, 2))
	picture.Set(0, 0, color.RGBA{R: 200, G: 220, B: 180, A: 255})
	if err := jpeg.Encode(file, picture, nil); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
