package downloads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/database"
	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/library"
)

func TestOriginalJobPreservesBytesOwnerScopeAndPlaylistDuplicates(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "Album", "song.wav")
	writeDownloadTestWAV(t, sourcePath, 8_000)
	original, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog, err := library.New(t.Context(), db, []library.Root{{ID: "music", Name: "Music", Path: root}}, filepath.Join(directory, "artwork"), nil)
	if err != nil {
		t.Fatal(err)
	}
	scan, err := catalog.StartScan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	waitDownloadTestScan(t, catalog, scan.ID)
	page, err := catalog.Browse(t.Context(), library.Query{Kind: "tracks", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	tracks := page.Items.([]library.Track)
	playlist, err := catalog.SavePlaylist(t.Context(), "", "Repeated", []string{tracks[0].ID, tracks[0].ID}, 0)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)

	created, err := service.Create(t.Context(), "owner-a", Request{Kind: "playlist", ID: playlist.ID, Quality: QualityOriginal})
	if err != nil {
		t.Fatal(err)
	}
	manifest := waitDownloadManifest(t, service, "owner-a", created.ID)
	if manifest.Status != "ready" || len(manifest.Tracks) != 2 || manifest.Tracks[0].TrackID != manifest.Tracks[1].TrackID {
		body, _ := json.Marshal(manifest)
		t.Fatalf("manifest = %s", body)
	}
	if manifest.Tracks[0].SHA256 == "" || manifest.Tracks[0].SHA256 != manifest.Tracks[1].SHA256 || manifest.Tracks[0].MediaPath == manifest.Tracks[1].MediaPath {
		t.Fatalf("duplicate artifacts = %+v", manifest.Tracks)
	}
	artifact, err := service.Artifact(t.Context(), "owner-a", created.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := io.ReadAll(artifact.File)
	closeErr := artifact.File.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read artifact error=%v close=%v", err, closeErr)
	}
	digest := sha256.Sum256(original)
	if !bytes.Equal(prepared, original) || artifact.Size != int64(len(original)) || artifact.SHA256 != hex.EncodeToString(digest[:]) {
		t.Fatal("original artifact bytes, length, or checksum changed")
	}
	current, err := os.ReadFile(sourcePath)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatal("library source was modified")
	}
	if _, err := service.Manifest(t.Context(), "owner-b", created.ID); !isDownloadNotFound(err) {
		t.Fatalf("other owner manifest error = %#v", err)
	}
	if _, err := service.Artifact(t.Context(), "owner-b", created.ID, 0); !isDownloadNotFound(err) {
		t.Fatalf("other owner artifact error = %#v", err)
	}
}
func TestArtifactNamesAllowHexDigitsAndRejectPathEscape(t *testing.T) {
	directory := t.TempDir()
	info, err := os.Lstat(directory)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{directory: directory, directoryInfo: info}
	id := strings.Repeat("a", 32)
	if _, err := service.artifactPath(id, "original-"+strings.Repeat("0", 64)+".wav"); err != nil {
		t.Fatalf("valid zero-containing artifact name rejected: %v", err)
	}
	for _, name := range []string{"../outside.wav", `sub\outside.wav`, "file\x00.wav"} {
		if _, err := service.artifactPath(id, name); err == nil {
			t.Fatalf("unsafe artifact name accepted: %q", name)
		}
	}
}

func TestCancelFencesPreparationAndReleasesArtifacts(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog := &blockingCatalog{opened: make(chan struct{})}
	service, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	created, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "track", Quality: QualityOriginal})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-catalog.opened:
	case <-time.After(2 * time.Second):
		t.Fatal("preparation did not begin")
	}
	if err := service.Cancel(t.Context(), "owner", created.ID); err != nil {
		t.Fatal(err)
	}
	manifest, err := service.Manifest(t.Context(), "owner", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "cancelled" {
		t.Fatalf("status = %q", manifest.Status)
	}
	if _, err := service.Artifact(t.Context(), "owner", created.ID, 0); err == nil {
		t.Fatal("cancelled artifact remained available")
	}
	jobDirectory, err := service.jobDirectory(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jobDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled artifact directory stat error = %v", err)
	}
}

func TestInterruptedPreparationIsPersistedAsFailedOnRestart(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog := &blockingCatalog{opened: make(chan struct{})}
	first, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	created, err := first.Create(t.Context(), "owner", Request{Kind: "track", ID: "track", Quality: QualityOriginal})
	if err != nil {
		first.Close()
		t.Fatal(err)
	}
	select {
	case <-catalog.opened:
	case <-time.After(2 * time.Second):
		first.Close()
		t.Fatal("preparation did not begin")
	}
	first.Close()

	second, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(second.Close)
	manifest, err := second.Manifest(t.Context(), "owner", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "failed" || manifest.Tracks[0].Error == nil || manifest.Tracks[0].Error.Code != "JOB_INTERRUPTED" {
		t.Fatalf("recovered manifest = %+v", manifest)
	}
}

func TestChangedSourceVersionFailsWithoutPublishingArtifact(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog := changedCatalog{}
	service, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	created, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "track", Quality: QualityOriginal})
	if err != nil {
		t.Fatal(err)
	}
	manifest := waitDownloadManifest(t, service, "owner", created.ID)
	if manifest.Status != "failed" || len(manifest.Tracks) != 1 || manifest.Tracks[0].Error == nil || manifest.Tracks[0].Error.Code != "SOURCE_CHANGED" {
		t.Fatalf("manifest = %+v", manifest)
	}
	if _, err := service.Artifact(t.Context(), "owner", created.ID, 0); err == nil {
		t.Fatal("changed source published an artifact")
	}
}

func TestConcurrentOwnerAdmissionIsAtomic(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog := &blockingCatalog{opened: make(chan struct{})}
	service, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)

	const attempts = maximumActiveJobsPerOwner * 2
	start := make(chan struct{})
	results := make(chan struct {
		manifest Manifest
		err      error
	}, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			manifest, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "track", Quality: QualityOriginal})
			results <- struct {
				manifest Manifest
				err      error
			}{manifest: manifest, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	accepted := make([]string, 0, maximumActiveJobsPerOwner)
	rejected := 0
	for result := range results {
		if result.err != nil {
			var public *fault.Error
			if !errors.As(result.err, &public) || public.Code != "DOWNLOAD_BUSY" {
				t.Fatalf("admission error = %#v", result.err)
			}
			rejected++
			continue
		}
		accepted = append(accepted, result.manifest.ID)
	}
	if len(accepted) != maximumActiveJobsPerOwner || rejected != attempts-maximumActiveJobsPerOwner {
		t.Fatalf("accepted=%d rejected=%d", len(accepted), rejected)
	}
	for _, id := range accepted {
		if err := service.Cancel(t.Context(), "owner", id); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentRetainedAdmissionIsAtomic(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(t.Context(), db, unavailableCatalog{}, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)

	const attempts = maximumRetainedJobsPerOwner + 32
	start := make(chan struct{})
	results := make(chan error, attempts)
	var group sync.WaitGroup
	for range attempts {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "missing", Quality: QualityOriginal})
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
			continue
		}
		var public *fault.Error
		if !errors.As(err, &public) || public.Code != "DOWNLOAD_BUSY" {
			t.Fatalf("admission error = %#v", err)
		}
	}
	if accepted != maximumRetainedJobsPerOwner {
		t.Fatalf("accepted=%d, want %d", accepted, maximumRetainedJobsPerOwner)
	}
}

func TestConcurrentGlobalAdmissionIsAtomic(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog := &blockingCatalog{opened: make(chan struct{})}
	service, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)

	const attempts = maximumQueuedJobs + 32
	start := make(chan struct{})
	results := make(chan error, attempts)
	var group sync.WaitGroup
	for index := range attempts {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			_, err := service.Create(t.Context(), fmt.Sprintf("owner-%d", index), Request{Kind: "track", ID: "track", Quality: QualityOriginal})
			results <- err
		}(index)
	}
	close(start)
	group.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
			continue
		}
		var public *fault.Error
		if !errors.As(err, &public) || public.Code != "DOWNLOAD_BUSY" {
			t.Fatalf("admission error = %#v", err)
		}
	}
	if accepted != maximumQueuedJobs {
		t.Fatalf("accepted=%d, want %d", accepted, maximumQueuedJobs)
	}
}

func TestExpiredPreparationTerminatesWithRetentionWindow(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog := &blockingCatalog{opened: make(chan struct{})}
	service, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	created, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "track", Quality: QualityOriginal})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-catalog.opened:
	case <-time.After(2 * time.Second):
		t.Fatal("preparation did not begin")
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE download_jobs SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), created.ID); err != nil {
		t.Fatal(err)
	}
	manifest, err := service.Manifest(t.Context(), "owner", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "failed" || manifest.Tracks[0].Error == nil || manifest.Tracks[0].Error.Code != "JOB_EXPIRED" {
		t.Fatalf("expired manifest = %+v", manifest)
	}
	retainedUntil, err := time.Parse(time.RFC3339Nano, manifest.ExpiresAt)
	if err != nil || !retainedUntil.After(time.Now()) {
		t.Fatalf("retention expiry = %q, error = %v", manifest.ExpiresAt, err)
	}
}

func TestQueuedCancellationPreventsLateWorkerStart(t *testing.T) {
	directory := t.TempDir()
	db, err := database.Open(filepath.Join(directory, "server.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalog := &routingBlockingCatalog{opened: make(chan string, 8)}
	service, err := New(t.Context(), db, catalog, filepath.Join(directory, "downloads"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	first, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "first", Quality: QualityOriginal})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "second", Quality: QualityOriginal})
	if err != nil {
		t.Fatal(err)
	}
	started := map[string]bool{}
	for len(started) < maximumConcurrentPrepares {
		select {
		case id := <-catalog.opened:
			started[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not become occupied")
		}
	}
	queued, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "queued", Quality: QualityOriginal})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Cancel(t.Context(), "owner", queued.ID); err != nil {
		t.Fatal(err)
	}
	marker, err := service.Create(t.Context(), "owner", Request{Kind: "track", ID: "marker", Quality: QualityOriginal})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Cancel(t.Context(), "owner", first.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Cancel(t.Context(), "owner", second.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-catalog.opened:
		if id == "queued" {
			t.Fatal("cancelled queued job began source preparation")
		}
		if id != "marker" {
			t.Fatalf("unexpected preparation %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not advance past cancelled queued job")
	}
	if err := service.Cancel(t.Context(), "owner", marker.ID); err != nil {
		t.Fatal(err)
	}
}

type unavailableCatalog struct{}

func (unavailableCatalog) DownloadSnapshot(context.Context, string, string) (library.DownloadSnapshot, error) {
	return library.DownloadSnapshot{Kind: "track", ID: "missing", Title: "Missing", Tracks: []library.Track{{ID: "missing", Title: "Missing"}}}, nil
}

func (unavailableCatalog) Open(context.Context, string) (*os.File, library.Track, error) {
	return nil, library.Track{}, errors.New("not used")
}

func (unavailableCatalog) Info(context.Context, string) (library.TrackInfo, error) {
	return library.TrackInfo{}, errors.New("not used")
}

type routingBlockingCatalog struct {
	opened chan string
}

func (catalog *routingBlockingCatalog) DownloadSnapshot(_ context.Context, _, id string) (library.DownloadSnapshot, error) {
	return library.DownloadSnapshot{Kind: "track", ID: id, Title: id, Tracks: []library.Track{{ID: id, Title: id, Available: true, Size: 4, ModifiedAt: "2026-01-01T00:00:00Z", Format: "wav", Mime: "audio/wav"}}}, nil
}

func (catalog *routingBlockingCatalog) Open(ctx context.Context, id string) (*os.File, library.Track, error) {

	catalog.opened <- id
	<-ctx.Done()
	return nil, library.Track{}, context.Cause(ctx)
}

func (catalog *routingBlockingCatalog) Info(context.Context, string) (library.TrackInfo, error) {
	return library.TrackInfo{}, errors.New("not used")
}

type blockingCatalog struct {
	opened chan struct{}
	once   sync.Once
}

func (catalog *blockingCatalog) DownloadSnapshot(context.Context, string, string) (library.DownloadSnapshot, error) {
	return library.DownloadSnapshot{Kind: "track", ID: "track", Title: "Track", Tracks: []library.Track{{ID: "track", Title: "Track", Available: true, Size: 4, ModifiedAt: "2026-01-01T00:00:00Z", Format: "wav", Mime: "audio/wav"}}}, nil
}

func (catalog *blockingCatalog) Open(ctx context.Context, _ string) (*os.File, library.Track, error) {
	catalog.once.Do(func() { close(catalog.opened) })
	<-ctx.Done()
	return nil, library.Track{}, context.Cause(ctx)
}

func (catalog *blockingCatalog) Info(context.Context, string) (library.TrackInfo, error) {
	return library.TrackInfo{}, errors.New("not used")
}

type changedCatalog struct{}

func (changedCatalog) DownloadSnapshot(context.Context, string, string) (library.DownloadSnapshot, error) {
	return library.DownloadSnapshot{Kind: "track", ID: "track", Title: "Track", Tracks: []library.Track{{ID: "track", Title: "Track", Available: true, Size: 4, ModifiedAt: "2026-01-01T00:00:00Z", Format: "wav", Mime: "audio/wav"}}}, nil
}

func (changedCatalog) Open(context.Context, string) (*os.File, library.Track, error) {
	file, err := os.CreateTemp("", "changed-source-*")
	if err != nil {
		return nil, library.Track{}, err
	}
	_ = os.Remove(file.Name())
	_, _ = file.Write([]byte("data"))
	_, _ = file.Seek(0, 0)
	return file, library.Track{ID: "track", Title: "Track", Available: true, Size: 4, ModifiedAt: "2026-01-02T00:00:00Z", Format: "wav", Mime: "audio/wav"}, nil
}

func (changedCatalog) Info(context.Context, string) (library.TrackInfo, error) {
	return library.TrackInfo{}, errors.New("not used")
}

func waitDownloadManifest(t *testing.T, service *Service, owner, id string) Manifest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		manifest, err := service.Manifest(t.Context(), owner, id)
		if err != nil {
			t.Fatal(err)
		}
		if manifest.Status != "preparing" {
			return manifest
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("download did not finish")
	return Manifest{}
}

func waitDownloadTestScan(t *testing.T, service *library.Service, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := service.Scans(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range jobs {
			if job.ID == id && job.Status == "complete" {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("scan did not finish")
}

func writeDownloadTestWAV(t *testing.T, path string, samples int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	buffer := bytes.NewBuffer(make([]byte, 0, 44+samples))
	buffer.WriteString("RIFF")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(36+samples))
	buffer.WriteString("WAVEfmt ")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(16))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(8_000))
	_ = binary.Write(buffer, binary.LittleEndian, uint32(8_000))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(1))
	_ = binary.Write(buffer, binary.LittleEndian, uint16(8))
	buffer.WriteString("data")
	_ = binary.Write(buffer, binary.LittleEndian, uint32(samples))
	buffer.Write(make([]byte, samples))
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func isDownloadNotFound(err error) bool {
	var public *fault.Error
	return errors.As(err, &public) && public.Status == 404
}
