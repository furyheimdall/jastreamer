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
