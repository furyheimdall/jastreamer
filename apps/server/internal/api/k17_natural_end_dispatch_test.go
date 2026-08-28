package api_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/api"
	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type k17NaturalDispatchFixture struct {
	transport  transportMediaFixture
	server     *httptest.Server
	dispatcher *api.K17LifecycleDispatcher
	ended      playback.K17LifecycleResult
	uriCalls   int
	playCalls  int
}

func newK17NaturalDispatchFixture(t *testing.T) k17NaturalDispatchFixture {
	t.Helper()
	transport := newTransportMediaFixture(t, playback.RendererKindK17)
	server := httptest.NewTLSServer(transport.handler(api.Config{}))
	t.Cleanup(server.Close)
	startTransport(t, server, transport.controller)
	playing := playback.K17Observation{
		RendererID: transport.rendererID, ZoneID: "transport", Transport: "PLAYING", Position: time.Second,
		CurrentURI: transport.adapter.mediaURL, Owned: true, ObservedAt: time.Date(2026, 8, 26, 1, 0, 0, 0, time.UTC),
	}
	if _, err := transport.fixture.store.ApplyK17Observation(context.Background(), playing); err != nil {
		t.Fatal(err)
	}
	stopped := playing
	stopped.Transport = "STOPPED"
	stopped.ObservedAt = stopped.ObservedAt.Add(time.Second)
	ended, err := transport.fixture.store.ApplyK17Observation(context.Background(), stopped)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := api.NewK17LifecycleDispatcher(api.K17LifecycleDispatcherConfig{
		Queue: transport.fixture.store, Media: transport.mediaService,
		Provider: transportK17UPnP{adapter: transport.adapter}, BaseURL: server.URL,
	})
	return k17NaturalDispatchFixture{
		transport: transport, server: server, dispatcher: dispatcher, ended: ended,
		uriCalls: transport.adapter.uriCalls, playCalls: transport.adapter.playCalls,
	}
}

func Test_K17Lifecycle_success_terminalizes_dispatch_without_fabricating_observation(t *testing.T) {
	// Given
	fixture := newK17NaturalDispatchFixture(t)

	// When
	err := fixture.dispatcher.Dispatch(context.Background(), fixture.ended)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	pending, err := fixture.transport.fixture.store.PendingOutbox(context.Background(), "transport")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := fixture.transport.fixture.store.Snapshot(context.Background(), "transport")
	if err != nil {
		t.Fatal(err)
	}
	truth, err := fixture.transport.fixture.store.RendererSessionTruth(context.Background(), fixture.transport.rendererID)
	if err != nil {
		t.Fatal(err)
	}
	command, err := fixture.transport.fixture.store.DurableCommand(context.Background(), fixture.ended.Decision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 || snapshot.Transport != playback.TransportStarting || truth.ObservedState != "stopped" || command.ReceiptState != playback.CommandReceiptTerminal || command.LastErrorCode != "" {
		t.Fatalf("success state = pending %+v snapshot %+v truth %+v command %+v", pending, snapshot, truth, command)
	}
	pullTransportMedia(t, fixture.server.Client(), fixture.transport.adapter.mediaURL)
}

func assertK17DispatchFailure(t *testing.T, fixture k17NaturalDispatchFixture, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("dispatch succeeded")
	}
	pending, pendingErr := fixture.transport.fixture.store.PendingOutbox(context.Background(), "transport")
	if pendingErr != nil {
		t.Fatal(pendingErr)
	}
	snapshot, snapshotErr := fixture.transport.fixture.store.Snapshot(context.Background(), "transport")
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	command, commandErr := fixture.transport.fixture.store.DurableCommand(context.Background(), fixture.ended.Decision.ID)
	if commandErr != nil {
		t.Fatal(commandErr)
	}
	if len(pending) != 0 || snapshot.Transport != playback.TransportSuspended || command.ReceiptState != playback.CommandReceiptTerminal || command.LastErrorCode != "ADAPTER_FAILURE" {
		t.Fatalf("failure state = pending %+v snapshot %+v command %+v", pending, snapshot, command)
	}
}

func Test_K17Lifecycle_URI_failure_suspends_and_terminalizes(t *testing.T) {
	// Given
	fixture := newK17NaturalDispatchFixture(t)
	fixture.transport.adapter.uriError = errors.New("fixture URI failure")

	// When
	err := fixture.dispatcher.Dispatch(context.Background(), fixture.ended)

	// Then
	assertK17DispatchFailure(t, fixture, err)
	if fixture.transport.adapter.uriCalls != fixture.uriCalls+1 || fixture.transport.adapter.playCalls != fixture.playCalls {
		t.Fatalf("URI failure calls = uri %d play %d", fixture.transport.adapter.uriCalls, fixture.transport.adapter.playCalls)
	}
}

func Test_K17Lifecycle_Play_failure_suspends_and_terminalizes(t *testing.T) {
	// Given
	fixture := newK17NaturalDispatchFixture(t)
	fixture.transport.adapter.playError = errors.New("fixture Play failure")

	// When
	err := fixture.dispatcher.Dispatch(context.Background(), fixture.ended)

	// Then
	assertK17DispatchFailure(t, fixture, err)
	if fixture.transport.adapter.uriCalls != fixture.uriCalls+1 || fixture.transport.adapter.playCalls != fixture.playCalls+1 {
		t.Fatalf("Play failure calls = uri %d play %d", fixture.transport.adapter.uriCalls, fixture.transport.adapter.playCalls)
	}
}

func Test_K17Lifecycle_pending_recovery_dispatches_once(t *testing.T) {
	// Given
	fixture := newK17NaturalDispatchFixture(t)

	// When
	if err := fixture.dispatcher.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := fixture.dispatcher.RecoverPending(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Then
	if fixture.transport.adapter.uriCalls != fixture.uriCalls+1 || fixture.transport.adapter.playCalls != fixture.playCalls+1 {
		t.Fatalf("recovery SOAP calls = uri %d play %d", fixture.transport.adapter.uriCalls, fixture.transport.adapter.playCalls)
	}
}

func Test_K17Lifecycle_duplicate_result_does_not_repeat_SOAP(t *testing.T) {
	// Given
	fixture := newK17NaturalDispatchFixture(t)
	if err := fixture.dispatcher.Dispatch(context.Background(), fixture.ended); err != nil {
		t.Fatal(err)
	}

	// When
	err := fixture.dispatcher.Dispatch(context.Background(), fixture.ended)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if fixture.transport.adapter.uriCalls != fixture.uriCalls+1 || fixture.transport.adapter.playCalls != fixture.playCalls+1 {
		t.Fatalf("duplicate SOAP calls = uri %d play %d", fixture.transport.adapter.uriCalls, fixture.transport.adapter.playCalls)
	}
}
