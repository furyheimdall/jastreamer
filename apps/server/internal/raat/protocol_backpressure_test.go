package raat

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func bufferedControlBurst(t *testing.T) (*controlClient, net.Conn, *controlSubscription, <-chan struct{}) {
	t.Helper()
	client, peer := testControlPipe(t)
	prefix := make(chan struct{})
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		id := testRequestID(t, peer)
		for index := 0; index < controlReplyBuffer*8; index++ {
			flag := byte(0)
			if index == controlReplyBuffer*8-1 {
				flag = 1
			}
			payload := binary.BigEndian.AppendUint32(nil, uint32(index))
			err := writeControlBytes(peer, testControlResponse(luaResponse, id, flag, string(payload)))
			if index == controlReplyBuffer {
				close(prefix)
			}
			if err != nil {
				return
			}
		}
	}()
	sub, err := client.subscribe(context.Background(), luaRequest, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-prefix:
	case <-time.After(time.Second):
		t.Fatal("burst did not reach the bounded receive queue")
	}
	return client, peer, sub, sent
}

func TestControlBurstBackpressuresWithoutLosingReplies(t *testing.T) {
	client, _, sub, sent := bufferedControlBurst(t)
	// The consumer is deliberately suspended while a network burst fills the queue.
	select {
	case <-client.done:
		t.Fatal("valid reply burst closed the connection")
	case <-time.After(20 * time.Millisecond):
	}
	for index := 0; index < controlReplyBuffer*8; index++ {
		reply := testReply(t, sub)
		if reply.Err != nil || len(reply.Payload) != 4 || binary.BigEndian.Uint32(reply.Payload) != uint32(index) || reply.Final != (index == controlReplyBuffer*8-1) {
			t.Fatalf("reply %d was lost, reordered or prematurely finalized: %+v", index, reply)
		}
	}
	<-sent
	if _, open := <-sub.Replies; open {
		t.Fatal("final reply did not end subscription")
	}
	select {
	case <-client.done:
		t.Fatal("burst corrupted connection")
	default:
	}
}

func TestControlCancelUnblocksFullSubscriptionAndOtherCalls(t *testing.T) {
	client, peer, sub, sent := bufferedControlBurst(t)
	cancelled := make(chan struct{})
	go func() { sub.Cancel(); close(cancelled) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cancellation deadlocked behind delivery")
	}
	if reply := testReply(t, sub); !reply.Final || !errors.Is(reply.Err, context.Canceled) {
		t.Fatalf("cancellation = %+v", reply)
	}
	<-sent
	go func() {
		id := testRequestID(t, peer)
		_ = writeControlBytes(peer, testControlResponse(luaResponse, id, 1, "still alive"))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result, err := client.call(ctx, luaRequest, nil)
	if err != nil || string(result) != "still alive" {
		t.Fatalf("unrelated call lost: %q %v", result, err)
	}
}

func TestControlCloseUnblocksFullSubscription(t *testing.T) {
	client, _, sub, sent := bufferedControlBurst(t)
	closed := make(chan struct{})
	go func() { client.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked behind delivery")
	}
	if reply := testReply(t, sub); !reply.Final || !errors.Is(reply.Err, errControlClosed) {
		t.Fatalf("close = %+v", reply)
	}
	<-sent
}
