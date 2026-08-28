package api_test

import (
	"testing"

	"github.com/jastreamer/jastreamer-server/internal/playback"
)

type rendererCrashStage string

const (
	crashBeforeReceipt   rendererCrashStage = "before receipt"
	crashAfterReceipt    rendererCrashStage = "after receipt"
	crashAfterStart      rendererCrashStage = "after physical start"
	crashAfterResult     rendererCrashStage = "after result"
	crashBeforeResultAck rendererCrashStage = "before result acknowledgement"
)

type fixtureRendererJournal struct {
	commandID      string
	sequence       int64
	received       bool
	physicalStarts int
	pendingResult  *rendererPendingResult
}

func Test_RendererSession_crash_matrix_never_duplicates_physical_start_or_queue_consumption(t *testing.T) {
	stages := []rendererCrashStage{
		crashBeforeReceipt, crashAfterReceipt, crashAfterStart,
		crashAfterResult, crashBeforeResultAck,
	}
	for _, stage := range stages {
		t.Run(string(stage), func(t *testing.T) {
			// Given
			scenario := newRendererSessionScenario(t, 1)
			firstSocket, _ := scenario.openSocket(t, 0, nil)
			command := firstSocket.readApplicationFrame(t)
			journal := fixtureRendererJournal{commandID: command.CommandID, sequence: command.Sequence}

			// When
			cutRendererSession(t, rendererCrashCut{
				stage: stage, socket: firstSocket, command: command, journal: &journal,
			})
			if err := firstSocket.connection.Close(); err != nil {
				t.Fatalf("disconnect at %s: %v", stage, err)
			}
			pending := []rendererPendingResult(nil)
			if journal.pendingResult != nil {
				pending = append(pending, *journal.pendingResult)
			}
			cursor := int64(0)
			if journal.received {
				cursor = journal.sequence
			}
			reconnected, _ := scenario.openSocket(t, cursor, pending)
			if journal.pendingResult == nil && stage != crashAfterResult {
				redelivered := reconnected.readApplicationFrame(t)
				if redelivered.CommandID != journal.commandID || redelivered.Sequence != journal.sequence {
					t.Fatalf("redelivery at %s changed identity: %+v", stage, redelivered)
				}
				status := "received"
				if journal.received {
					status = "duplicate"
				}
				journal.received = true
				reconnected.sendCommandAck(t, redelivered, status)
				if journal.physicalStarts == 0 {
					journal.physicalStarts++
				}
				result := reconnected.sendResult(t, rendererResultInput{
					command: redelivered, resultID: "result-crash", status: "succeeded", observedState: "playing",
				})
				journal.pendingResult = &result
			}
			if journal.pendingResult != nil && stage != crashAfterResult {
				ack := reconnected.readApplicationFrame(t)
				if ack.Type != "result.ack" || ack.ResultID != journal.pendingResult.ResultID {
					t.Fatalf("result replay acknowledgement at %s = %+v", stage, ack)
				}
			} else {
				reconnected.sendFrame(t, 0x9, []byte("cursor"))
				opcode, payload := reconnected.readFrame(t)
				if opcode != 0xa || string(payload) != "cursor" {
					t.Fatalf("terminal cursor redelivered command at %s: %#x %q", stage, opcode, payload)
				}
			}
			snapshot, snapshotErr := scenario.fixture.store.Snapshot(t.Context(), scenario.zoneID)
			durable, durableErr := scenario.fixture.store.DurableCommand(t.Context(), journal.commandID)

			// Then
			if snapshotErr != nil || durableErr != nil {
				t.Fatalf("post-crash state errors at %s: %v / %v", stage, snapshotErr, durableErr)
			}
			expectedTransport := playback.TransportPlaying
			if stage == crashAfterResult || stage == crashBeforeResultAck {
				expectedTransport = playback.TransportSuspended
			}
			queueSafe := len(snapshot.Queue) == 1 && snapshot.Queue[0].State != playback.QueueCompleted
			if journal.physicalStarts != 1 || !queueSafe || snapshot.Transport != expectedTransport ||
				durable.ReceiptState != playback.CommandReceiptTerminal || durable.ResultAckAt.IsZero() {
				t.Fatalf("post-crash state at %s: journal=%+v snapshot=%+v command=%+v", stage, journal, snapshot, durable)
			}
		})
	}
}

type rendererCrashCut struct {
	stage   rendererCrashStage
	socket  *rendererSocket
	command rendererFrame
	journal *fixtureRendererJournal
}

func cutRendererSession(t *testing.T, cut rendererCrashCut) {
	t.Helper()
	stage, socket, command, journal := cut.stage, cut.socket, cut.command, cut.journal
	switch stage {
	case crashBeforeReceipt:
		return
	case crashAfterReceipt:
		journal.received = true
		socket.sendCommandAck(t, command, "received")
	case crashAfterStart:
		journal.received = true
		socket.sendCommandAck(t, command, "received")
		journal.physicalStarts++
	case crashAfterResult:
		journal.received = true
		socket.sendCommandAck(t, command, "received")
		journal.physicalStarts++
		result := socket.sendResult(t, rendererResultInput{
			command: command, resultID: "result-crash", status: "succeeded", observedState: "playing",
		})
		ack := socket.readApplicationFrame(t)
		if ack.Type != "result.ack" || ack.ResultID != result.ResultID {
			t.Fatalf("pre-cut result acknowledgement = %+v", ack)
		}
	case crashBeforeResultAck:
		journal.received = true
		socket.sendCommandAck(t, command, "received")
		journal.physicalStarts++
		result := socket.sendResult(t, rendererResultInput{
			command: command, resultID: "result-crash", status: "succeeded", observedState: "playing",
		})
		journal.pendingResult = &result
	default:
		t.Fatalf("unknown renderer crash stage %q", stage)
	}
}
