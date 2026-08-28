package api_test

import (
	"slices"
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

func Test_RendererSession_happy_path_advances_only_after_acknowledged_natural_end(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 2)
	socket, welcome := scenario.openSocket(t, 0, nil)
	firstCommand := socket.readApplicationFrame(t)
	physicalStarts := 0

	// When
	socket.sendCommandAck(t, firstCommand, "received")
	physicalStarts++
	socket.sendResult(t, rendererResultInput{
		command: firstCommand, resultID: "result-first", status: "succeeded", observedState: "playing",
	})
	resultAck := socket.readApplicationFrame(t)
	socket.sendPlaybackEvent(t, rendererEventInput{
		epoch: welcome.SessionEpoch, eventID: "event-natural-end", playID: *firstCommand.PlayID, kind: "ended",
	})
	secondCommand := socket.readApplicationFrame(t)
	snapshot, err := scenario.fixture.store.Snapshot(t.Context(), scenario.zoneID)
	renderer, rendererErr := scenario.fixture.store.Renderer(
		t.Context(), playback.RendererID(scenario.renderer.Device.ID),
	)

	// Then
	if firstCommand.Type != "command" || firstCommand.Kind != "play" || firstCommand.Sequence != 1 ||
		firstCommand.PlayID == nil || firstCommand.Deadline == "" {
		t.Fatalf("first renderer command = %+v", firstCommand)
	}
	if resultAck.Type != "result.ack" || resultAck.ResultID != "result-first" {
		t.Fatalf("terminal result acknowledgement = %+v", resultAck)
	}
	if secondCommand.Type != "command" || secondCommand.Kind != "play" ||
		secondCommand.Sequence != firstCommand.Sequence+1 || secondCommand.CommandID == firstCommand.CommandID {
		t.Fatalf("serialized second renderer command = %+v", secondCommand)
	}
	if err != nil || rendererErr != nil {
		t.Fatalf("load post-end queue/renderer: %v / %v", err, rendererErr)
	}
	if renderer.ProtocolMajor != 3 || !slices.Contains(renderer.Capabilities, "command:play") ||
		!slices.Contains(renderer.Capabilities, "media:audio/flac") {
		t.Fatalf("persisted renderer hello = %+v", renderer)
	}
	if physicalStarts != 1 || len(snapshot.Queue) != 2 || snapshot.Queue[0].State != "completed" ||
		snapshot.Queue[1].State != "reserved" {
		t.Fatalf("physical starts/queue = %d / %+v", physicalStarts, snapshot.Queue)
	}
}

func Test_RendererSession_retries_unacknowledged_command_with_same_identity(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 1)
	socket, _ := scenario.openSocket(t, 0, nil)
	first := socket.readApplicationFrame(t)

	// When
	retried := socket.readApplicationFrame(t)
	socket.sendCommandAck(t, retried, "received")
	socket.sendResult(t, rendererResultInput{
		command: retried, resultID: "result-after-retry", status: "succeeded", observedState: "playing",
	})
	resultAck := socket.readApplicationFrame(t)

	// Then
	if retried.CommandID != first.CommandID || retried.Sequence != first.Sequence ||
		retried.Kind != first.Kind || resultAck.ResultID != "result-after-retry" {
		t.Fatalf("bounded retry changed identity: first=%+v retry=%+v ack=%+v", first, retried, resultAck)
	}
	durable, err := scenario.fixture.store.DurableCommand(t.Context(), first.CommandID)
	if err != nil || durable.Attempts != 2 || durable.ReceiptState != playback.CommandReceiptTerminal {
		t.Fatalf("retry state = %+v (%v)", durable, err)
	}
}

func Test_RendererSession_stale_epoch_is_fenced_without_queue_advance(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 1)
	socket, _ := scenario.openSocket(t, 0, nil)
	command := socket.readApplicationFrame(t)

	// When
	socket.sendPlaybackEvent(t, rendererEventInput{
		epoch: "stale-epoch", eventID: "event-stale", playID: *command.PlayID, kind: "ended",
	})
	protocolError := socket.readApplicationFrame(t)
	snapshot, err := scenario.fixture.store.Snapshot(t.Context(), scenario.zoneID)

	// Then
	if protocolError.Type != "error" || protocolError.Code != "STALE_SESSION_EPOCH" {
		t.Fatalf("stale epoch response = %+v", protocolError)
	}
	if err != nil {
		t.Fatalf("load queue after stale event: %v", err)
	}
	if snapshot.Queue[0].State != "reserved" || snapshot.CurrentPlay != scenario.first.PlayID {
		t.Fatalf("stale epoch advanced queue: %+v", snapshot)
	}
}

func Test_RendererSession_reconnect_without_observation_suspends_instead_of_resuming(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 1)
	first, _ := scenario.openSocket(t, 0, nil)
	command := first.readApplicationFrame(t)
	if err := first.connection.Close(); err != nil {
		t.Fatalf("disconnect ambiguous renderer: %v", err)
	}

	// When
	reconnected, _ := scenario.openSocket(t, 0, nil)
	redelivered := reconnected.readApplicationFrame(t)
	truth, err := scenario.fixture.store.RendererSessionTruth(t.Context(), playback.RendererID(scenario.renderer.Device.ID))

	// Then
	if redelivered.CommandID != command.CommandID || redelivered.Sequence != command.Sequence {
		t.Fatalf("ambiguous reconnect changed command: first=%+v replay=%+v", command, redelivered)
	}
	if err != nil {
		t.Fatalf("load ambiguous renderer truth: %v", err)
	}
	if truth.IntentTransport != playback.TransportSuspended || truth.ObservedState != "unknown" {
		t.Fatalf("ambiguous reconnect guessed physical state: %+v", truth)
	}
}

func Test_RendererSession_stop_survives_disconnect_and_supersedes_unstarted_play(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 1)
	first, _ := scenario.openSocket(t, 0, nil)
	play := first.readApplicationFrame(t)
	if err := scenario.fixture.store.Stop(t.Context(), scenario.zoneID); err != nil {
		t.Fatalf("commit stop while renderer is connected: %v", err)
	}
	if err := first.connection.Close(); err != nil {
		t.Fatalf("disconnect before stop delivery: %v", err)
	}

	// When
	reconnected, _ := scenario.openSocket(t, play.Sequence, nil)
	stop := reconnected.readApplicationFrame(t)

	// Then
	if stop.Type != "command" || stop.Kind != "stop" || stop.PlayID == nil ||
		*stop.PlayID != *play.PlayID || stop.Sequence != play.Sequence+1 {
		t.Fatalf("reconnected stop = %+v, play = %+v", stop, play)
	}
}

func Test_RendererSession_ping_is_bounded_control_traffic(t *testing.T) {
	// Given
	scenario := newRendererSessionScenario(t, 0)
	socket, _ := scenario.openSocket(t, 0, nil)
	payload := []byte("heartbeat")

	// When
	socket.sendFrame(t, 0x9, payload)
	opcode, echoed := socket.readFrame(t)

	// Then
	if opcode != 0xa || string(echoed) != string(payload) {
		t.Fatalf("renderer heartbeat response = %#x %q", opcode, echoed)
	}
}
