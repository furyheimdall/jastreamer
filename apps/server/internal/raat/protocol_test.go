package raat

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func testControlFrame(kind uint32, body []byte) []byte {
	frame := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	frame = binary.BigEndian.AppendUint32(frame, kind)
	return append(frame, body...)
}

func testControlResponse(kind, id uint32, flags byte, payload string) []byte {
	body := binary.BigEndian.AppendUint32(nil, id)
	body = append(body, flags)
	body = append(body, payload...)
	return testControlFrame(kind, body)
}

func testControlPipe(t *testing.T) (*controlClient, net.Conn) {
	t.Helper()
	local, peer := net.Pipe()
	client := controlClientFromConn(local)
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close() })
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return client, peer
}

func testRequestID(t *testing.T, reader io.Reader) uint32 {
	t.Helper()
	_, body, err := readControlFrame(reader)
	if err != nil {
		t.Error(err)
		return 0
	}
	if len(body) < 4 {
		t.Error("missing request ID")
		return 0
	}
	return binary.BigEndian.Uint32(body[:4])
}

func testReply(t *testing.T, sub *controlSubscription) controlReply {
	t.Helper()
	select {
	case reply, open := <-sub.Replies:
		if !open {
			t.Fatal("subscription closed without a reply")
		}
		return reply
	case <-time.After(3 * time.Second):
		t.Fatal("subscription did not respond")
		return controlReply{}
	}
}

func TestControlFrameBoundaries(t *testing.T) {
	cases := map[string][]byte{
		"empty":          nil,
		"short header":   {0, 0, 0, 8},
		"short length":   testControlFrame(luaResponse, nil),
		"oversized":      testControlFrame(luaResponse, nil),
		"truncated body": testControlFrame(luaResponse, []byte("payload")),
	}
	binary.BigEndian.PutUint32(cases["short length"][:4], 7)
	binary.BigEndian.PutUint32(cases["oversized"][:4], maxControlBody+9)
	cases["truncated body"] = cases["truncated body"][:10]
	for name, frame := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readControlFrame(bytes.NewReader(frame)); err == nil {
				t.Fatal("accepted malformed frame")
			}
		})
	}
	frame := testControlFrame(luaResponse, []byte("valid"))
	kind, body, err := readControlFrame(&oneByteReader{data: frame})
	if err != nil || kind != luaResponse || string(body) != "valid" {
		t.Fatalf("partial read: %x %q %v", kind, body, err)
	}
}

type oneByteReader struct{ data []byte }

func (reader *oneByteReader) Read(buffer []byte) (int, error) {
	if len(reader.data) == 0 {
		return 0, io.EOF
	}
	buffer[0] = reader.data[0]
	reader.data = reader.data[1:]
	return 1, nil
}

type shortControlWriter struct{ bytes.Buffer }

func (writer *shortControlWriter) Write(data []byte) (int, error) {
	if len(data) > 2 {
		data = data[:2]
	}
	return writer.Buffer.Write(data)
}

func TestControlPartialWrites(t *testing.T) {
	var writer shortControlWriter
	if err := writeControlBytes(&writer, []byte("complete message")); err != nil {
		t.Fatal(err)
	}
	if writer.String() != "complete message" {
		t.Fatal("short writes lost data")
	}
}

func TestControlCorrelatesAndRetainsNonfinalReplies(t *testing.T) {
	client, peer := testControlPipe(t)
	ids := make(chan uint32, 2)
	go func() { ids <- testRequestID(t, peer); ids <- testRequestID(t, peer) }()
	first, err := client.subscribe(context.Background(), luaRequest, []byte("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.subscribe(context.Background(), managementRequest, []byte("second"))
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := <-ids, <-ids
	go func() {
		frames := [][]byte{
			testControlResponse(managementResponse, secondID, 1, "second final"),
			testControlResponse(luaResponse, firstID, 2, "first notification"),
			testControlResponse(luaResponse, firstID, 3, "first final"),
		}
		for _, frame := range frames {
			if err := writeControlBytes(peer, frame); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	if reply := testReply(t, second); !reply.Final || string(reply.Payload) != "second final" {
		t.Fatalf("wrong second reply: %+v", reply)
	}
	if reply := testReply(t, first); reply.Final || string(reply.Payload) != "first notification" {
		t.Fatalf("wrong notification: %+v", reply)
	}
	if reply := testReply(t, first); !reply.Final || string(reply.Payload) != "first final" {
		t.Fatalf("wrong final: %+v", reply)
	}
	if _, open := <-first.Replies; open {
		t.Fatal("final subscription remains open")
	}
}

func TestControlCallWaitsForFinal(t *testing.T) {
	client, peer := testControlPipe(t)
	initial := make(chan struct{})
	finish := make(chan struct{})
	go func() {
		id := testRequestID(t, peer)
		if err := writeControlBytes(peer, testControlResponse(luaResponse, id, 0, `{"status":"Success"}`)); err != nil {
			t.Error(err)
			return
		}
		close(initial)
		<-finish
		_ = writeControlBytes(peer, testControlResponse(luaResponse, id, 1, "final outcome"))
	}()
	type result struct {
		payload []byte
		err     error
	}
	resultCh := make(chan result, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { payload, err := client.call(ctx, luaRequest, nil); resultCh <- result{payload, err} }()
	<-initial
	select {
	case value := <-resultCh:
		close(finish)
		t.Fatalf("call returned on nonfinal success: %+v", value)
	case <-time.After(20 * time.Millisecond):
	}
	close(finish)
	value := <-resultCh
	if value.err != nil || string(value.payload) != "final outcome" {
		t.Fatalf("wrong final result: %+v", value)
	}
}

func TestControlSubscriptionContextOnlyAppliesToSend(t *testing.T) {
	client, peer := testControlPipe(t)
	ids := make(chan uint32, 1)
	go func() { ids <- testRequestID(t, peer) }()
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := client.subscribe(ctx, luaRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := <-ids
	cancel()
	go func() { _ = writeControlBytes(peer, testControlResponse(luaResponse, id, 0, "alive")) }()
	if reply := testReply(t, sub); reply.Err != nil || string(reply.Payload) != "alive" {
		t.Fatalf("send context cancelled subscription: %+v", reply)
	}
	sub.Cancel()
	sub.Cancel()
	if reply := testReply(t, sub); !errors.Is(reply.Err, context.Canceled) || !reply.Final {
		t.Fatalf("explicit cancellation: %+v", reply)
	}
}

func TestControlCallCancellationRemovesPending(t *testing.T) {
	client, peer := testControlPipe(t)
	read := make(chan struct{})
	go func() { testRequestID(t, peer); close(read) }()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := client.call(ctx, luaRequest, nil); result <- err }()
	<-read
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("call cancellation: %v", err)
	}
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.pending) != 0 {
		t.Fatal("cancelled request remains pending")
	}
}

func TestControlDisconnectFailsAllPending(t *testing.T) {
	client, peer := testControlPipe(t)
	go func() { testRequestID(t, peer); testRequestID(t, peer); _ = peer.Close() }()
	first, err := client.subscribe(context.Background(), luaRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.subscribe(context.Background(), managementRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, sub := range []*controlSubscription{first, second} {
		if reply := testReply(t, sub); reply.Err == nil || !reply.Final {
			t.Fatalf("disconnect was not terminal: %+v", reply)
		}
	}
}

func TestControlMalformedResponseFailsPending(t *testing.T) {
	for name, response := range map[string]func(uint32) []byte{
		"short body":          func(uint32) []byte { return testControlFrame(luaResponse, []byte{0, 0, 0, 1}) },
		"wrong response kind": func(id uint32) []byte { return testControlResponse(managementResponse, id, 1, "wrong") },
		"unknown kind":        func(id uint32) []byte { return testControlResponse(9, id, 1, "wrong") },
	} {
		t.Run(name, func(t *testing.T) {
			client, peer := testControlPipe(t)
			go func() { id := testRequestID(t, peer); _ = writeControlBytes(peer, response(id)) }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := client.call(ctx, luaRequest, nil); !errors.Is(err, errControlFrame) {
				t.Fatalf("malformed reply error: %v", err)
			}
		})
	}
}

func TestControlBlockedWriteCancellation(t *testing.T) {
	client, _ := testControlPipe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := client.send(ctx, luaRequest, []byte("blocked")); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write cancellation: %v", err)
	}
	select {
	case <-client.done:
	default:
		t.Fatal("partially sent frame did not close connection")
	}
}
