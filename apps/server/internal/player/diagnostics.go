package player

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/fault"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type diagnosticErrorInfo struct {
	category   string
	action     string
	code       int
	httpStatus int
	faultCode  string
}

func diagnosticError(err error, fallback string) diagnosticErrorInfo {
	info := diagnosticErrorInfo{category: fallback}
	if err == nil {
		return info
	}
	var actionError *output.ActionError
	if errors.As(err, &actionError) {
		info.category = string(actionError.Kind)
		info.action = actionError.Action
		info.code = actionError.Code
		return info
	}
	var publicError *fault.Error
	if errors.As(err, &publicError) {
		info.category = "fault"
		info.httpStatus = publicError.Status
		info.faultCode = publicError.Code
		return info
	}
	if errors.Is(err, context.DeadlineExceeded) {
		info.category = "timeout"
	} else if errors.Is(err, context.Canceled) {
		info.category = "cancelled"
	} else if info.category == "" {
		info.category = "internal"
	}
	return info
}

func diagnosticElapsedMS(started, finished time.Time) int64 {
	if started.IsZero() || finished.Before(started) {
		return 0
	}
	return finished.Sub(started).Milliseconds()
}

func diagnosticTransportStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "OK":
		return "OK"
	case "ERROR_OCCURRED":
		return "ERROR_OCCURRED"
	default:
		return "unknown"
	}
}

func (s *Service) logCommandAccepted(commandID, action, rendererID, playID, currentEntryID, targetEntryID, source, state string, sequence int64, positionMS int64) {
	log.Printf("diagnostic component=player event=command_accepted service_epoch=%q command_id=%q action=%q renderer_id=%q play_id=%q current_entry_id=%q target_entry_id=%q source=%q sequence=%d state=%q position_ms=%d status=%q", s.epoch, commandID, action, rendererID, playID, currentEntryID, targetEntryID, source, sequence, state, positionMS, "pending")
}

func (s *Service) logCommandTerminal(command commandRecord, status, reason string, st storedState, resultPlayID, resultEntryID, resultState string, positionMS int64, info diagnosticErrorInfo, finished time.Time) {
	log.Printf("diagnostic component=player event=command_terminal service_epoch=%q command_id=%q action=%q renderer_id=%q play_id=%q result_play_id=%q current_entry_id=%q result_entry_id=%q state=%q result_state=%q position_ms=%d status=%q reason=%q error_category=%q renderer_action=%q renderer_code=%d http_status=%d fault_code=%q elapsed_ms=%d", s.epoch, command.id, command.action, st.rendererID, st.playID, resultPlayID, st.currentEntryID, resultEntryID, st.state, resultState, positionMS, status, reason, info.category, info.action, info.code, info.httpStatus, info.faultCode, diagnosticElapsedMS(command.startedAt, finished))
}

func (s *Service) logOutputSelected(previous storedState, rendererID, state string, online bool) {
	status := "offline"
	if online {
		status = "online"
	}
	log.Printf("diagnostic component=player event=output_selected service_epoch=%q renderer_id=%q previous_renderer_id=%q play_id=%q current_entry_id=%q sequence=%d state=%q previous_state=%q status=%q", s.epoch, rendererID, previous.rendererID, previous.playID, previous.currentEntryID, previous.revision+1, state, previous.state, status)
}

func (s *Service) logRendererUnavailable(rendererID, playID, currentEntryID, previousState, reason string, sequence int64) {
	log.Printf("diagnostic component=player event=renderer_unavailable service_epoch=%q renderer_id=%q play_id=%q current_entry_id=%q sequence=%d state=%q previous_state=%q status=%q reason=%q", s.epoch, rendererID, playID, currentEntryID, sequence, StateUnavailable, previousState, "offline", reason)
}

func (s *Service) logObservationFailure(st storedState, count int, info diagnosticErrorInfo, warning bool) {
	log.Printf("diagnostic component=player event=observation_failed_transition service_epoch=%q renderer_id=%q play_id=%q current_entry_id=%q sequence=%d state=%q previous_state=%q status=%q reason=%q failure_count=%d warning=%t error_category=%q renderer_action=%q renderer_code=%d", s.epoch, st.rendererID, st.playID, st.currentEntryID, st.revision+1, StateUnavailable, st.state, "failed", "observe_failed", count, warning, info.category, info.action, info.code)
}

func (s *Service) logObservationRecovered(failure observationFailure, observation output.Observation) {
	log.Printf("diagnostic component=player event=observation_recovered service_epoch=%q renderer_id=%q play_id=%q current_entry_id=%q sequence=%d state=%q status=%q reason=%q failure_count=%d", s.epoch, failure.rendererID, failure.playID, failure.currentEntryID, observation.CommandSequence, normalizeObservedState(observation.State), diagnosticTransportStatus(observation.TransportStatus), "observe_succeeded", failure.count)
}

func (s *Service) logObservationInterrupted(st storedState, observation output.Observation, resultState, reason string, positionMS int64) {
	category := "ownership"
	if reason == "renderer_playback_error" {
		category = "renderer_reported_error"
	} else if reason == "renderer_stopped" {
		category = "renderer_stopped"
	}
	log.Printf("diagnostic component=player event=observation_interrupted service_epoch=%q renderer_id=%q play_id=%q current_entry_id=%q sequence=%d state=%q previous_state=%q observed_state=%q status=%q reason=%q position_ms=%d error_category=%q", s.epoch, st.rendererID, st.playID, st.currentEntryID, observation.CommandSequence, resultState, st.state, normalizeObservedState(observation.State), diagnosticTransportStatus(observation.TransportStatus), reason, positionMS, category)
}

func (s *Service) logNaturalTransition(st storedState, observation output.Observation, commandID, action, nextEntryID, resultState, reason string, positionMS int64) {
	log.Printf("diagnostic component=player event=automatic_next_transition service_epoch=%q command_id=%q action=%q renderer_id=%q play_id=%q current_entry_id=%q target_entry_id=%q sequence=%d state=%q previous_state=%q observed_state=%q status=%q reason=%q position_ms=%d", s.epoch, commandID, action, st.rendererID, st.playID, st.currentEntryID, nextEntryID, observation.CommandSequence, resultState, st.state, normalizeObservedState(observation.State), diagnosticTransportStatus(observation.TransportStatus), reason, positionMS)
}

func (s *Service) logMediaFailureTransition(st storedState, observation output.Observation, commandID, nextEntryID, resultState string, positionMS int64) {
	log.Printf("diagnostic component=player event=automatic_next_transition service_epoch=%q command_id=%q action=%q renderer_id=%q play_id=%q current_entry_id=%q target_entry_id=%q sequence=%d state=%q previous_state=%q observed_state=%q status=%q reason=%q position_ms=%d error_category=%q", s.epoch, commandID, "error_next", st.rendererID, st.playID, st.currentEntryID, nextEntryID, observation.CommandSequence, resultState, st.state, normalizeObservedState(observation.State), diagnosticTransportStatus(observation.TransportStatus), "media_failed", positionMS, "media")
}
