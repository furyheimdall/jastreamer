package catalog

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
)

type Scanner struct {
	root   string
	rootID RootID
	reader SnapshotReader
}

type osSnapshotReader struct{}

func (osSnapshotReader) ReadStable(path string) (media MediaSnapshot, before FileVersion, after FileVersion, err error) {
	file, err := os.Open(path)
	if err != nil {
		return MediaSnapshot{}, before, after, fmt.Errorf("open: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	first, err := file.Stat()
	if err != nil {
		return MediaSnapshot{}, before, after, fmt.Errorf("stat before read: %w", err)
	}
	media, err = parseMedia(path, file)
	if err != nil {
		return MediaSnapshot{}, before, after, fmt.Errorf("parse media: %w", err)
	}
	last, err := file.Stat()
	if err != nil {
		return MediaSnapshot{}, before, after, fmt.Errorf("stat after read: %w", err)
	}
	return media, versionOf(first), versionOf(last), nil
}

func NewScanner(root string) (*Scanner, error) { return NewScannerWithReader(root, osSnapshotReader{}) }
func NewScannerWithReader(root string, reader SnapshotReader) (*Scanner, error) {
	canonical, err := canonicalRoot(root)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, errors.New("catalog: nil snapshot reader")
	}
	return &Scanner{root: canonical, rootID: RootID(hashID("logical-root", canonical)), reader: reader}, nil
}

func (s *Scanner) Scan(ctx context.Context, previous Snapshot) (ScanResult, error) {
	generation := previous.Generation + 1
	tracks := cloneTracks(previous.Tracks)
	result := ScanResult{Snapshot: Snapshot{Generation: generation, Revision: previous.Revision, Tracks: tracks}, Complete: true}
	seen, targets := make(map[TrackID]bool), make(map[string]bool)
	changed := false
	walkErr := filepath.WalkDir(s.root, func(path string, entry os.DirEntry, walkError error) error {
		if err := ctx.Err(); err != nil {
			result.Complete = false
			return err
		}
		if walkError != nil {
			result.Issues = append(result.Issues, issueFor(path, walkError))
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(s.root, path)
		if err != nil {
			result.Issues = append(result.Issues, Issue{Path: path, Code: IssueOutsideRoot, Err: err})
			return nil
		}
		resolved, err := Resolve(s.root, relative)
		if err != nil {
			result.Issues = append(result.Issues, Issue{Path: relative, Code: IssueOutsideRoot, Err: err})
			return nil
		}
		info, err := os.Stat(resolved)
		if err != nil {
			result.Issues = append(result.Issues, issueFor(relative, err))
			return nil
		}
		if !info.Mode().IsRegular() || !isSupportedPath(relative) || targets[resolved] {
			return nil
		}
		targets[resolved] = true
		prior, hasPrior := findByPath(previous.Tracks, relative)
		if hasPrior {
			seen[prior.TrackID] = true
		}
		currentVersion := versionOf(info)
		if hasPrior && prior.Available && prior.FileVersion == currentVersion {
			prior.Generation = generation
			tracks[prior.TrackID] = prior
			return nil
		}
		if info.Mode().Perm()&0o444 == 0 {
			result.Issues = append(result.Issues, Issue{Path: relative, Code: IssuePermission, Err: os.ErrPermission})
			if preserveUnavailable(tracks, prior, hasPrior, generation) {
				changed = true
			}
			return nil
		}
		media, before, after, err := s.reader.ReadStable(resolved)
		if err != nil {
			code := IssueRead
			if errors.Is(err, ErrMalformedMedia) {
				code = IssueMalformed
			} else if errors.Is(err, os.ErrPermission) {
				code = IssuePermission
			}
			result.Issues = append(result.Issues, Issue{Path: relative, Code: code, Err: err})
			if preserveUnavailable(tracks, prior, hasPrior, generation) {
				changed = true
			}
			return nil
		}
		if currentVersion != before || before != after {
			result.Issues = append(result.Issues, Issue{Path: relative, Code: IssueChangedDuringRead, Err: errors.New("file changed while fingerprinting")})
			if preserveUnavailable(tracks, prior, hasPrior, generation) {
				changed = true
			}
			return nil
		}
		if !hasPrior {
			prior, hasPrior = findUniqueFingerprint(previous.Tracks, media.ContentFingerprint)
		}
		ids := identities(relative, media.ContentFingerprint, media.AudioFingerprint, media.Metadata)
		if hasPrior {
			ids.FileID, ids.TrackID = prior.FileID, prior.TrackID
		}
		status := AnalysisQueued
		if hasPrior && prior.AudioFingerprint == media.AudioFingerprint {
			status = prior.AnalysisStatus
		}
		track := Track{
			RootID: s.rootID, FileID: ids.FileID, TrackID: ids.TrackID, RecordingID: ids.RecordingID, AlbumID: ids.AlbumID,
			RelativePath: filepathSlash(relative), Format: media.Format, Fingerprint: media.ContentFingerprint,
			AudioFingerprint: media.AudioFingerprint, FileVersion: after, Metadata: media.Metadata,
			Order: NewOrderKey(media.Metadata, relative, ids.TrackID), Available: true, Generation: generation,
			AnalysisStatus: status,
		}
		seen[track.TrackID] = true
		if !hasPrior || !equivalentTrack(prior, track) {
			changed = true
		}
		tracks[track.TrackID] = track
		if !hasPrior || prior.AudioFingerprint != media.AudioFingerprint {
			result.AnalysisJobs = append(result.AnalysisJobs, AnalysisJob{TrackID: track.TrackID, Fingerprint: media.AudioFingerprint})
		}
		return nil
	})
	if walkErr != nil {
		if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
			return result, fmt.Errorf("scan interrupted: %w", walkErr)
		}
		return result, fmt.Errorf("walk catalog: %w", walkErr)
	}
	for id, track := range tracks {
		if track.Available && !seen[id] {
			track.Available = false
			track.Generation = generation
			tracks[id] = track
			changed = true
		}
	}
	if changed {
		result.Snapshot.Revision++
	}
	return result, nil
}

func versionOf(info os.FileInfo) FileVersion {
	return FileVersion{Size: info.Size(), Modified: info.ModTime()}
}

func cloneTracks(source map[TrackID]Track) map[TrackID]Track {
	result := make(map[TrackID]Track, len(source))
	maps.Copy(result, source)
	return result
}

func findByPath(tracks map[TrackID]Track, path string) (Track, bool) {
	normalized := filepathSlash(path)
	for _, track := range tracks {
		if track.RelativePath == normalized {
			return track, true
		}
	}
	return Track{}, false
}

func findUniqueFingerprint(tracks map[TrackID]Track, fingerprint string) (Track, bool) {
	var found Track
	count := 0
	for _, track := range tracks {
		if track.Fingerprint == fingerprint {
			found = track
			count++
		}
	}
	return found, count == 1
}

func equivalentTrack(left, right Track) bool {
	left.Generation, right.Generation = 0, 0
	return reflect.DeepEqual(left, right)
}

func preserveUnavailable(tracks map[TrackID]Track, prior Track, found bool, generation uint64) bool {
	if !found {
		return false
	}
	changed := prior.Available
	prior.Available = false
	prior.Generation = generation
	tracks[prior.TrackID] = prior
	return changed
}

func issueFor(path string, err error) Issue {
	if errors.Is(err, os.ErrPermission) {
		return Issue{Path: path, Code: IssuePermission, Err: err}
	}
	return Issue{Path: path, Code: IssueRead, Err: err}
}
