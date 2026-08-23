package catalog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIncrementalIndexStable_when_files_are_unchanged(t *testing.T) {
	// Given
	root := copyFixtureTree(t)
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	first, err := scanner.Scan(context.Background(), EmptySnapshot())
	assertNoError(t, err)
	if len(first.AnalysisJobs) != 5 {
		t.Fatalf("first analysis jobs = %d, want 5", len(first.AnalysisJobs))
	}
	// When
	second, err := scanner.Scan(context.Background(), first.Snapshot)
	// Then
	assertNoError(t, err)
	if len(second.AnalysisJobs) != 0 {
		t.Fatalf("stable analysis jobs = %d, want 0", len(second.AnalysisJobs))
	}
	if second.Snapshot.Revision != first.Snapshot.Revision {
		t.Fatalf("stable revision changed: %d -> %d", first.Snapshot.Revision, second.Snapshot.Revision)
	}
}

func TestRenamedFilePreservesIdentity_when_path_changes(t *testing.T) {
	// Given
	root := t.TempDir()
	writeRealFixture(t, "real.flac", filepath.Join(root, "old.flac"))
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	first, err := scanner.Scan(context.Background(), EmptySnapshot())
	assertNoError(t, err)
	before := onlyTrack(t, first.Snapshot)
	assertNoError(t, os.Rename(filepath.Join(root, "old.flac"), filepath.Join(root, "new.flac")))
	// When
	second, err := scanner.Scan(context.Background(), first.Snapshot)
	// Then
	assertNoError(t, err)
	after := onlyTrack(t, second.Snapshot)
	if after.RecordingID != before.RecordingID || after.TrackID != before.TrackID || after.FileID != before.FileID {
		t.Fatalf("rename changed identities: before=%+v after=%+v", before, after)
	}
}

func TestRemovedFileCreatesTombstone_when_queued_file_disappears(t *testing.T) {
	// Given
	root := t.TempDir()
	path := filepath.Join(root, "queued.flac")
	writeRealFixture(t, "real.flac", path)
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	first, err := scanner.Scan(context.Background(), EmptySnapshot())
	assertNoError(t, err)
	before := onlyTrack(t, first.Snapshot)
	assertNoError(t, os.Remove(path))
	// When
	second, err := scanner.Scan(context.Background(), first.Snapshot)
	// Then
	assertNoError(t, err)
	after, ok := second.Snapshot.Tracks[before.TrackID]
	if !ok || after.Available {
		t.Fatalf("removed track = (%+v, %v), want tombstone", after, ok)
	}
	if after.AlbumID != before.AlbumID || after.Order != before.Order {
		t.Fatalf("tombstone lost anchor: before=%+v after=%+v", before, after)
	}
}

func TestRootEscapeSymlink_when_link_escapes_root(t *testing.T) {
	// Given
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.flac")
	writeRealFixture(t, "real.flac", outside)
	assertNoError(t, os.Symlink(outside, filepath.Join(root, "escape.flac")))
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	// When
	result, err := scanner.Scan(context.Background(), EmptySnapshot())
	// Then
	assertNoError(t, err)
	if len(result.Snapshot.Tracks) != 0 || !hasIssue(result.Issues, IssueOutsideRoot) {
		t.Fatalf("outside symlink result = %+v", result)
	}
}

func TestScanFollowsSymlinkInside_when_target_is_in_root(t *testing.T) {
	// Given
	root := t.TempDir()
	writeRealFixture(t, "real.flac", filepath.Join(root, "target.flac"))
	assertNoError(t, os.Symlink("target.flac", filepath.Join(root, "inside.flac")))
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	// When
	result, err := scanner.Scan(context.Background(), EmptySnapshot())
	// Then
	assertNoError(t, err)
	if availableCount(result.Snapshot) != 1 {
		t.Fatalf("available tracks = %d, want one", availableCount(result.Snapshot))
	}
}

func TestTraversalRejected_when_relative_path_escapes(t *testing.T) {
	// Given
	root := t.TempDir()
	// When
	_, err := Resolve(root, "../outside.flac")
	// Then
	if !errors.Is(err, ErrOutsideRoot) {
		t.Fatalf("Resolve error = %v, want ErrOutsideRoot", err)
	}
}

func copyFixtureTree(t *testing.T) string {
	t.Helper()
	return decodeRealFixtures(t)
}

func onlyTrack(t *testing.T, snapshot Snapshot) Track {
	t.Helper()
	for _, track := range snapshot.Tracks {
		if track.Available {
			return track
		}
	}
	t.Fatal("no available track")
	return Track{}
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func hasIssue(issues []Issue, code IssueCode) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func availableCount(snapshot Snapshot) int {
	count := 0
	for _, track := range snapshot.Tracks {
		if track.Available {
			count++
		}
	}
	return count
}

func TestIncrementalIndexResumes_without_duplicate_analysis_work(t *testing.T) {
	// Given
	root := copyFixtureTree(t)
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelingReader{cancel: cancel}
	scanner, err := NewScannerWithReader(root, reader)
	assertNoError(t, err)
	partial, err := scanner.Scan(ctx, EmptySnapshot())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted scan error = %v, want context cancellation", err)
	}
	if partial.Complete || len(partial.AnalysisJobs) != 1 {
		t.Fatalf("partial result = %+v, want one resumable job", partial)
	}
	// When
	resumer, err := NewScanner(root)
	assertNoError(t, err)
	resumed, err := resumer.Scan(context.Background(), partial.Snapshot)
	// Then
	assertNoError(t, err)
	if !resumed.Complete || len(resumed.AnalysisJobs) != 4 {
		t.Fatalf("resumed result = %+v, want four remaining jobs", resumed)
	}
}

func TestChangedContentPreservesFileAndTrackIdentity_when_path_is_stable(t *testing.T) {
	// Given
	root := t.TempDir()
	path := filepath.Join(root, "track.flac")
	original := bytes.Replace(decodeFixture(t, "real.flac.b64"), []byte("MUSICBRAINZ_TRACKID"), []byte("XUSICBRAINZ_TRACKID"), 1)
	assertNoError(t, os.WriteFile(path, original, 0o600))
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	first, err := scanner.Scan(context.Background(), EmptySnapshot())
	assertNoError(t, err)
	before := onlyTrack(t, first.Snapshot)
	changed := bytes.Clone(original)
	changed[len(changed)-1] ^= 1
	assertNoError(t, os.WriteFile(path, changed, 0o600))
	// When
	second, err := scanner.Scan(context.Background(), first.Snapshot)
	// Then
	assertNoError(t, err)
	after := onlyTrack(t, second.Snapshot)
	if after.FileID != before.FileID || after.TrackID != before.TrackID {
		t.Fatalf("stable path identities changed: before=%+v after=%+v", before, after)
	}
	if after.RecordingID == before.RecordingID || len(second.AnalysisJobs) != 1 {
		t.Fatalf("changed content did not create new recording analysis: result=%+v", second)
	}
}

type cancelingReader struct {
	cancel context.CancelFunc
	calls  int
}

func (r *cancelingReader) ReadStable(path string) (MediaSnapshot, FileVersion, FileVersion, error) {
	media, before, after, err := (osSnapshotReader{}).ReadStable(path)
	r.calls++
	if r.calls == 1 {
		r.cancel()
	}
	return media, before, after, err
}
