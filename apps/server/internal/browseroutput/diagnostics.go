package browseroutput

import (
	"errors"
	"log"
	"strconv"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

const diagnosticSummaryInterval = 30 * time.Second

type diagnosticLimiter struct {
	lastLog    time.Time
	suppressed uint64
}

type registrationDiagnostics struct {
	createdAt                time.Time
	lastPoll                 time.Time
	lastRenew                time.Time
	lastReport               time.Time
	lastSeenGap              time.Duration
	lastSummaryAt            time.Time
	lastDeliveryAt           time.Time
	lastAcknowledgementAt    time.Time
	lastDeliveredSequence    uint64
	lastAcknowledgedSequence uint64
	deliveredSequence        uint64
	pollCount                uint64
	renewalCount             uint64
	reportCount              uint64
	deliveryRepeatCount      uint64
	duplicateReportCount     uint64
	ownerRejectionCount      uint64
	reportRejectionCount     uint64
	ownerRejectionLimiter    diagnosticLimiter
	reportRejectionLimiter   diagnosticLimiter
}

func (limiter *diagnosticLimiter) record(now time.Time) (uint64, bool) {
	if limiter.lastLog.IsZero() || !now.Before(limiter.lastLog.Add(diagnosticSummaryInterval)) {
		suppressed := limiter.suppressed
		limiter.lastLog = now
		limiter.suppressed = 0
		return suppressed, true
	}
	limiter.suppressed++
	return 0, false
}

func (service *Service) logRegistrationCreatedLocked(value *registration, now time.Time) {
	log.Printf("diagnostic component=browseroutput event=registration_created renderer_id=%q created_at=%q lease_expires_at=%q protocol_count=%d", value.id, diagnosticTimestamp(now), diagnosticTimestamp(value.expiresAt), len(value.protocolInfo))
}

func (service *Service) recordRenewLocked(value *registration) {
	value.diagnostics.lastRenew = value.lastSeen
	value.diagnostics.renewalCount++
}

func (service *Service) recordPollLocked(value *registration) {
	value.diagnostics.lastPoll = value.lastSeen
	value.diagnostics.pollCount++
}

func (service *Service) recordReportLocked(value *registration) {
	value.diagnostics.lastReport = value.lastSeen
	value.diagnostics.reportCount++
}

func (service *Service) recordCommandDeliveryLocked(value *registration, command *Command) {
	now := value.lastSeen
	if value.diagnostics.deliveredSequence == command.Sequence {
		value.diagnostics.deliveryRepeatCount++
		return
	}
	value.diagnostics.deliveredSequence = command.Sequence
	value.diagnostics.lastDeliveredSequence = command.Sequence
	value.diagnostics.lastDeliveryAt = now
	log.Printf("diagnostic component=browseroutput event=command_first_delivery renderer_id=%q command_id=%q sequence=%d action=%q play_id=%q delivered_at=%q", value.id, diagnosticCommandID(value.id, command.Sequence), command.Sequence, command.Action, command.PlayID, diagnosticTimestamp(now))
}

func (service *Service) logCommandDispatchedLocked(value *registration, command *Command) {
	log.Printf("diagnostic component=browseroutput event=command_dispatched renderer_id=%q command_id=%q sequence=%d action=%q play_id=%q", value.id, diagnosticCommandID(value.id, command.Sequence), command.Sequence, command.Action, command.PlayID)
}

func (service *Service) logCommandResultLocked(value *registration, command *Command, status, reason string) {
	now := value.lastSeen
	value.diagnostics.lastAcknowledgedSequence = command.Sequence
	value.diagnostics.lastAcknowledgementAt = now
	log.Printf("diagnostic component=browseroutput event=command_result renderer_id=%q command_id=%q sequence=%d action=%q play_id=%q status=%q reason=%q acknowledged_at=%q state=%q transport_status=%q position_ms=%d", value.id, diagnosticCommandID(value.id, command.Sequence), command.Sequence, command.Action, command.PlayID, status, reason, diagnosticTimestamp(now), value.observation.State, value.observation.TransportStatus, value.observation.PositionMS)
}

func (service *Service) logCommandCancelledLocked(value *registration, command *Command, reason string) {
	log.Printf("diagnostic component=browseroutput event=command_cancelled renderer_id=%q command_id=%q sequence=%d action=%q play_id=%q status=%q reason=%q", value.id, diagnosticCommandID(value.id, command.Sequence), command.Sequence, command.Action, command.PlayID, "cancelled", reason)
}

func (service *Service) logObservationTransitionLocked(value *registration, previous, current output.Observation) {
	if previous.State == current.State && previous.TransportStatus == current.TransportStatus && previous.Completed == current.Completed {
		return
	}
	log.Printf("diagnostic component=browseroutput event=observation_transition renderer_id=%q play_id=%q sequence=%d state=%q previous_state=%q transport_status=%q previous_transport_status=%q completed=%t position_ms=%d", value.id, current.PlayID, current.CommandSequence, current.State, previous.State, current.TransportStatus, previous.TransportStatus, current.Completed, current.PositionMS)
}

func (service *Service) logResourceRevokedLocked(value *registration, playID string) {
	log.Printf("diagnostic component=browseroutput event=media_resource_revoked renderer_id=%q play_id=%q cancel_before_sequence=%d state=%q status=%q", value.id, playID, value.cancelBefore, value.observation.State, value.observation.TransportStatus)
}

func (service *Service) logDisconnectLocked(value *registration) {
	log.Printf("diagnostic component=browseroutput event=registration_disconnected renderer_id=%q play_id=%q last_delivered_sequence=%d last_acknowledged_sequence=%d state=%q status=%q", value.id, diagnosticResourcePlayID(value), value.diagnostics.lastDeliveredSequence, value.diagnostics.lastAcknowledgedSequence, value.observation.State, value.observation.TransportStatus)
}

func (service *Service) logServiceShutdownLocked(registrationCount int) {
	log.Printf("diagnostic component=browseroutput event=service_shutdown registration_count=%d", registrationCount)
}

func (service *Service) logShutdownLocked(value *registration) {
	pendingSequence, pendingAction, pendingPlayID := diagnosticPending(value)
	log.Printf("diagnostic component=browseroutput event=registration_shutdown renderer_id=%q play_id=%q pending_sequence=%d pending_action=%q pending_play_id=%q last_delivered_sequence=%d last_acknowledged_sequence=%d state=%q status=%q", value.id, diagnosticResourcePlayID(value), pendingSequence, pendingAction, pendingPlayID, value.diagnostics.lastDeliveredSequence, value.diagnostics.lastAcknowledgedSequence, value.observation.State, value.observation.TransportStatus)
}

func (service *Service) logLeaseExpiredLocked(value *registration, now time.Time) {
	pendingSequence, pendingAction, pendingPlayID := diagnosticPending(value)
	log.Printf("diagnostic component=browseroutput event=lease_expired renderer_id=%q created_at=%q registration_age_ms=%d lease_expires_at=%q lease_expired_age_ms=%d last_seen_at=%q last_seen_age_ms=%d last_poll_at=%q last_poll_age_ms=%d last_renew_at=%q last_renew_age_ms=%d last_report_at=%q last_report_age_ms=%d play_id=%q pending_sequence=%d pending_action=%q pending_play_id=%q last_delivered_sequence=%d last_delivered_at=%q last_acknowledged_sequence=%d last_acknowledged_at=%q state=%q status=%q position_ms=%d", value.id, diagnosticTimestamp(value.diagnostics.createdAt), diagnosticAgeMS(now, value.diagnostics.createdAt), diagnosticTimestamp(value.expiresAt), diagnosticAgeMS(now, value.expiresAt), diagnosticTimestamp(value.lastSeen), diagnosticAgeMS(now, value.lastSeen), diagnosticTimestamp(value.diagnostics.lastPoll), diagnosticAgeMS(now, value.diagnostics.lastPoll), diagnosticTimestamp(value.diagnostics.lastRenew), diagnosticAgeMS(now, value.diagnostics.lastRenew), diagnosticTimestamp(value.diagnostics.lastReport), diagnosticAgeMS(now, value.diagnostics.lastReport), diagnosticResourcePlayID(value), pendingSequence, pendingAction, pendingPlayID, value.diagnostics.lastDeliveredSequence, diagnosticTimestamp(value.diagnostics.lastDeliveryAt), value.diagnostics.lastAcknowledgedSequence, diagnosticTimestamp(value.diagnostics.lastAcknowledgementAt), value.observation.State, value.observation.TransportStatus, value.observation.PositionMS)
}

func (service *Service) logOwnerRejectionLocked(value *registration, reason string, now time.Time) {
	value.diagnostics.ownerRejectionCount++
	repeats, allowed := value.diagnostics.ownerRejectionLimiter.record(now)
	if !allowed {
		return
	}
	log.Printf("diagnostic component=browseroutput event=owner_rejected renderer_id=%q reason=%q repeats=%d rejection_count=%d", value.id, reason, repeats, value.diagnostics.ownerRejectionCount)
}

func (service *Service) rejectReportLocked(value *registration, reason string, err error) error {
	now := value.lastSeen
	value.diagnostics.reportRejectionCount++
	repeats, allowed := value.diagnostics.reportRejectionLimiter.record(now)
	if allowed {
		pendingSequence, pendingAction, pendingPlayID := diagnosticPending(value)
		log.Printf("diagnostic component=browseroutput event=report_rejected renderer_id=%q reason=%q repeats=%d rejection_count=%d pending_sequence=%d pending_action=%q pending_play_id=%q", value.id, reason, repeats, value.diagnostics.reportRejectionCount, pendingSequence, pendingAction, pendingPlayID)
	}
	return err
}

func (service *Service) maybeLogHealthLocked(value *registration, now time.Time) {
	if now.Before(value.diagnostics.lastSummaryAt.Add(diagnosticSummaryInterval)) {
		return
	}
	pendingSequence, pendingAction, pendingPlayID := diagnosticPending(value)
	log.Printf("diagnostic component=browseroutput event=health_summary renderer_id=%q registration_age_ms=%d elapsed_ms=%d poll_count=%d renewal_count=%d report_count=%d delivery_repeat_count=%d duplicate_report_count=%d owner_rejection_count=%d report_rejection_count=%d last_seen_gap_ms=%d last_poll_at=%q last_renew_at=%q last_report_at=%q last_delivered_sequence=%d last_delivered_at=%q last_acknowledged_sequence=%d last_acknowledged_at=%q play_id=%q pending_sequence=%d pending_action=%q pending_play_id=%q state=%q status=%q position_ms=%d", value.id, diagnosticAgeMS(now, value.diagnostics.createdAt), diagnosticAgeMS(now, value.diagnostics.lastSummaryAt), value.diagnostics.pollCount, value.diagnostics.renewalCount, value.diagnostics.reportCount, value.diagnostics.deliveryRepeatCount, value.diagnostics.duplicateReportCount, value.diagnostics.ownerRejectionCount, value.diagnostics.reportRejectionCount, value.diagnostics.lastSeenGap.Milliseconds(), diagnosticTimestamp(value.diagnostics.lastPoll), diagnosticTimestamp(value.diagnostics.lastRenew), diagnosticTimestamp(value.diagnostics.lastReport), value.diagnostics.lastDeliveredSequence, diagnosticTimestamp(value.diagnostics.lastDeliveryAt), value.diagnostics.lastAcknowledgedSequence, diagnosticTimestamp(value.diagnostics.lastAcknowledgementAt), diagnosticResourcePlayID(value), pendingSequence, pendingAction, pendingPlayID, value.observation.State, value.observation.TransportStatus, value.observation.PositionMS)
	value.diagnostics.lastSummaryAt = now
}

func diagnosticPending(value *registration) (uint64, string, string) {
	if value.pending == nil {
		return 0, "", ""
	}
	return value.pending.command.Sequence, value.pending.command.Action, value.pending.command.PlayID
}

func diagnosticResourcePlayID(value *registration) string {
	if value.resource == nil {
		return ""
	}
	return value.resource.PlayID
}

func diagnosticCommandID(rendererID string, sequence uint64) string {
	return rendererID + "/" + strconv.FormatUint(sequence, 10)
}

func diagnosticTimestamp(value time.Time) string {
	if value.IsZero() {
		return "never"
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func diagnosticAgeMS(now, value time.Time) int64 {
	if value.IsZero() {
		return -1
	}
	age := now.Sub(value)
	if age < 0 {
		return 0
	}
	return age.Milliseconds()
}

func reportRejectionReason(err error) string {
	if errors.Is(err, ErrStaleReport) {
		return "stale_observation"
	}
	return "invalid_observation"
}
