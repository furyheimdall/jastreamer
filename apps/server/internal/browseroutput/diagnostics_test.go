package browseroutput

import (
	"bytes"
	"errors"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

func captureDiagnosticLogs(t *testing.T, run func()) string {
	t.Helper()
	var buffer bytes.Buffer
	writer, flags, prefix := log.Writer(), log.Flags(), log.Prefix()
	log.SetOutput(&buffer)
	log.SetFlags(0)
	log.SetPrefix("")
	defer func() {
		log.SetOutput(writer)
		log.SetFlags(flags)
		log.SetPrefix(prefix)
	}()
	run()
	return buffer.String()
}

func TestOwnerRejectionLogsAreRedactedAndRateLimited(t *testing.T) {
	now := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	value := &registration{
		id:        "browser:server-generated",
		token:     "registered-owner-secret",
		address:   "192.0.2.10",
		expiresAt: now.Add(time.Hour),
	}
	service := &Service{
		now:     func() time.Time { return now },
		devices: map[string]*registration{value.id: value},
	}

	logs := captureDiagnosticLogs(t, func() {
		for range 3 {
			_, _ = service.ownerLocked(value.id, "attacker-supplied-secret", value.address)
		}
		now = now.Add(diagnosticSummaryInterval)
		_, _ = service.ownerLocked(value.id, value.token, "198.51.100.99")
		_, _ = service.ownerLocked("browser:client-supplied-unknown", "unknown-secret", value.address)
	})

	if count := strings.Count(logs, "event=owner_rejected"); count != 2 {
		t.Fatalf("owner rejection log count=%d, logs=%q", count, logs)
	}
	for _, expected := range []string{`reason="owner_token_mismatch"`, `reason="address_mismatch"`, "repeats=2", "rejection_count=4"} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("owner rejection logs missing %q: %q", expected, logs)
		}
	}
	for _, secret := range []string{"registered-owner-secret", "attacker-supplied-secret", "192.0.2.10", "198.51.100.99", "browser:client-supplied-unknown", "unknown-secret"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("owner rejection logs exposed %q: %q", secret, logs)
		}
	}
}

func TestLeaseExpiryLogIdentifiesStoppedHeartbeatAndLostPlayback(t *testing.T) {
	lastSeen := time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC)
	now := lastSeen.Add(20 * time.Second)
	value := &registration{
		id:        "browser:server-generated",
		expiresAt: lastSeen.Add(leaseDuration),
		lastSeen:  lastSeen,
		pending: &pendingCommand{
			command: Command{Sequence: 7, Action: "play", PlayID: "server-play-id"},
			result:  make(chan error, 1),
		},
		resource: &output.Resource{PlayID: "server-play-id"},
		observation: output.Observation{
			State: "playing", TransportStatus: "OK", PositionMS: 42000, PlayID: "server-play-id", CommandSequence: 6,
		},
		diagnostics: registrationDiagnostics{
			lastPoll:                 lastSeen.Add(2 * time.Second),
			lastRenew:                lastSeen.Add(3 * time.Second),
			lastReport:               lastSeen.Add(4 * time.Second),
			lastDeliveredSequence:    7,
			lastDeliveryAt:           lastSeen.Add(2 * time.Second),
			lastAcknowledgedSequence: 6,
			lastAcknowledgementAt:    lastSeen.Add(4 * time.Second),
		},
	}
	service := &Service{devices: map[string]*registration{value.id: value}}

	logs := captureDiagnosticLogs(t, func() {
		if !service.removeExpiredLocked(now) {
			t.Fatal("expired registration was not removed")
		}
	})

	for _, expected := range []string{
		"event=lease_expired",
		"lease_expired_age_ms=5000",
		"last_seen_age_ms=20000",
		"last_poll_age_ms=18000",
		"last_renew_age_ms=17000",
		"last_report_age_ms=16000",
		`play_id="server-play-id"`,
		"pending_sequence=7",
		`pending_action="play"`,
		"last_delivered_sequence=7",
		"last_acknowledged_sequence=6",
		`state="playing"`,
		"position_ms=42000",
		`event=command_cancelled renderer_id="browser:server-generated"`,
		`reason="lease_expired"`,
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("lease expiry logs missing %q: %q", expected, logs)
		}
	}
}

func testPlaybackError(stage string) PlaybackError {
	httpStatus := int64(503)
	platformCode := int64(-110)
	errorCode := int64(2004)
	occurredAtMS := int64(1789905600123)
	positionMS := int64(42000)
	return PlaybackError{
		Stage:        stage,
		ErrorCode:    &errorCode,
		ErrorName:    "ERROR_CODE_IO_BAD_HTTP_STATUS",
		OccurredAtMS: &occurredAtMS,
		PositionMS:   &positionMS,
		Causes: []PlaybackErrorCause{{
			Type:         "androidx.media3.datasource.HttpDataSource$InvalidResponseCodeException",
			Stack:        []string{"androidx.media3.exoplayer.ExoPlayerImplInternal#handleIoException:778", "java.lang.Thread#run:1012"},
			HTTPStatus:   &httpStatus,
			PlatformCode: &platformCode,
		}},
	}
}

func TestTerminalPlaybackErrorLogsBoundedNativeDetails(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)
	loaded, err := service.Observe(t.Context(), registration.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := Report{
		Sequence: loaded.CommandSequence,
		Observation: &ObservationReport{
			Event: "error", PlayID: loaded.PlayID, PositionMS: 42000,
		},
		PlaybackErrors: []PlaybackError{testPlaybackError("playback")},
	}
	logs := captureDiagnosticLogs(t, func() {
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", report); err != nil {
			t.Fatal(err)
		}
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", report); !errors.Is(err, ErrStaleReport) {
			t.Fatalf("terminal diagnostic retry error=%v", err)
		}
	})

	for _, expected := range []string{
		"event=native_playback_error",
		`renderer_id="` + registration.ID + `"`,
		`play_id="play-1"`,
		"sequence=1",
		`command_id="` + registration.ID + `/1"`,
		`playback_errors="[{\"stage\":\"playback\",\"error_code\":2004,\"error_name\":\"ERROR_CODE_IO_BAD_HTTP_STATUS\",\"occurred_at_ms\":1789905600123,\"position_ms\":42000`,
		`\"type\":\"androidx.media3.datasource.HttpDataSource$InvalidResponseCodeException\"`,
		`\"stack\":[\"androidx.media3.exoplayer.ExoPlayerImplInternal#handleIoException:778\",\"java.lang.Thread#run:1012\"]`,
		`\"http_status\":503`,
		`\"platform_code\":-110`,
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("native playback error log missing %q: %q", expected, logs)
		}
	}
	if count := strings.Count(logs, "event=native_playback_error"); count != 1 {
		t.Fatalf("native playback error log count=%d, logs=%q", count, logs)
	}
}

func TestFailedSetURIPlaybackErrorLogsOnceWithPendingPlayIdentity(t *testing.T) {
	service, registration := newTestService(t)
	result := make(chan error, 1)
	go func() {
		result <- service.SetURI(t.Context(), registration.ID, output.Resource{
			URL: "/media/grant", Mime: "audio/wav", TrackID: "track-failure",
			PlayID: "set-uri-failure-play", Seekable: true,
		})
	}()
	command := waitForCommand(t, service, registration)
	report := Report{
		Sequence: command.Sequence, Result: "failed", ErrorCode: "media_error",
		PlaybackErrors: []PlaybackError{testPlaybackError("prepare")},
	}
	unrelatedSuccess := Report{
		Sequence: command.Sequence, Result: "succeeded",
		Observation:    successfulObservation(command, "loaded"),
		PlaybackErrors: []PlaybackError{testPlaybackError("prepare")},
	}
	logs := captureDiagnosticLogs(t, func() {
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", unrelatedSuccess); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("successful report with diagnostics error=%v", err)
		}
		select {
		case err := <-result:
			t.Fatalf("rejected successful report resolved set_uri: %v", err)
		default:
		}
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", report); err != nil {
			t.Fatal(err)
		}
		if err := <-result; err == nil {
			t.Fatal("failed set_uri returned no action error")
		}
		report.PlaybackErrors[0].Causes[0].Stack[0] = "caller.Mutation#mustNotChangeAcceptedReport:1"
		retry := Report{
			Sequence: command.Sequence, Result: "failed", ErrorCode: "media_error",
			PlaybackErrors: []PlaybackError{testPlaybackError("prepare")},
		}
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", retry); err != nil {
			t.Fatalf("identical failed report retry: %v", err)
		}
	})

	for _, expected := range []string{
		`event=native_playback_error renderer_id="` + registration.ID + `"`,
		`play_id="set-uri-failure-play"`,
		"sequence=1",
		`command_id="` + registration.ID + `/1"`,
		`\"stage\":\"prepare\"`,
	} {
		if !strings.Contains(logs, expected) {
			t.Fatalf("failed set_uri diagnostic missing %q: %q", expected, logs)
		}
	}
	if count := strings.Count(logs, "event=native_playback_error"); count != 1 {
		t.Fatalf("duplicate failed report produced %d native diagnostics: %q", count, logs)
	}
	if strings.Contains(logs, "caller.Mutation") {
		t.Fatalf("caller mutation polluted accepted diagnostic: %q", logs)
	}
}

func TestRejectedPlaybackErrorsCannotInjectDiagnosticContent(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)
	loaded, err := service.Observe(t.Context(), registration.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := func(name string) Report {
		playbackError := testPlaybackError("playback")
		playbackError.ErrorName = name
		return Report{
			Sequence: loaded.CommandSequence,
			Observation: &ObservationReport{
				Event: "error", PlayID: loaded.PlayID, PositionMS: 42000,
			},
			PlaybackErrors: []PlaybackError{playbackError},
		}
	}

	logs := captureDiagnosticLogs(t, func() {
		if err := service.Report(registration.ID, "wrong-owner-token", "192.0.2.10", report("UNAUTHORIZED_CANARY")); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("unauthorized diagnostic error=%v", err)
		}
		malformed := report("MALICIOUS_CANARY")
		status := report("STATUS_CANARY")
		status.Observation.Event = "timeupdate"
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", status); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("status diagnostic error=%v", err)
		}
		malformed.PlaybackErrors[0].Causes[0].Stack[0] = "java.io.FileInputStream#open:/data/user/0/secret.mp3"
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", malformed); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("malformed diagnostic error=%v", err)
		}
		stale := report("STALE_CANARY")
		stale.Observation.PlayID = "unassociated-play"
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", stale); !errors.Is(err, ErrStaleReport) {
			t.Fatalf("stale diagnostic error=%v", err)
		}
		if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", report("ERROR_CODE_IO_BAD_HTTP_STATUS")); err != nil {
			t.Fatalf("valid terminal diagnostic: %v", err)
		}
	})

	if count := strings.Count(logs, "event=native_playback_error"); count != 1 {
		t.Fatalf("rejected reports produced native diagnostics, count=%d logs=%q", count, logs)
	}
	for _, rejected := range []string{"UNAUTHORIZED_CANARY", "STATUS_CANARY", "MALICIOUS_CANARY", "STALE_CANARY", "/data/user/0/secret.mp3", "unassociated-play"} {
		if strings.Contains(logs, rejected) {
			t.Fatalf("rejected diagnostic content %q reached logs: %q", rejected, logs)
		}
	}
}

func TestPlaybackErrorValidationLimits(t *testing.T) {
	valid := func() Report {
		return Report{
			Result:         "failed",
			PlaybackErrors: []PlaybackError{testPlaybackError("command")},
		}
	}
	int64Pointer := func(value int64) *int64 { return &value }
	cases := map[string]func(*Report){
		"unrelated context": func(report *Report) {
			report.Result = ""
			report.Observation = &ObservationReport{Event: "timeupdate"}
		},
		"too many errors": func(report *Report) {
			report.PlaybackErrors = append(report.PlaybackErrors, report.PlaybackErrors[0], report.PlaybackErrors[0], report.PlaybackErrors[0], report.PlaybackErrors[0])
		},
		"invalid stage":     func(report *Report) { report.PlaybackErrors[0].Stage = "recovery" },
		"missing name":      func(report *Report) { report.PlaybackErrors[0].ErrorName = "" },
		"missing code":      func(report *Report) { report.PlaybackErrors[0].ErrorCode = nil },
		"negative code":     func(report *Report) { report.PlaybackErrors[0].ErrorCode = int64Pointer(-1) },
		"oversized code":    func(report *Report) { report.PlaybackErrors[0].ErrorCode = int64Pointer(1000000) },
		"invalid name":      func(report *Report) { report.PlaybackErrors[0].ErrorName = "ERROR code" },
		"oversized name":    func(report *Report) { report.PlaybackErrors[0].ErrorName = strings.Repeat("E", 97) },
		"native mismatch":   func(report *Report) { report.PlaybackErrors[0].ErrorCode = int64Pointer(0) },
		"missing time":      func(report *Report) { report.PlaybackErrors[0].OccurredAtMS = nil },
		"nonpositive time":  func(report *Report) { report.PlaybackErrors[0].OccurredAtMS = int64Pointer(0) },
		"missing position":  func(report *Report) { report.PlaybackErrors[0].PositionMS = nil },
		"negative position": func(report *Report) { report.PlaybackErrors[0].PositionMS = int64Pointer(-1) },
		"oversized position": func(report *Report) {
			report.PlaybackErrors[0].PositionMS = int64Pointer(maximumMediaMS + 1)
		},
		"missing cause": func(report *Report) { report.PlaybackErrors[0].Causes = nil },
		"too many causes": func(report *Report) {
			cause := report.PlaybackErrors[0].Causes[0]
			report.PlaybackErrors[0].Causes = []PlaybackErrorCause{cause, cause, cause, cause, cause}
		},
		"invalid cause type": func(report *Report) { report.PlaybackErrors[0].Causes[0].Type = "java.io.IOException/path" },
		"oversized cause type": func(report *Report) {
			report.PlaybackErrors[0].Causes[0].Type = strings.Repeat("A", 161)
		},
		"too many frames": func(report *Report) {
			report.PlaybackErrors[0].Causes[0].Stack = []string{"a#b:1", "a#b:2", "a#b:3", "a#b:4", "a#b:5", "a#b:6", "a#b:7"}
		},
		"invalid frame": func(report *Report) { report.PlaybackErrors[0].Causes[0].Stack[0] = "a#b:file.mp3" },
		"missing stack": func(report *Report) { report.PlaybackErrors[0].Causes[0].Stack = nil },
		"oversized frame": func(report *Report) {
			report.PlaybackErrors[0].Causes[0].Stack[0] = strings.Repeat("a", 197) + "#b:1"
		},
		"invalid HTTP status": func(report *Report) { report.PlaybackErrors[0].Causes[0].HTTPStatus = int64Pointer(600) },
		"low HTTP status":     func(report *Report) { report.PlaybackErrors[0].Causes[0].HTTPStatus = int64Pointer(99) },
		"invalid platform code": func(report *Report) {
			report.PlaybackErrors[0].Causes[0].PlatformCode = int64Pointer(1 << 31)
		},
		"low platform code": func(report *Report) {
			report.PlaybackErrors[0].Causes[0].PlatformCode = int64Pointer(-1<<31 - 1)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			report := valid()
			mutate(&report)
			if err := validatePlaybackErrors(report); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("validation error=%v", err)
			}
		})
	}
	if err := validatePlaybackErrors(valid()); err != nil {
		t.Fatalf("valid playback error rejected: %v", err)
	}
	boundary := valid()
	boundaryError := &boundary.PlaybackErrors[0]
	boundaryError.ErrorCode = int64Pointer(999999)
	boundaryError.ErrorName = strings.Repeat("E", maximumPlaybackErrorName)
	boundaryError.PositionMS = int64Pointer(maximumMediaMS)
	boundaryCause := boundaryError.Causes[0]
	boundaryCause.Type = strings.Repeat("A", maximumPlaybackCauseType)
	boundaryCause.Stack = []string{
		strings.Repeat("a", 196) + "#b:1",
		"a#b:2", "a#b:3", "a#b:4", "a#b:5", "a#b:6",
	}
	boundaryCause.HTTPStatus = int64Pointer(599)
	boundaryCause.PlatformCode = int64Pointer(1<<31 - 1)
	boundaryError.Causes = []PlaybackErrorCause{boundaryCause, boundaryCause, boundaryCause, boundaryCause}
	boundary.PlaybackErrors = []PlaybackError{*boundaryError, *boundaryError, *boundaryError, *boundaryError}
	if err := validatePlaybackErrors(boundary); err != nil {
		t.Fatalf("maximum boundary playback error rejected: %v", err)
	}
	native := valid()
	native.PlaybackErrors[0].ErrorCode = int64Pointer(0)
	native.PlaybackErrors[0].ErrorName = "NATIVE_ERROR"
	native.PlaybackErrors[0].Causes[0].Stack = []string{}
	native.PlaybackErrors[0].Causes[0].HTTPStatus = nil
	native.PlaybackErrors[0].Causes[0].PlatformCode = nil
	if err := validatePlaybackErrors(native); err != nil {
		t.Fatalf("native error with empty stack rejected: %v", err)
	}
	if err := validatePlaybackErrors(Report{}); err != nil {
		t.Fatalf("empty legacy report diagnostic rejected: %v", err)
	}
}
