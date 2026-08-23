package catalog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScanRecognizesInitialFormats_when_signatures_are_valid(t *testing.T) {
	// Given
	root := copyFixtureTree(t)
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	// When
	result, err := scanner.Scan(context.Background(), EmptySnapshot())
	// Then
	assertNoError(t, err)
	got := map[Format]bool{}
	for _, track := range result.Snapshot.Tracks {
		got[track.Format] = true
	}
	for _, format := range []Format{FormatFLAC, FormatMP3, FormatOggVorbis, FormatOpus, FormatPCMWAV} {
		if !got[format] {
			t.Errorf("format %q was not recognized", format)
		}
	}
}

func TestMalformedContainer_when_other_files_are_valid(t *testing.T) {
	// Given
	root := t.TempDir()
	writeRealFixture(t, "real.flac", filepath.Join(root, "good.flac"))
	assertNoError(t, os.WriteFile(filepath.Join(root, "bad.flac"), []byte("not flac"), 0o600))
	assertNoError(t, os.WriteFile(filepath.Join(root, "note.txt"), []byte("ignored"), 0o600))
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	// When
	result, err := scanner.Scan(context.Background(), EmptySnapshot())
	// Then
	assertNoError(t, err)
	if availableCount(result.Snapshot) != 1 || !hasIssue(result.Issues, IssueMalformed) {
		t.Fatalf("result = %+v, want valid track plus malformed issue", result)
	}
}

func TestScanDoesNotCommitFingerprint_when_file_changes_during_read(t *testing.T) {
	// Given
	root := t.TempDir()
	writeRealFixture(t, "real.flac", filepath.Join(root, "moving.flac"))
	scanner, err := NewScannerWithReader(root, changingReader{data: []byte("fLaC\nTITLE=Moving")})
	assertNoError(t, err)
	// When
	result, err := scanner.Scan(context.Background(), EmptySnapshot())
	// Then
	assertNoError(t, err)
	if availableCount(result.Snapshot) != 0 || !hasIssue(result.Issues, IssueChangedDuringRead) {
		t.Fatalf("changed snapshot result = %+v", result)
	}
}

func TestPermissionErrorIsolated_when_mode_has_no_read_bits(t *testing.T) {
	// Given
	root := t.TempDir()
	path := filepath.Join(root, "private.flac")
	writeRealFixture(t, "real.flac", path)
	assertNoError(t, os.Chmod(path, 0))
	t.Cleanup(func() { assertNoError(t, os.Chmod(path, 0o600)) })
	scanner, err := NewScanner(root)
	assertNoError(t, err)
	// When
	result, err := scanner.Scan(context.Background(), EmptySnapshot())
	// Then
	assertNoError(t, err)
	if !hasIssue(result.Issues, IssuePermission) {
		t.Fatalf("issues = %+v, want permission issue", result.Issues)
	}
}

type changingReader struct{ data []byte }

func (r changingReader) ReadStable(string) (MediaSnapshot, FileVersion, FileVersion, error) {
	return MediaSnapshot{
		Format: FormatFLAC, Metadata: Metadata{Title: "Moving"},
		ContentFingerprint: hashID(string(r.data)), AudioFingerprint: hashID("audio", string(r.data)),
	}, FileVersion{Size: int64(len(r.data)), Modified: time.Unix(1, 0)}, FileVersion{Size: int64(len(r.data) + 1), Modified: time.Unix(2, 0)}, nil
}
