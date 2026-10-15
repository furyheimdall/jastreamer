package raat

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

const (
	managementRequest   uint32 = 0x80000001
	managementResponse  uint32 = 0x80000002
	luaRequest          uint32 = 1
	luaResponse         uint32 = 2
	maxControlBody             = 4 << 20
	maxPendingRequests         = 128
	controlReplyBuffer         = 32
	controlWriteTimeout        = 5 * time.Second
)

var (
	errControlClosed = errors.New("raat: control connection closed")
	errControlFrame  = errors.New("raat: invalid control frame")
)

type endpoint struct {
	address      string
	localAddress string
	port         int
}

type controlReply struct {
	Payload []byte
	Final   bool
	Err     error
}

type controlSubscription struct {
	Replies <-chan controlReply
	Cancel  func()
}

type pendingControl struct {
	kind    uint32
	replies chan controlReply
	done    chan struct{}
	cancel  sync.Once
}

type controlClient struct {
	conn      net.Conn
	writeGate chan struct{}
	mu        sync.Mutex
	pending   map[uint32]*pendingControl
	nextID    uint32
	err       error
	done      chan struct{}
	keepalive bool
	stopOnce  sync.Once
}

func validEndpoint(ep endpoint) bool {
	remote, err := netip.ParseAddr(ep.address)
	if err != nil || !allowedLocalAddress(remote) || ep.port < 1 || ep.port > 65535 {
		return false
	}
	local, err := netip.ParseAddr(ep.localAddress)
	return err == nil && allowedLocalAddress(local) && local.IsLoopback() == remote.IsLoopback()
}

func newControlClient(ctx context.Context, ep endpoint) (*controlClient, error) {
	if !validEndpoint(ep) {
		return nil, errors.New("raat: invalid control endpoint")
	}
	dialer := net.Dialer{Timeout: controlWriteTimeout, LocalAddr: &net.TCPAddr{IP: net.ParseIP(ep.localAddress)}}
	conn, err := dialer.DialContext(ctx, "tcp4", net.JoinHostPort(ep.address, strconv.Itoa(ep.port)))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("raat: control connection failed")
	}
	return controlClientFromConn(conn), nil
}

func controlClientFromConn(conn net.Conn) *controlClient {
	client := &controlClient{conn: conn, writeGate: make(chan struct{}, 1), pending: make(map[uint32]*pendingControl), done: make(chan struct{})}
	go client.readLoop()
	return client
}

func readControlFrame(reader io.Reader) (uint32, []byte, error) {
	var header [8]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[:4])
	if length < 8 || length-8 > maxControlBody {
		return 0, nil, errControlFrame
	}
	body := make([]byte, int(length)-8)
	if _, err := io.ReadFull(reader, body); err != nil {
		return 0, nil, err
	}
	return binary.BigEndian.Uint32(header[4:]), body, nil
}

func (client *controlClient) readLoop() {
	for {
		kind, body, err := readControlFrame(client.conn)
		if err != nil {
			if errors.Is(err, errControlFrame) {
				client.fail(errControlFrame)
			} else {
				client.fail(errControlClosed)
			}
			return
		}
		if kind == 0x80000003 {
			continue
		}
		if (kind != managementResponse && kind != luaResponse) || len(body) < 5 {
			client.fail(errControlFrame)
			return
		}
		id := binary.BigEndian.Uint32(body[:4])
		reply := controlReply{Payload: body[5:], Final: body[4]&1 != 0}
		client.mu.Lock()
		pending := client.pending[id]
		if pending == nil {
			client.mu.Unlock()
			continue // Late replies to explicitly cancelled subscriptions are harmless.
		}
		if pending.kind != kind {
			client.mu.Unlock()
			client.fail(errControlFrame)
			return
		}
		// A delayed TCP delivery can contain many valid notifications. Apply
		// bounded backpressure, not a connection failure. Cancellation signals
		// are closed before taking mu so shutdown cannot deadlock here.
		select {
		case pending.replies <- reply:
			if reply.Final {
				delete(client.pending, id)
				close(pending.replies)
			}
		case <-pending.done:
		case <-client.done:
			client.mu.Unlock()
			return
		}
		client.mu.Unlock()
	}
}

// finishPending discards queued notifications so a terminal error is never lost.
// The client mutex protects every producer and channel close.
func finishPending(pending *pendingControl, err error) {
	for {
		select {
		case <-pending.replies:
		default:
			pending.replies <- controlReply{Final: true, Err: err}
			close(pending.replies)
			return
		}
	}
}

func (client *controlClient) fail(err error) {
	client.stopOnce.Do(func() {
		close(client.done)
		_ = client.conn.Close()
	})
	client.mu.Lock()
	if client.err != nil {
		client.mu.Unlock()
		return
	}
	client.err = err
	for id, pending := range client.pending {
		finishPending(pending, err)
		delete(client.pending, id)
	}
	client.mu.Unlock()
}

func (client *controlClient) Close() error {
	client.fail(errControlClosed)
	return nil
}

// Subscribers must keep draining replies and must not wait for another RPC on
// this connection while handling a notification.
func (client *controlClient) subscribe(ctx context.Context, kind uint32, payload []byte) (*controlSubscription, error) {
	var response uint32
	switch kind {
	case managementRequest:
		response = managementResponse
	case luaRequest:
		response = luaResponse
	default:
		return nil, errControlFrame
	}
	if len(payload) > maxControlBody-4 {
		return nil, errControlFrame
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client.mu.Lock()
	if client.err != nil {
		err := client.err
		client.mu.Unlock()
		return nil, err
	}
	if len(client.pending) >= maxPendingRequests {
		client.mu.Unlock()
		return nil, errors.New("raat: too many control requests")
	}
	for {
		client.nextID++
		if client.nextID != 0 && client.pending[client.nextID] == nil {
			break
		}
	}
	id := client.nextID
	pending := &pendingControl{kind: response, replies: make(chan controlReply, controlReplyBuffer), done: make(chan struct{})}
	client.pending[id] = pending
	client.mu.Unlock()
	cancel := func() {
		pending.cancel.Do(func() { close(pending.done) })
		client.mu.Lock()
		if client.pending[id] == pending {
			delete(client.pending, id)
			finishPending(pending, context.Canceled)
		}
		client.mu.Unlock()
	}
	var requestID [4]byte
	binary.BigEndian.PutUint32(requestID[:], id)
	if err := client.sendParts(ctx, kind, requestID[:], payload); err != nil {
		cancel()
		return nil, err
	}
	return &controlSubscription{Replies: pending.replies, Cancel: cancel}, nil
}

func (client *controlClient) call(ctx context.Context, kind uint32, payload []byte) ([]byte, error) {
	sub, err := client.subscribe(ctx, kind, payload)
	if err != nil {
		return nil, err
	}
	defer sub.Cancel()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case reply, open := <-sub.Replies:
			if !open {
				return nil, errControlClosed
			}
			if reply.Err != nil {
				return nil, reply.Err
			}
			if reply.Final {
				return reply.Payload, nil
			}
		}
	}
}

func (client *controlClient) send(ctx context.Context, kind uint32, payload []byte) error {
	return client.sendParts(ctx, kind, nil, payload)
}

func (client *controlClient) sendParts(ctx context.Context, kind uint32, prefix, payload []byte) error {
	if len(prefix) > maxControlBody || len(payload) > maxControlBody-len(prefix) {
		return errControlFrame
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case client.writeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-client.done:
		return errControlClosed
	}
	defer func() { <-client.writeGate }()
	select {
	case <-client.done:
		return errControlClosed
	default:
	}
	deadline := time.Now().Add(controlWriteTimeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := client.conn.SetWriteDeadline(deadline); err != nil {
		client.fail(errControlClosed)
		return errControlClosed
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = client.conn.SetWriteDeadline(time.Now()); close(interrupted) })
	var header [8]byte
	binary.BigEndian.PutUint32(header[:4], uint32(8+len(prefix)+len(payload)))
	binary.BigEndian.PutUint32(header[4:], kind)
	// Submit header, request ID and body together without copying the payload.
	var buffers [3][]byte
	buffers[0] = header[:]
	parts := net.Buffers(buffers[:1])
	if len(prefix) != 0 {
		parts = append(parts, prefix)
	}
	if len(payload) != 0 {
		parts = append(parts, payload)
	}
	written, err := parts.WriteTo(client.conn)
	if err == nil && written != int64(8+len(prefix)+len(payload)) {
		err = io.ErrShortWrite
	}
	if !stop() {
		<-interrupted
	}
	_ = client.conn.SetWriteDeadline(time.Time{})
	if err != nil {
		client.fail(errControlClosed) // A partially sent frame cannot be safely retried.
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if value, ok := ctx.Deadline(); ok && !time.Now().Before(value) {
			return context.DeadlineExceeded
		}
		return errControlClosed
	}
	return nil
}

func writeControlBytes(writer io.Writer, data []byte) error {
	for len(data) != 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}

func (client *controlClient) startKeepalive(ctx context.Context) error {
	client.mu.Lock()
	if client.err != nil {
		err := client.err
		client.mu.Unlock()
		return err
	}
	if client.keepalive {
		client.mu.Unlock()
		return errors.New("raat: keepalive already started")
	}
	client.keepalive = true
	client.mu.Unlock()
	var body [8]byte
	binary.BigEndian.PutUint32(body[:4], 2000)
	binary.BigEndian.PutUint32(body[4:], 10000)
	if err := client.send(ctx, 0x80000004, body[:]); err != nil {
		client.fail(err)
		return err
	}
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				client.fail(ctx.Err())
				return
			case <-client.done:
				return
			case <-ticker.C:
				if err := client.send(ctx, 0x80000003, nil); err != nil {
					client.fail(err)
					return
				}
			}
		}
	}()
	return nil
}
