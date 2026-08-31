package api

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/security"
)

func requireBrokerEvent(t *testing.T, events <-chan eventEnvelope) eventEnvelope {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for broker event")
		return eventEnvelope{}
	}
}

func TestEventBroker_unsubscribe_removes_subscriber(t *testing.T) {
	// Given
	broker := newEventBroker()
	subscription := broker.subscribe("device-1")
	if len(broker.subscribers) != 1 {
		t.Fatal("subscriber was not registered")
	}

	// When
	subscription.unsubscribe()

	// Then
	if len(broker.subscribers) != 0 {
		t.Fatal("subscriber remained after disconnect cleanup")
	}
}

func TestEventBroker_revoke_closes_only_matching_device_and_suppresses_later_events(t *testing.T) {
	// Given
	broker := newEventBroker()
	revoked := broker.subscribe(security.DeviceID("device-revoked"))
	defer revoked.unsubscribe()
	active := broker.subscribe(security.DeviceID("device-active"))
	defer active.unsubscribe()

	// When
	broker.revokeDevice(security.DeviceID("device-revoked"))
	broker.publishInvalidation("queue", 1)

	// Then
	select {
	case <-revoked.revoked:
	default:
		t.Fatal("revoked device subscription remained active")
	}
	select {
	case event := <-revoked.events:
		t.Fatalf("revoked device received event = %#v", event)
	default:
	}
	select {
	case event := <-active.events:
		if event.Resource != "queue" || event.Revision != 1 {
			t.Fatalf("active device event = %#v", event)
		}
	default:
		t.Fatal("unrelated device subscription was interrupted")
	}
}

func TestEventBroker_publish_emits_one_ordered_invalidation_and_suppresses_duplicate(t *testing.T) {
	// Given
	broker := newEventBroker()
	subscription := broker.subscribe("device-1")
	defer subscription.unsubscribe()

	// When
	broker.publishInvalidation("zone/main/queue", 4)
	broker.publishInvalidation("zone/main/queue", 4)

	// Then
	event := requireBrokerEvent(t, subscription.events)
	if event.Type != eventTypeInvalidation || event.Epoch != subscription.snapshot.Epoch ||
		event.Sequence != subscription.snapshot.Sequence+1 || event.Resource != "zone/main/queue" || event.Revision != 4 {
		t.Fatalf("invalidation = %#v, snapshot = %#v", event, subscription.snapshot)
	}
	select {
	case duplicate := <-subscription.events:
		t.Fatalf("duplicate invalidation = %#v", duplicate)
	default:
	}
}

func TestServer_publishZoneState_preserves_equal_revisions_for_distinct_zones(t *testing.T) {
	// Given
	broker := newEventBroker()
	service := &server{eventHub: broker}
	subscription := broker.subscribe("device-1")
	defer subscription.unsubscribe()

	// When
	service.publishZoneState("transport", "living", 4)
	service.publishZoneState("transport", "office", 4)

	// Then
	first := requireBrokerEvent(t, subscription.events)
	second := requireBrokerEvent(t, subscription.events)
	if first.Resource != "transport" || first.ZoneID != "living" || first.Revision != 4 {
		t.Fatalf("first invalidation = %#v", first)
	}
	if second.Resource != "transport" || second.ZoneID != "office" || second.Revision != 4 {
		t.Fatalf("second invalidation = %#v", second)
	}
}

func TestEventBroker_overflow_signals_resync_instead_of_silent_drop(t *testing.T) {
	// Given
	broker := newEventBroker()
	subscription := broker.subscribe("device-1")
	defer subscription.unsubscribe()

	// When
	for revision := uint64(1); revision <= eventBufferSize+1; revision++ {
		broker.publishInvalidation("zone/main/queue", revision)
	}

	// Then
	select {
	case signal := <-subscription.resync:
		if signal.Type != eventTypeResyncRequired || signal.Sequence != eventBufferSize+1 {
			t.Fatalf("resync signal = %#v", signal)
		}
	default:
		t.Fatal("overflow was silently dropped")
	}
}

func TestWriteResync_emits_required_event_then_1013_close(t *testing.T) {
	// Given
	var wire bytes.Buffer
	writer := bufio.NewWriter(&wire)
	service := &server{}

	// When
	service.writeResync(writer, 3, 9)

	// Then
	reader := bufio.NewReader(&wire)
	readFrame := func() (byte, []byte) {
		header := make([]byte, 2)
		if _, err := io.ReadFull(reader, header); err != nil {
			t.Fatalf("read frame header: %v", err)
		}
		payload := make([]byte, int(header[1]))
		if _, err := io.ReadFull(reader, payload); err != nil {
			t.Fatalf("read frame payload: %v", err)
		}
		return header[0] & 0x0f, payload
	}
	opcode, payload := readFrame()
	var event eventEnvelope
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode resync event: %v", err)
	}
	if opcode != opcodeText || event.Type != eventTypeResyncRequired || event.Epoch != 3 || event.Sequence != 9 {
		t.Fatalf("resync frame = %#x %#v", opcode, event)
	}
	opcode, payload = readFrame()
	if opcode != opcodeClose || binary.BigEndian.Uint16(payload[:2]) != closeTryAgainLater || string(payload[2:]) != "resync required" {
		t.Fatalf("resync close = %#x %x", opcode, payload)
	}
}

func TestValidateEventSequence_rejects_gap(t *testing.T) {
	// Given
	event := eventEnvelope{Type: eventTypeInvalidation, Sequence: 12}

	// When
	err := validateEventSequence(10, event)

	// Then
	if err == nil {
		t.Fatal("sequence gap was accepted")
	}
}

func TestEventBroker_publishZoneDeletion_allows_every_scoped_resource_to_restart(t *testing.T) {
	broker := newEventBroker()
	subscription := broker.subscribe("device-1")
	defer subscription.unsubscribe()

	resources := []string{"zones", "queue", "transport", "continuation-policy"}
	for index, resource := range resources {
		broker.publishScopedInvalidation(resource, "main", uint64(index+6))
		requireBrokerEvent(t, subscription.events)
	}

	broker.publishZoneDeletion("main", 10)
	deleted := requireBrokerEvent(t, subscription.events)
	if deleted.Resource != "zones" || deleted.ZoneID != "main" || deleted.Revision != 10 {
		t.Fatalf("zone deletion event = %#v", deleted)
	}

	for _, resource := range resources {
		broker.publishScopedInvalidation(resource, "main", 1)
		recreated := requireBrokerEvent(t, subscription.events)
		if recreated.Resource != resource || recreated.ZoneID != "main" || recreated.Revision != 1 {
			t.Fatalf("recreated %s event = %#v", resource, recreated)
		}
	}
}

func TestFrameReader_reassembles_fragmented_text_with_interleaved_ping(t *testing.T) {
	// Given
	wire := append(maskedClientFrame(false, opcodeText, []byte("hel")), maskedClientFrame(true, opcodePing, []byte("p"))...)
	wire = append(wire, maskedClientFrame(true, opcodeContinuation, []byte("lo"))...)
	reader := newFrameReader(bufio.NewReader(bytes.NewReader(wire)))
	var ping []byte

	// When
	payload, err := reader.readMessage(func(opcode byte, payload []byte) error {
		if opcode == opcodePing {
			ping = append([]byte(nil), payload...)
		}
		return nil
	})

	// Then
	if err != nil || string(payload) != "hello" || string(ping) != "p" {
		t.Fatalf("payload/ping/error = %q/%q/%v", payload, ping, err)
	}
}

func TestFrameReader_rejects_unmasked_client_frame(t *testing.T) {
	// Given
	reader := newFrameReader(bufio.NewReader(bytes.NewReader([]byte{0x81, 0x01, 'x'})))

	// When
	_, err := reader.readMessage(func(byte, []byte) error { return nil })

	// Then
	assertCloseCode(t, err, closeProtocolError)
}

func TestFrameReader_rejects_oversized_fragmented_message(t *testing.T) {
	// Given
	first := maskedClientFrame(false, opcodeText, bytes.Repeat([]byte{'a'}, maxWebSocketMessage))
	wire := append(first, maskedClientFrame(true, opcodeContinuation, []byte{'b'})...)
	reader := newFrameReader(bufio.NewReader(bytes.NewReader(wire)))

	// When
	_, err := reader.readMessage(func(byte, []byte) error { return nil })

	// Then
	assertCloseCode(t, err, closeMessageTooBig)
}

func TestFrameReader_rejects_fragmented_control_frame(t *testing.T) {
	// Given
	reader := newFrameReader(bufio.NewReader(bytes.NewReader(maskedClientFrame(false, opcodePing, []byte("x")))))

	// When
	_, err := reader.readMessage(func(byte, []byte) error { return nil })

	// Then
	assertCloseCode(t, err, closeProtocolError)
}

func TestFrameReader_rejects_reserved_opcode(t *testing.T) {
	// Given
	reader := newFrameReader(bufio.NewReader(bytes.NewReader(maskedClientFrame(true, 0x3, nil))))

	// When
	_, err := reader.readMessage(func(byte, []byte) error { return nil })

	// Then
	assertCloseCode(t, err, closeProtocolError)
}

func TestWebSocket_liveness_and_message_limits_are_bounded(t *testing.T) {
	// Then
	if websocketPingPeriod != 20_000_000_000 || websocketPongWait != 60_000_000_000 || maxWebSocketMessage != 64<<10 {
		t.Fatalf("ping/pong/max = %v/%v/%d", websocketPingPeriod, websocketPongWait, maxWebSocketMessage)
	}
}

func TestWriteFrame_supports_64KiB_payload_without_truncation(t *testing.T) {
	// Given
	var wire bytes.Buffer
	writer := bufio.NewWriter(&wire)
	payload := bytes.Repeat([]byte{'x'}, maxWebSocketMessage)

	// When
	err := writeFrame(writer, opcodeText, payload)
	flushErr := writer.Flush()

	// Then
	if err != nil || flushErr != nil {
		t.Fatalf("write/flush = %v/%v", err, flushErr)
	}
	reader := bufio.NewReader(&wire)
	if first, _ := reader.ReadByte(); first != 0x81 {
		t.Fatalf("first byte = %#x", first)
	}
	lengthMarker, _ := reader.ReadByte()
	if lengthMarker != 127 {
		t.Fatalf("length marker = %d", lengthMarker)
	}
	lengthBytes := make([]byte, 8)
	_, _ = io.ReadFull(reader, lengthBytes)
	body, _ := io.ReadAll(reader)
	if !bytes.Equal(body, payload) {
		t.Fatalf("payload length = %d", len(body))
	}
}

func maskedClientFrame(final bool, opcode byte, payload []byte) []byte {
	first := opcode
	if final {
		first |= 0x80
	}
	wire := []byte{first}
	switch {
	case len(payload) <= 125:
		wire = append(wire, 0x80|byte(len(payload)))
	case len(payload) <= 65535:
		wire = append(wire, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	default:
		wire = append(wire, 0x80|127, 0, 0, 0, 0, byte(len(payload)>>24), byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload)))
	}
	mask := []byte{1, 2, 3, 4}
	wire = append(wire, mask...)
	for index, value := range payload {
		wire = append(wire, value^mask[index%len(mask)])
	}
	return wire
}

func assertCloseCode(t *testing.T, err error, expected uint16) {
	t.Helper()
	var closeErr *websocketCloseError
	if !errors.As(err, &closeErr) || closeErr.code != expected {
		t.Fatalf("close error = %#v, want %d", err, expected)
	}
}
