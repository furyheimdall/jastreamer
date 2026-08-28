package api_test

import (
	"context"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type k17CrashStage string

const (
	k17CrashAfterClaim k17CrashStage = "after_claim"
	k17CrashAfterURI   k17CrashStage = "after_uri"
	k17CrashAfterPlay  k17CrashStage = "after_play"
)

func verifyInterruptedK17Recovery(t *testing.T, stage k17CrashStage) {
	t.Helper()
	fixture := newK17NaturalDispatchFixture(t)
	identity := playback.K17DispatchIdentity{
		ZoneID: fixture.ended.ZoneID, CommandID: fixture.ended.Decision.ID, PlayID: fixture.ended.Decision.PlayID,
	}
	claim, err := fixture.transport.fixture.store.ClaimK17TransportDispatch(context.Background(), identity)
	if err != nil || claim != playback.K17DispatchClaimed {
		t.Fatalf("claim = %q, %v", claim, err)
	}
	switch stage {
	case k17CrashAfterClaim:
	case k17CrashAfterURI:
		if err := fixture.transport.adapter.SetAVTransportURI(context.Background(), playback.MediaResource{
			URL: "https://127.0.0.1/media/v1/interrupted", MimeType: "audio/flac",
		}); err != nil {
			t.Fatal(err)
		}
	case k17CrashAfterPlay:
		if err := fixture.transport.adapter.SetAVTransportURI(context.Background(), playback.MediaResource{
			URL: "https://127.0.0.1/media/v1/interrupted", MimeType: "audio/flac",
		}); err != nil {
			t.Fatal(err)
		}
		if err := fixture.transport.adapter.Play(context.Background()); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown crash stage %q", stage)
	}
	uriCalls, playCalls := fixture.transport.adapter.uriCalls, fixture.transport.adapter.playCalls
	if err := fixture.transport.fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := playback.Open(context.Background(), fixture.transport.fixture.playbackConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restarted.Close(); err != nil {
			t.Errorf("close restarted store: %v", err)
		}
	})
	dispatcher := api.NewK17LifecycleDispatcher(api.K17LifecycleDispatcherConfig{
		Queue: restarted, Media: fixture.transport.mediaService,
		Provider: transportK17UPnP{adapter: fixture.transport.adapter}, BaseURL: fixture.server.URL,
	})

	if err := dispatcher.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}

	if fixture.transport.adapter.uriCalls != uriCalls || fixture.transport.adapter.playCalls != playCalls {
		t.Fatalf("recovery repeated SOAP = uri %d/%d play %d/%d", fixture.transport.adapter.uriCalls, uriCalls, fixture.transport.adapter.playCalls, playCalls)
	}
	pending, err := restarted.PendingOutbox(context.Background(), fixture.ended.ZoneID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := restarted.Snapshot(context.Background(), fixture.ended.ZoneID)
	if err != nil {
		t.Fatal(err)
	}
	command, err := restarted.DurableCommand(context.Background(), fixture.ended.Decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 || snapshot.Transport != playback.TransportSuspended || snapshot.CurrentPlay != fixture.ended.Decision.PlayID ||
		command.ReceiptState != playback.CommandReceiptTerminal || command.LastErrorCode != "ADAPTER_FAILURE" {
		t.Fatalf("recovered cut point = pending %+v snapshot %+v command %+v", pending, snapshot, command)
	}
	if snapshot.Queue[0].State != playback.QueueCompleted || snapshot.Queue[1].State != playback.QueueReserved {
		t.Fatalf("recovery double advanced = %+v", snapshot.Queue)
	}
	stopped, err := restarted.MutateTransport(context.Background(), playback.TransportMutationRequest{
		ZoneID: fixture.ended.ZoneID, IdempotencyKey: "recover-stop-" + string(stage),
		ExpectedRevision: snapshot.Revision, Command: playback.TransportStop,
	})
	if err != nil {
		t.Fatalf("user stop after recovery: %v", err)
	}
	retried, err := restarted.MutateTransport(context.Background(), playback.TransportMutationRequest{
		ZoneID: fixture.ended.ZoneID, IdempotencyKey: "recover-start-" + string(stage),
		ExpectedRevision: stopped.Revision, Command: playback.TransportStart,
	})
	if err != nil || retried.PlayID == fixture.ended.Decision.PlayID {
		t.Fatalf("user retry = %+v, %v", retried, err)
	}
}

func Test_K17Lifecycle_restart_after_claim_before_URI_suspends_without_SOAP_retry(t *testing.T) {
	// Given/When/Then
	verifyInterruptedK17Recovery(t, k17CrashAfterClaim)
}

func Test_K17Lifecycle_restart_after_URI_before_Play_suspends_without_SOAP_retry(t *testing.T) {
	// Given/When/Then
	verifyInterruptedK17Recovery(t, k17CrashAfterURI)
}

func Test_K17Lifecycle_restart_after_Play_before_completion_suspends_without_SOAP_retry(t *testing.T) {
	// Given/When/Then
	verifyInterruptedK17Recovery(t, k17CrashAfterPlay)
}
