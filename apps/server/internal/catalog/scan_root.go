package catalog

import "context"

func ScanRoot(ctx context.Context, root Root, previous Snapshot) (ScanResult, error) {
	scanner, err := NewScanner(root.CanonicalPath)
	if err != nil {
		return ScanResult{}, err
	}
	scoped := Snapshot{Generation: previous.Generation, Revision: previous.Revision, Tracks: make(map[TrackID]Track)}
	for id, track := range previous.Tracks {
		if track.RootID == root.ID {
			scoped.Tracks[id] = track
		}
	}
	result, err := scanner.Scan(ctx, scoped)
	for id, track := range previous.Tracks {
		if track.RootID != root.ID {
			result.Snapshot.Tracks[id] = track
		}
	}
	for id, track := range result.Snapshot.Tracks {
		if track.RootID == scanner.rootID {
			track.RootID = root.ID
			result.Snapshot.Tracks[id] = track
		}
	}
	return result, err
}
