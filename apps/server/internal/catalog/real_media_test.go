package catalog

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestScanParsesRealContainers_when_all_mandatory_codecs_present(t *testing.T) {
	// Given
	root := decodeRealFixtures(t)
	before := fileDigests(t, root)
	scanner, err := NewScanner(root)
	assertNoError(t, err)

	// When
	result, err := scanner.Scan(context.Background(), EmptySnapshot())

	// Then
	assertNoError(t, err)
	if availableCount(result.Snapshot) != 5 {
		t.Fatalf("available tracks = %d, want 5; issues=%+v", availableCount(result.Snapshot), result.Issues)
	}
	formats := make(map[Format]bool)
	for _, track := range result.Snapshot.Tracks {
		formats[track.Format] = true
		if track.Metadata.Title != "Fixture" || track.Metadata.Album != "Release" {
			t.Fatalf("metadata = %+v, want normalized real tags", track.Metadata)
		}
		if track.Format != FormatPCMWAV && (track.Metadata.RecordingID != "recording-real" || track.Metadata.ReleaseID != "release-real") {
			t.Fatalf("format=%q recording=%q release=%q, want MusicBrainz IDs", track.Format, track.Metadata.RecordingID, track.Metadata.ReleaseID)
		}
	}
	for _, format := range []Format{FormatFLAC, FormatMP3, FormatOggVorbis, FormatOpus, FormatPCMWAV} {
		if !formats[format] {
			t.Errorf("real format %q was not recognized", format)
		}
	}
	if after := fileDigests(t, root); !equalDigests(before, after) {
		t.Fatalf("read-only scan mutated media: before=%v after=%v", before, after)
	}
}

func TestScanSkipsStableReads_when_file_versions_are_unchanged(t *testing.T) {
	// Given
	root := decodeRealFixtures(t)
	reader := &countingReader{delegate: osSnapshotReader{}}
	scanner, err := NewScannerWithReader(root, reader)
	assertNoError(t, err)
	first, err := scanner.Scan(context.Background(), EmptySnapshot())
	assertNoError(t, err)
	reader.calls = 0

	// When
	second, err := scanner.Scan(context.Background(), first.Snapshot)

	// Then
	assertNoError(t, err)
	if reader.calls != 0 || len(second.AnalysisJobs) != 0 {
		t.Fatalf("stable scan reads=%d jobs=%d, want zero", reader.calls, len(second.AnalysisJobs))
	}
}

func TestTagOnlyChangePreservesRecordingAndAnalysis_when_audio_is_unchanged(t *testing.T) {
	// Given
	root := t.TempDir()
	data := decodeFixture(t, "real.flac.b64")
	path := filepath.Join(root, "track.flac")
	assertNoError(t, os.WriteFile(path, data, 0o600))
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	first, err := scanner.Scan(context.Background(), EmptySnapshot())
	assertNoError(t, err)
	before := onlyTrack(t, first.Snapshot)
	changed := strings.Replace(string(data), "Fixture", "Changed", 1)
	assertNoError(t, os.WriteFile(path, []byte(changed), 0o600))

	// When
	second, err := scanner.Scan(context.Background(), first.Snapshot)

	// Then
	assertNoError(t, err)
	after := onlyTrack(t, second.Snapshot)
	if after.Metadata.Title != "Changed" {
		t.Fatalf("title = %q, want Changed", after.Metadata.Title)
	}
	if after.RecordingID != before.RecordingID {
		t.Fatalf("recording changed after tag-only edit: %q -> %q", before.RecordingID, after.RecordingID)
	}
	if len(second.AnalysisJobs) != 0 {
		t.Fatalf("tag-only edit queued %d analysis jobs, want zero", len(second.AnalysisJobs))
	}
}

func TestScanRejectsMalformedTag_when_container_signature_is_valid(t *testing.T) {
	// Given
	root := t.TempDir()
	assertNoError(t, os.WriteFile(filepath.Join(root, "broken.mp3"), []byte("ID3\x04\x00\x00\x7f\x7f\x7f\x7f"), 0o600))
	scanner, err := NewScanner(root)
	assertNoError(t, err)

	// When
	result, err := scanner.Scan(context.Background(), EmptySnapshot())

	// Then
	assertNoError(t, err)
	if availableCount(result.Snapshot) != 0 || !hasIssue(result.Issues, IssueMalformed) {
		t.Fatalf("malformed tag result = %+v", result)
	}
}

func TestMalformedReplacementPreservesAnchorAsUnavailable_when_track_exists(t *testing.T) {
	// Given
	root := t.TempDir()
	path := filepath.Join(root, "track.flac")
	writeRealFixture(t, "real.flac", path)
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	first, err := scanner.Scan(context.Background(), EmptySnapshot())
	assertNoError(t, err)
	before := onlyTrack(t, first.Snapshot)
	assertNoError(t, os.WriteFile(path, []byte("fLaC"), 0o600))

	// When
	second, err := scanner.Scan(context.Background(), first.Snapshot)

	// Then
	assertNoError(t, err)
	after := second.Snapshot.Tracks[before.TrackID]
	if after.Available || after.AlbumID != before.AlbumID || after.Order != before.Order {
		t.Fatalf("malformed replacement lost unavailable anchor: before=%+v after=%+v", before, after)
	}
	if !hasIssue(second.Issues, IssueMalformed) {
		t.Fatalf("issues = %+v, want malformed", second.Issues)
	}
}

func TestScannerIsRaceSafe_when_incremental_scans_run_concurrently(t *testing.T) {
	// Given
	root := decodeRealFixtures(t)
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	initial, err := scanner.Scan(context.Background(), EmptySnapshot())
	assertNoError(t, err)
	const workers = 8
	start := make(chan struct{})
	results := make(chan ScanResult, workers)
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			<-start
			result, scanErr := scanner.Scan(context.Background(), initial.Snapshot)
			results <- result
			errors <- scanErr
		})
	}

	// When
	close(start)
	group.Wait()
	close(results)
	close(errors)

	// Then
	for scanErr := range errors {
		assertNoError(t, scanErr)
	}
	var baseline *ScanResult
	for result := range results {
		if baseline == nil {
			copy := result
			baseline = &copy
			continue
		}
		if result.Snapshot.Generation != baseline.Snapshot.Generation ||
			result.Snapshot.Revision != baseline.Snapshot.Revision ||
			len(result.Snapshot.Tracks) != len(baseline.Snapshot.Tracks) ||
			len(result.AnalysisJobs) != len(baseline.AnalysisJobs) ||
			len(result.Issues) != len(baseline.Issues) ||
			result.Complete != baseline.Complete {
			t.Fatalf("concurrent result differs: baseline=%+v result=%+v", *baseline, result)
		}
		for id, track := range baseline.Snapshot.Tracks {
			if !reflect.DeepEqual(result.Snapshot.Tracks[id], track) {
				t.Fatalf("concurrent track %q differs: baseline=%+v result=%+v", id, track, result.Snapshot.Tracks[id])
			}
		}
	}
	if baseline == nil {
		t.Fatal("no concurrent scan result")
	}
	for id := range baseline.Snapshot.Tracks {
		delete(baseline.Snapshot.Tracks, id)
		break
	}
	if len(initial.Snapshot.Tracks) != 5 {
		t.Fatalf("result map aliases input snapshot: initial tracks=%d", len(initial.Snapshot.Tracks))
	}
}

type countingReader struct {
	delegate SnapshotReader
	calls    int
}

func (r *countingReader) ReadStable(path string) (MediaSnapshot, FileVersion, FileVersion, error) {
	r.calls++
	return r.delegate.ReadStable(path)
}

func decodeRealFixtures(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"real.flac", "real.mp3", "real.ogg", "real.opus", "real.wav"} {
		assertNoError(t, os.WriteFile(filepath.Join(root, name), decodeFixture(t, name+".b64"), 0o600))
	}
	return root
}

func writeRealFixture(t *testing.T, name, destination string) {
	t.Helper()
	assertNoError(t, os.WriteFile(destination, decodeFixture(t, name+".b64"), 0o600))
}

func decodeFixture(t *testing.T, name string) []byte {
	t.Helper()
	root := os.Getenv("JSTREAMER_FIXTURES")
	if root == "" {
		root = filepath.Join("..", "..", "..", "..", "tooling", "fixtures", "music")
	} else if !filepath.IsAbs(root) {
		root = filepath.Join("..", "..", root)
	}
	encoded, err := os.ReadFile(filepath.Join(root, name))
	assertNoError(t, err)
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	assertNoError(t, err)
	return data
}

func fileDigests(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	entries, err := os.ReadDir(root)
	assertNoError(t, err)
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(root, entry.Name()))
		assertNoError(t, err)
		result[entry.Name()] = hashID(string(data))
	}
	return result
}

func equalDigests(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}
