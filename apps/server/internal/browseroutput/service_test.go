package browseroutput

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/library"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

type testMedia struct{}

func (testMedia) Prepare(context.Context, output.Device, library.Track, string) (output.Resource, error) {
	return output.Resource{}, nil
}

func (testMedia) Revoke(string) {}

func newTestService(t *testing.T) (*Service, Registration) {
	t.Helper()
	service, err := New(testMedia{}, func(string) {}, nil)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := service.Register("192.0.2.10", "Test browser", []string{"audio/wav"})
	if err != nil {
		t.Fatal(err)
	}
	return service, registration
}

func waitForCommand(t *testing.T, service *Service, registration Registration) Command {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		poll, err := service.Poll(registration.ID, registration.OwnerToken, "192.0.2.10")
		if err != nil {
			t.Fatal(err)
		}
		if poll.Command != nil {
			return *poll.Command
		}
		runtime.Gosched()
	}
	t.Fatal("browser command was not dispatched")
	return Command{}
}

func successfulObservation(command Command, event string) *ObservationReport {
	return &ObservationReport{
		Event: event, PlayID: command.PlayID, State: "playing",
		PositionMS: 1000, DurationMS: 3000, HasPosition: true,
	}
}

func setTestResource(t *testing.T, service *Service, registration Registration) {
	t.Helper()
	result := make(chan error, 1)
	go func() {
		result <- service.SetURI(context.Background(), registration.ID, output.Resource{
			URL: "/media/grant", Mime: "audio/wav", TrackID: "track-1", PlayID: "play-1", Seekable: true,
		})
	}()
	command := waitForCommand(t, service, registration)
	if command.Action != "set_uri" {
		t.Fatalf("action=%q, want set_uri", command.Action)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Result: "succeeded", Observation: successfulObservation(command, "loaded"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestOwnerCredentialAndAddressAreBothRequired(t *testing.T) {
	service, registration := newTestService(t)
	if _, err := service.Renew(registration.ID, "wrong-owner-token", "192.0.2.10"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token error=%v", err)
	}
	if _, err := service.Renew(registration.ID, registration.OwnerToken, "192.0.2.11"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong address error=%v", err)
	}
	if _, err := service.Renew(registration.ID, registration.OwnerToken, "192.0.2.10"); err != nil {
		t.Fatalf("owner renewal: %v", err)
	}
}

func TestRenameRequiresOwnerAndPreservesPendingPlayback(t *testing.T) {
	service, registration := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- service.SetURI(ctx, registration.ID, output.Resource{
			URL: "/media/grant", Mime: "audio/wav", TrackID: "track-1", PlayID: "play-1", Seekable: true,
		})
	}()
	command := waitForCommand(t, service, registration)
	for _, credential := range []struct{ token, address string }{
		{"wrong-owner-token", "192.0.2.10"},
		{registration.OwnerToken, "192.0.2.11"},
	} {
		if _, err := service.Rename(registration.ID, credential.token, credential.address, "Not mine"); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("unauthorized rename: %v", err)
		}
	}
	before, ok := service.Device(registration.ID)
	if !ok || before.Name != "Test browser" {
		t.Fatalf("unauthorized rename changed the output: %#v", before)
	}
	device, err := service.Rename(registration.ID, registration.OwnerToken, "192.0.2.10", "  작업실 PC  ")
	if err != nil || device.Name != "작업실 PC" || device.ID != registration.ID {
		t.Fatalf("rename result: %#v, %v", device, err)
	}
	after := waitForCommand(t, service, registration)
	if after.Sequence != command.Sequence || after.Action != command.Action || after.PlayID != command.PlayID {
		t.Fatalf("rename replaced pending playback: before=%#v after=%#v", command, after)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Result: "succeeded", Observation: successfulObservation(command, "loaded"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatalf("playback command failed after rename: %v", err)
	}
}

func TestStaleReportCannotCompleteCurrentCommand(t *testing.T) {
	service, registration := newTestService(t)
	result := make(chan error, 1)
	go func() {
		result <- service.SetURI(context.Background(), registration.ID, output.Resource{
			URL: "/media/grant", Mime: "audio/wav", TrackID: "track-1", PlayID: "play-1", Seekable: true,
		})
	}()
	command := waitForCommand(t, service, registration)
	stale := Report{Sequence: command.Sequence + 1, Result: "succeeded", Observation: successfulObservation(command, "loaded")}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", stale); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("stale report error=%v", err)
	}
	select {
	case err := <-result:
		t.Fatalf("stale report completed command: %v", err)
	default:
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Result: "succeeded", Observation: successfulObservation(command, "loaded"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestAcceptedReportRetryDoesNotReplayOrRollBack(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)

	playResult := make(chan error, 1)
	go func() { playResult <- service.Play(t.Context(), registration.ID) }()
	playCommand := waitForCommand(t, service, registration)
	acceptedObservation := successfulObservation(playCommand, "playing")
	accepted := Report{
		Sequence: playCommand.Sequence, Result: "succeeded", Observation: acceptedObservation,
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", accepted); err != nil {
		t.Fatal(err)
	}
	if err := <-playResult; err != nil {
		t.Fatal(err)
	}

	// The accepted payload must be copied rather than retained through its caller-owned pointer.
	acceptedObservation.PositionMS = 999
	laterEvidence := successfulObservation(playCommand, "timeupdate")
	laterEvidence.PositionMS = 2000
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: playCommand.Sequence, Observation: laterEvidence,
	}); err != nil {
		t.Fatal(err)
	}

	pauseResult := make(chan error, 1)
	go func() { pauseResult <- service.Pause(t.Context(), registration.ID) }()
	pauseCommand := waitForCommand(t, service, registration)
	retry := Report{
		Sequence: playCommand.Sequence, Result: "succeeded", Observation: successfulObservation(playCommand, "playing"),
	}
	for _, owner := range []struct {
		token   string
		address string
	}{
		{token: "wrong-owner-token", address: "192.0.2.10"},
		{token: registration.OwnerToken, address: "192.0.2.11"},
	} {
		if err := service.Report(registration.ID, owner.token, owner.address, retry); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("unauthorized retry error=%v", err)
		}
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", retry); err != nil {
		t.Fatalf("identical retry error=%v", err)
	}
	changed := retry
	changedObservation := *retry.Observation
	changedObservation.PositionMS++
	changed.Observation = &changedObservation
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", changed); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("changed retry error=%v", err)
	}
	select {
	case err := <-pauseResult:
		t.Fatalf("old retry resolved newer command: %v", err)
	default:
	}
	observed, err := service.Observe(t.Context(), registration.ID)
	if err != nil || observed.PositionMS != laterEvidence.PositionMS || observed.CommandSequence != playCommand.Sequence {
		t.Fatalf("old retry replaced newer evidence: %+v, %v", observed, err)
	}

	pauseReport := Report{
		Sequence: pauseCommand.Sequence, Result: "succeeded", Observation: successfulObservation(pauseCommand, "pause"),
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", pauseReport); err != nil {
		t.Fatal(err)
	}
	if err := <-pauseResult; err != nil {
		t.Fatal(err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", retry); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("superseded retry error=%v", err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", pauseReport); err != nil {
		t.Fatalf("latest identical retry error=%v", err)
	}
}

func TestAcceptedFailureRetryHonorsCancellationFence(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)

	playResult := make(chan error, 1)
	go func() { playResult <- service.Play(t.Context(), registration.ID) }()
	playCommand := waitForCommand(t, service, registration)
	failed := Report{Sequence: playCommand.Sequence, Result: "failed", ErrorCode: "action_failed"}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", failed); err != nil {
		t.Fatal(err)
	}
	var actionError *output.ActionError
	if err := <-playResult; !errors.As(err, &actionError) || actionError.Kind != output.ErrorFault {
		t.Fatalf("failed command error=%v", err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", failed); err != nil {
		t.Fatalf("identical failed retry error=%v", err)
	}
	changed := failed
	changed.ErrorCode = "media_error"
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", changed); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("changed failed retry error=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	pauseResult := make(chan error, 1)
	go func() { pauseResult <- service.Pause(ctx, registration.ID) }()
	cancelledCommand := waitForCommand(t, service, registration)
	cancel()
	if err := <-pauseResult; !errors.As(err, &actionError) || actionError.Kind != output.ErrorCancelled {
		t.Fatalf("cancelled command error=%v", err)
	}
	if cancelledCommand.Sequence <= failed.Sequence {
		t.Fatalf("cancelled sequence=%d, accepted sequence=%d", cancelledCommand.Sequence, failed.Sequence)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", failed); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("retry behind cancellation fence error=%v", err)
	}
}

func TestLeaseLossCancelsPendingCommandAndRemovesDevice(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)
	now := registration.LeaseExpiresAt.Add(-leaseDuration)
	service.now = func() time.Time { return now }

	result := make(chan error, 1)
	go func() { result <- service.Play(context.Background(), registration.ID) }()
	_ = waitForCommand(t, service, registration)
	now = now.Add(leaseDuration + time.Second)
	service.expire(now)
	var actionError *output.ActionError
	if err := <-result; !errors.As(err, &actionError) || actionError.Kind != output.ErrorTransport {
		t.Fatalf("lease cancellation error=%v", err)
	}
	if _, found := service.Device(registration.ID); found {
		t.Fatal("expired browser output remained discoverable")
	}
}

func TestCancelledCommandCannotCompleteOrContaminateNextCommand(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)

	ctx, cancel := context.WithCancel(context.Background())
	cancelledResult := make(chan error, 1)
	go func() { cancelledResult <- service.Play(ctx, registration.ID) }()
	cancelledCommand := waitForCommand(t, service, registration)
	cancel()
	var actionError *output.ActionError
	if err := <-cancelledResult; !errors.As(err, &actionError) || actionError.Kind != output.ErrorCancelled {
		t.Fatalf("cancelled command error=%v", err)
	}
	poll, err := service.Poll(registration.ID, registration.OwnerToken, "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	if poll.CancelBeforeSequence < cancelledCommand.Sequence {
		t.Fatalf("cancel_before_sequence=%d, want at least %d", poll.CancelBeforeSequence, cancelledCommand.Sequence)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: cancelledCommand.Sequence, Observation: successfulObservation(cancelledCommand, "timeupdate"),
	}); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("cancelled observation error=%v", err)
	}

	nextResult := make(chan error, 1)
	go func() { nextResult <- service.Pause(context.Background(), registration.ID) }()
	nextCommand := waitForCommand(t, service, registration)
	if nextCommand.Sequence <= cancelledCommand.Sequence {
		t.Fatalf("next sequence=%d, cancelled sequence=%d", nextCommand.Sequence, cancelledCommand.Sequence)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: cancelledCommand.Sequence, Result: "succeeded", Observation: successfulObservation(cancelledCommand, "playing"),
	}); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("cancelled completion error=%v", err)
	}
	select {
	case err := <-nextResult:
		t.Fatalf("old completion resolved new command: %v", err)
	default:
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: nextCommand.Sequence, Result: "succeeded", Observation: successfulObservation(nextCommand, "pause"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-nextResult; err != nil {
		t.Fatal(err)
	}
}

func TestNaturalCompletionCannotBeOverwrittenByDelayedPosition(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)
	result := make(chan error, 1)
	go func() { result <- service.Play(t.Context(), registration.ID) }()
	command := waitForCommand(t, service, registration)
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Result: "succeeded", Observation: successfulObservation(command, "playing"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Observation: successfulObservation(command, "ended"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Observation: successfulObservation(command, "timeupdate"),
	}); !errors.Is(err, ErrStaleReport) {
		t.Fatalf("late position error=%v", err)
	}
	now := time.Now()
	service.now = func() time.Time { return now }
	now = now.Add(leaseDuration - time.Second)
	if _, err := service.Renew(registration.ID, registration.OwnerToken, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	observed, err := service.Observe(t.Context(), registration.ID)
	if err != nil || !observed.CompletionKnown || !observed.Completed || observed.State != "stopped" {
		t.Fatalf("natural completion lost: %+v, %v", observed, err)
	}
}

func TestLiveControlLeaseDoesNotInventFreshMediaObservation(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)
	now := time.Now()
	service.now = func() time.Time { return now }
	now = now.Add(leaseDuration - time.Second)
	if _, err := service.Renew(registration.ID, registration.OwnerToken, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	var actionError *output.ActionError
	if _, err := service.Observe(t.Context(), registration.ID); !errors.As(err, &actionError) || actionError.Kind != output.ErrorTimeout {
		t.Fatalf("stale media observation error=%v", err)
	}
}

func TestTerminalErrorSurvivesSlowPlayerPolling(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)
	loaded, err := service.Observe(t.Context(), registration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence:    loaded.CommandSequence,
		Observation: &ObservationReport{Event: "error", PlayID: loaded.PlayID},
	}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	service.now = func() time.Time { return now }
	now = now.Add(leaseDuration - time.Second)
	if _, err := service.Renew(registration.ID, registration.OwnerToken, "192.0.2.10"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Second)
	observed, err := service.Observe(t.Context(), registration.ID)
	if err != nil || observed.TransportStatus != "ERROR_OCCURRED" || observed.State != "stopped" {
		t.Fatalf("confirmed error lost: %+v, %v", observed, err)
	}
}

func TestMediaFailedCommandReportMapsOnlyEligibleActions(t *testing.T) {
	service, registration := newTestService(t)
	result := make(chan error, 1)
	go func() {
		result <- service.SetURI(t.Context(), registration.ID, output.Resource{
			URL: "/media/grant", Mime: "audio/wav", TrackID: "track-1", PlayID: "play-1", Seekable: true,
		})
	}()
	command := waitForCommand(t, service, registration)
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Result: "failed", ErrorCode: "media_failed",
		Observation: &ObservationReport{Event: "error", PlayID: command.PlayID},
	}); err != nil {
		t.Fatal(err)
	}
	var actionError *output.ActionError
	if err := <-result; !errors.As(err, &actionError) || actionError.Kind != output.ErrorMedia {
		t.Fatalf("media failure error=%v", err)
	}
	observed, err := service.Observe(t.Context(), registration.ID)
	if err != nil || !observed.MediaFailed || observed.Completed {
		t.Fatalf("media failure mapping=%+v, %v", observed, err)
	}

	setTestResource(t, service, registration)
	playResult := make(chan error, 1)
	go func() { playResult <- service.Play(t.Context(), registration.ID) }()
	play := waitForCommand(t, service, registration)
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: play.Sequence, Result: "failed", ErrorCode: "media_failed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-playResult; !errors.As(err, &actionError) || actionError.Kind != output.ErrorMedia {
		t.Fatalf("play media failure error=%v", err)
	}

	pauseResult := make(chan error, 1)
	go func() { pauseResult <- service.Pause(t.Context(), registration.ID) }()
	pause := waitForCommand(t, service, registration)
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: pause.Sequence, Result: "failed", ErrorCode: "media_failed",
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("pause media_failed error=%v", err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: pause.Sequence, Result: "failed", ErrorCode: "action_failed",
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-pauseResult; !errors.As(err, &actionError) || actionError.Kind != output.ErrorFault {
		t.Fatalf("pause fallback error=%v", err)
	}
}

func TestMediaFailedObservationRequiresErrorEvent(t *testing.T) {
	service, registration := newTestService(t)
	setTestResource(t, service, registration)
	loaded, err := service.Observe(t.Context(), registration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: loaded.CommandSequence, ErrorCode: "media_failed",
		Observation: &ObservationReport{Event: "timeupdate", PlayID: loaded.PlayID, State: "playing"},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("non-error media_failed observation error=%v", err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: loaded.CommandSequence, ErrorCode: "media_error",
		Observation: &ObservationReport{Event: "error", PlayID: loaded.PlayID},
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("generic unsolicited error code error=%v", err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: loaded.CommandSequence, ErrorCode: "media_failed",
		Observation: &ObservationReport{Event: "error", PlayID: loaded.PlayID},
	}); err != nil {
		t.Fatal(err)
	}
	observed, err := service.Observe(t.Context(), registration.ID)
	if err != nil || !observed.MediaFailed || observed.TransportStatus != "ERROR_OCCURRED" {
		t.Fatalf("terminal media failure observation=%+v, %v", observed, err)
	}
}

func TestSuccessfulReportRejectsErrorCode(t *testing.T) {
	service, registration := newTestService(t)
	result := make(chan error, 1)
	go func() {
		result <- service.SetURI(t.Context(), registration.ID, output.Resource{
			URL: "/media/grant", Mime: "audio/wav", TrackID: "track-1", PlayID: "play-1", Seekable: true,
		})
	}()
	command := waitForCommand(t, service, registration)
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Result: "succeeded", ErrorCode: "media_failed",
		Observation: successfulObservation(command, "loaded"),
	}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("successful media_failed report error=%v", err)
	}
	if err := service.Report(registration.ID, registration.OwnerToken, "192.0.2.10", Report{
		Sequence: command.Sequence, Result: "succeeded", Observation: successfulObservation(command, "loaded"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}
