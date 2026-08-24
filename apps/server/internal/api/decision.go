package api

import (
	"net/http"
	"strings"

	"github.com/jastreamer/jastreamer-server/internal/curation/ranking"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type decisionView struct {
	DecisionID       string               `json:"decision_id,omitempty"`
	Kind             string               `json:"kind"`
	Reason           string               `json:"reason"`
	Source           string               `json:"source,omitempty"`
	TrackID          string               `json:"track_id,omitempty"`
	QueueEntryID     string               `json:"queue_entry_id,omitempty"`
	AlgorithmVersion string               `json:"algorithm_revision"`
	CatalogRevision  uint64               `json:"catalog_revision"`
	PolicyRevision   int64                `json:"policy_revision"`
	ContractRevision string               `json:"contract_revision"`
	SignalCoverage   int                  `json:"signal_coverage"`
	RecordingKey     string               `json:"recording_key,omitempty"`
	Explanation      *ranking.Explanation `json:"explanation,omitempty"`
}

func (service *server) preview(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	state, found, err := service.config.Queue.AutomaticPreview(
		request.Context(), playback.ZoneID(request.PathValue("zoneID")),
	)
	if err != nil {
		writeError(writer, err)
		return
	}
	view := service.decisionFor(request)
	if found {
		view = service.viewForDecision(request, state.Decision)
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"active": found && state.Active, "replaceable": !found || state.Replaceable,
		"committed": found && state.Committed, "decision": view,
	})
}

func (service *server) explanation(writer http.ResponseWriter, request *http.Request) {
	if _, ok := service.authenticate(writer, request); !ok {
		return
	}
	writeJSON(writer, http.StatusOK, service.decisionFor(request))
}

func (service *server) decisionFor(request *http.Request) decisionView {
	zoneID := playback.ZoneID(request.PathValue("zoneID"))
	latest, found, err := service.config.Queue.LatestDecision(request.Context(), zoneID)
	if err == nil && found {
		return service.viewForDecision(request, latest)
	}
	persistedPolicy, policyErr := service.config.Queue.ContinuationPolicy(request.Context(), zoneID)
	view := service.viewForDecision(request, playback.Decision{Kind: playback.DecisionStop, Reason: playback.ReasonQueueEmpty})
	view.PolicyRevision = persistedPolicy.Revision
	if policyErr != nil || (err != nil && !strings.Contains(err.Error(), "does not exist")) {
		view.Reason = "STATE_UNAVAILABLE"
	}
	return view
}

func (service *server) viewForDecision(request *http.Request, value playback.Decision) decisionView {
	policyValue, _ := service.config.Queue.ContinuationPolicy(
		request.Context(), playback.ZoneID(request.PathValue("zoneID")),
	)
	view := decisionView{
		DecisionID: value.ID, Kind: string(value.Kind), Reason: value.Reason, Source: value.Source,
		TrackID: string(value.TrackID), QueueEntryID: string(value.QueueEntryID),
		AlgorithmVersion: decisionAlgorithm(value), CatalogRevision: service.catalogSnapshot(request.Context()).Revision,
		PolicyRevision: policyValue.Revision, ContractRevision: contractRevision,
		SignalCoverage: service.signalCoverage(request), RecordingKey: value.RecordingKey,
	}
	if value.Explanation.AlgorithmVersion != "" || value.Explanation.RecordingKey != "" {
		view.Explanation = &value.Explanation
	}
	return view
}

func decisionAlgorithm(value playback.Decision) string {
	if value.Explanation.AlgorithmVersion != "" {
		return value.Explanation.AlgorithmVersion
	}
	return ranking.AlgorithmVersion
}

func (service *server) signalCoverage(request *http.Request) int {
	total, complete := 0, 0
	for _, track := range service.catalogSnapshot(request.Context()).Tracks {
		if track.Available {
			total++
			if track.AnalysisStatus == "complete" {
				complete++
			}
		}
	}
	if total == 0 {
		return 0
	}
	return complete * 100 / total
}
