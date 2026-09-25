package player

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
	"github.com/jastreamer/jastreamer-server/internal/playstats"
)

const (
	fullPlayThreshold = 30 * time.Second
	seekJumpTolerance = 2 * time.Second
	maximumListenGap  = 5 * time.Second
)

type listeningEvidence struct {
	playID      string
	trackID     string
	durationMS  int64
	listened    time.Duration
	observedAt  time.Time
	receivedAt  time.Time
	positionMS  int64
	hasBaseline bool
	counted     bool
}

func (s *Service) resetListeningInterval(playID string) {
	if s.listening.playID != playID {
		return
	}
	s.listening.hasBaseline = false
	s.listening.observedAt = time.Time{}
	s.listening.receivedAt = time.Time{}
}

func (s *Service) listeningGapLimit() time.Duration {
	limit := 3 * s.pollInterval
	if limit < 2*time.Second {
		limit = 2 * time.Second
	}
	if limit > maximumListenGap {
		limit = maximumListenGap
	}
	return limit
}

func playThreshold(durationMS int64) time.Duration {
	if durationMS > 0 && durationMS < int64(time.Minute/time.Millisecond) {
		return time.Duration(durationMS) * time.Millisecond / 2
	}
	return fullPlayThreshold
}

// prepareListening derives the next in-memory evidence and, when qualified,
// records the count in the observation transaction. The caller must install
// next only after that transaction commits.
func (s *Service) prepareListening(ctx context.Context, tx *sql.Tx, st storedState, observation output.Observation, completed bool) (next listeningEvidence, libraryChanged bool, err error) {
	next = s.listening
	if st.playID == "" || st.currentEntryID == "" {
		return listeningEvidence{}, false, nil
	}
	if next.playID != st.playID {
		trackID := st.currentTrackID
		if trackID == "" {
			if err := tx.QueryRowContext(ctx, "SELECT track_id FROM player_queue WHERE entry_id=?", st.currentEntryID).Scan(&trackID); err != nil {
				return listeningEvidence{}, false, fmt.Errorf("player: load listening track: %w", err)
			}
		}
		next = listeningEvidence{playID: st.playID, trackID: trackID, durationMS: st.durationMS}
	}
	if next.counted {
		return next, false, nil
	}
	if observation.DurationMS > 0 {
		next.durationMS = observation.DurationMS
	} else if next.durationMS <= 0 && st.durationMS > 0 {
		next.durationMS = st.durationMS
	}

	receivedAt := s.now().UTC()
	observedAt := observation.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = receivedAt
	}
	normalized := normalizeObservedState(observation.State)
	correlated := observation.PlayID == st.playID && observation.PlayID != ""
	if observation.PlayID == "" {
		correlated = observation.HasURI && st.currentURI != "" && observation.URI == st.currentURI
	}
	if completed {
		// Completion is accepted only after the ownership checks in the ordinary
		// natural-end path, including renderers that clear the owned URI.
		correlated = true
	}
	if !correlated || st.resumeRequired || strings.EqualFold(strings.TrimSpace(observation.TransportStatus), "ERROR_OCCURRED") {
		next.hasBaseline = false
		return next, false, nil
	}

	position := observation.PositionMS
	hasPosition := observation.HasPosition && position >= 0
	if completed && next.durationMS > 0 {
		position = next.durationMS
		hasPosition = true
	}
	canAdvance := normalized == "playing" || normalized == "paused" || completed
	if !canAdvance || !hasPosition {
		next.hasBaseline = false
		return next, false, nil
	}
	if next.durationMS > 0 && position > next.durationMS+1000 {
		next.hasBaseline = false
		return next, false, nil
	}

	if next.hasBaseline {
		observedElapsed := observedAt.Sub(next.observedAt)
		receivedElapsed := receivedAt.Sub(next.receivedAt)
		positionElapsed := time.Duration(position-next.positionMS) * time.Millisecond
		gapLimit := s.listeningGapLimit()
		switch {
		case observedElapsed <= 0 || receivedElapsed <= 0:
			next.hasBaseline = false
		case observedElapsed > gapLimit || receivedElapsed > gapLimit:
			next.hasBaseline = false
		case positionElapsed < 0:
			next.hasBaseline = false
		case positionElapsed == 0:
			// Whole-second renderer positions may repeat between polls. Retain the
			// earlier baseline briefly, but never credit the duplicate itself.
			if normalized != "playing" {
				next.hasBaseline = false
			}
			return next, false, nil
		case positionElapsed > observedElapsed+seekJumpTolerance || positionElapsed > receivedElapsed+seekJumpTolerance:
			// A progress jump that outruns both clocks is a seek, not listening.
			next.hasBaseline = false
		default:
			credit := positionElapsed
			if observedElapsed < credit {
				credit = observedElapsed
			}
			if receivedElapsed < credit {
				credit = receivedElapsed
			}
			next.listened += credit
		}
	}

	if normalized == "playing" && !completed {
		next.positionMS = position
		next.observedAt = observedAt
		next.receivedAt = receivedAt
		next.hasBaseline = true
	} else {
		next.hasBaseline = false
	}
	if next.listened < playThreshold(next.durationMS) {
		return next, false, nil
	}
	changed, err := playstats.Record(ctx, tx, next.trackID, next.playID)
	if err != nil {
		return listeningEvidence{}, false, err
	}
	next.counted = true
	return next, changed, nil
}
