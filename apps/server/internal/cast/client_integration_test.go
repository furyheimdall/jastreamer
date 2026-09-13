package cast

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

type peerScript func(*testing.T, net.Conn)

func TestSetURIOrdersLaunchAndLoadWithoutAutoplayAndPlayRejectsReplacement(t *testing.T) {
	const mediaURL = "http://127.0.0.1:8080/media/owned"
	address := startTLSPeer(t, func(t *testing.T, connection net.Conn) {
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload := expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerPayload(t, connection, castMessage{
			source: receiverID, destination: request.source, namespace: heartbeatNamespace,
		}, map[string]any{"type": "PING"})
		expectPeerMessage(t, connection, heartbeatNamespace, "PONG", receiverID)
		sendPeerPayload(t, connection, castMessage{
			source: receiverID, destination: request.source, namespace: receiverNamespace,
		}, map[string]any{
			"type": "RECEIVER_STATUS", "requestId": 999,
			"status": map[string]any{"applications": []any{}},
		})
		sendPeerResponse(t, connection, request, payload, map[string]any{
			"type": "RECEIVER_STATUS", "status": map[string]any{"applications": []any{}},
		})
		request, payload = expectPeerMessage(t, connection, receiverNamespace, "LAUNCH", receiverID)
		if payload["appId"] != defaultReceiverApp {
			t.Fatalf("LAUNCH appId = %v", payload["appId"])
		}
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "LOAD", "transport-1")
		if autoplay, ok := payload["autoplay"].(bool); !ok || autoplay {
			t.Fatalf("LOAD autoplay = %#v, want false", payload["autoplay"])
		}
		media, ok := payload["media"].(map[string]any)
		if !ok || media["contentId"] != mediaURL || media["contentType"] != "audio/flac" {
			t.Fatalf("LOAD media = %#v", payload["media"])
		}
		sendPeerResponse(t, connection, request, payload, mediaStatusPayloadForTest(41, mediaURL, "PAUSED", "", 3))
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload = expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "GET_STATUS", "transport-1")
		sendPeerResponse(t, connection, request, payload, mediaStatusPayloadForTest(42, "http://127.0.0.1:8080/media/remote", "PLAYING", "", 3))
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload = expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "GET_STATUS", "transport-1")
		sendPeerResponse(t, connection, request, payload, mediaStatusPayloadForTest(41, "http://127.0.0.1:8080/media/remote", "IDLE", "FINISHED", 3))
	})
	manager, deviceID := tlsTestManager(t, address)
	resource := output.Resource{
		URL: mediaURL, Mime: "audio/flac", Title: "Owned track", Artist: "Artist",
		DurationMS: 120000, Seekable: true, PlayID: "play-test",
	}
	if err := manager.SetURI(context.Background(), deviceID, resource); err != nil {
		t.Fatalf("SetURI: %v", err)
	}
	err := manager.Play(context.Background(), deviceID)
	var actionError *output.ActionError
	if !errors.As(err, &actionError) || actionError.Kind != output.ErrorUnsupported {
		t.Fatalf("Play after remote replacement error = %v, want unsupported ActionError", err)
	}
	observation, err := manager.Observe(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("Observe replacement: %v", err)
	}
	if !observation.CompletionKnown || observation.Completed || observation.URI == mediaURL {
		t.Fatalf("replacement FINISHED observation = %+v", observation)
	}
}

func TestPauseReturnsReceiverInvalidPlayerState(t *testing.T) {
	const mediaURL = "http://127.0.0.1:8080/media/owned"
	address := startTLSPeer(t, func(t *testing.T, connection net.Conn) {
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload := expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "GET_STATUS", "transport-1")
		sendPeerResponse(t, connection, request, payload, mediaStatusPayloadForTest(41, mediaURL, "PLAYING", "", mediaCommandPause))
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "PAUSE", "transport-1")
		sendPeerResponse(t, connection, request, payload, map[string]any{
			"type": "INVALID_PLAYER_STATE", "reason": "not supported in the current state",
		})
	})
	manager, deviceID := tlsTestManager(t, address)
	seedOwnedTestSession(manager, deviceID, mediaURL, mediaCommandPause)
	err := manager.Pause(context.Background(), deviceID)
	var actionError *output.ActionError
	if !errors.As(err, &actionError) || actionError.Kind != output.ErrorResponse {
		t.Fatalf("Pause receiver error = %v, want response ActionError", err)
	}
}

func TestPauseRejectsUnsupportedMediaWithoutSendingCommand(t *testing.T) {
	const mediaURL = "http://127.0.0.1:8080/media/owned"
	address := startTLSPeer(t, func(t *testing.T, connection net.Conn) {
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload := expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "GET_STATUS", "transport-1")
		sendPeerResponse(t, connection, request, payload, mediaStatusPayloadForTest(41, mediaURL, "PLAYING", "", 0))
		_ = connection.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if message, err := readCastMessage(connection); err == nil {
			t.Fatalf("unexpected unsupported PAUSE command: %s", message.payload)
		}
	})
	manager, deviceID := tlsTestManager(t, address)
	seedOwnedTestSession(manager, deviceID, mediaURL, 0)
	err := manager.Pause(context.Background(), deviceID)
	var actionError *output.ActionError
	if !errors.As(err, &actionError) || actionError.Kind != output.ErrorUnsupported {
		t.Fatalf("unsupported Pause error = %v, want unsupported ActionError", err)
	}
}

func TestStopConfirmsOwnedActiveMediaBeforeAcceptingSessionRemoval(t *testing.T) {
	const mediaURL = "http://127.0.0.1:8080/media/owned"
	address := startTLSPeer(t, func(t *testing.T, connection net.Conn) {
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload := expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "GET_STATUS", "transport-1")
		sendPeerResponse(t, connection, request, payload, mediaStatusPayloadForTest(41, mediaURL, "PLAYING", "", 0))
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "STOP", "transport-1")
		if payload["mediaSessionId"] != float64(41) {
			t.Fatalf("STOP mediaSessionId = %#v, want owned session 41", payload["mediaSessionId"])
		}
		sendPeerResponse(t, connection, request, payload, map[string]any{"type": "MEDIA_STATUS", "status": []any{}})
	})
	manager, deviceID := tlsTestManager(t, address)
	seedOwnedTestSession(manager, deviceID, mediaURL, 0)
	if err := manager.Stop(context.Background(), deviceID); err != nil {
		t.Fatalf("Stop active owned media: %v", err)
	}
}

func TestStopAcceptsConfirmedAbsenceAfterOwnedMediaEnded(t *testing.T) {
	const mediaURL = "http://127.0.0.1:8080/media/owned"
	address := startTLSPeer(t, func(t *testing.T, connection net.Conn) {
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload := expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "GET_STATUS", "transport-1")
		sendPeerResponse(t, connection, request, payload, map[string]any{"type": "MEDIA_STATUS", "status": []any{}})
		_ = connection.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if message, err := readCastMessage(connection); err == nil {
			t.Fatalf("unexpected STOP for already-ended media: %s", message.payload)
		}
	})
	manager, deviceID := tlsTestManager(t, address)
	seedOwnedTestSession(manager, deviceID, mediaURL, 0)
	session := manager.devices[deviceID].session
	session.lastStatus.PlayerState = "IDLE"
	session.lastStatus.IdleReason = "ERROR"
	if err := manager.Stop(context.Background(), deviceID); err != nil {
		t.Fatalf("Stop ended owned media: %v", err)
	}
}

func TestStopRejectsForeignMediaWithoutSendingCommand(t *testing.T) {
	const mediaURL = "http://127.0.0.1:8080/media/owned"
	address := startTLSPeer(t, func(t *testing.T, connection net.Conn) {
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload := expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "GET_STATUS", "transport-1")
		sendPeerResponse(t, connection, request, payload, mediaStatusPayloadForTest(42, "http://127.0.0.1:8080/media/foreign", "PLAYING", "", 0))
		_ = connection.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if message, err := readCastMessage(connection); err == nil {
			t.Fatalf("unexpected STOP for foreign media: %s", message.payload)
		}
	})
	manager, deviceID := tlsTestManager(t, address)
	seedOwnedTestSession(manager, deviceID, mediaURL, 0)
	err := manager.Stop(context.Background(), deviceID)
	var actionError *output.ActionError
	if !errors.As(err, &actionError) || actionError.Kind != output.ErrorUnsupported {
		t.Fatalf("Stop foreign media error = %v, want unsupported ActionError", err)
	}
}

func TestPersistentClientPreservesUnsolicitedFinishedAcrossEmptyPoll(t *testing.T) {
	const mediaURL = "http://127.0.0.1:8080/media/owned"
	loaded := make(chan struct{})
	eventsDelivered := make(chan struct{})
	address := startTLSPeer(t, func(t *testing.T, connection net.Conn) {
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload := expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "LOAD", "transport-1")
		sendPeerResponse(t, connection, request, payload, mediaStatusPayloadForTest(41, mediaURL, "PAUSED", "", 3))
		<-loaded
		sendPeerPayload(t, connection, castMessage{
			source: "transport-1", destination: request.source, namespace: mediaNamespace,
		}, map[string]any{
			"type":   "MEDIA_STATUS",
			"status": []map[string]any{{"mediaSessionId": 41, "playerState": "PLAYING", "currentTime": 119}},
		})
		sendPeerPayload(t, connection, castMessage{
			source: receiverID, destination: request.source, namespace: heartbeatNamespace,
		}, map[string]any{"type": "PING"})
		expectPeerMessage(t, connection, heartbeatNamespace, "PONG", receiverID)
		sendPeerPayload(t, connection, castMessage{
			source: "transport-1", destination: request.source, namespace: mediaNamespace,
		}, map[string]any{
			"type":   "MEDIA_STATUS",
			"status": []map[string]any{{"mediaSessionId": 41, "playerState": "IDLE", "idleReason": "FINISHED", "currentTime": 120}},
		})
		close(eventsDelivered)
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", receiverID)
		request, payload = expectPeerMessage(t, connection, receiverNamespace, "GET_STATUS", receiverID)
		sendPeerResponse(t, connection, request, payload, receiverStatusPayloadForTest())
		expectPeerMessage(t, connection, connectionNamespace, "CONNECT", "transport-1")
		request, payload = expectPeerMessage(t, connection, mediaNamespace, "GET_STATUS", "transport-1")
		sendPeerResponse(t, connection, request, payload, map[string]any{"type": "MEDIA_STATUS", "status": []any{}})
	})
	manager, deviceID := tlsTestManager(t, address)
	resource := output.Resource{URL: mediaURL, Mime: "audio/flac", DurationMS: 120000, Seekable: true}
	if err := manager.SetURI(context.Background(), deviceID, resource); err != nil {
		t.Fatalf("SetURI: %v", err)
	}
	close(loaded)
	<-eventsDelivered
	deadline := time.Now().Add(time.Second)
	for {
		if _, available := manager.devices[deviceID].session.terminalSnapshot(); available {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("unsolicited FINISHED status was not retained")
		}
		time.Sleep(time.Millisecond)
	}
	observation, err := manager.Observe(context.Background(), deviceID)
	if err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if !observation.CompletionKnown || !observation.Completed || observation.URI != mediaURL ||
		!observation.HasPosition || observation.PositionMS != 120000 {
		t.Fatalf("preserved FINISHED observation = %+v", observation)
	}
}

func seedOwnedTestSession(manager *Manager, deviceID, mediaURL string, supported int64) {
	duration, position := 120.0, 5.0
	record := manager.devices[deviceID]
	record.session.resource = output.Resource{URL: mediaURL, Mime: "audio/flac", DurationMS: 120000, Seekable: true}
	record.session.mediaSessionID = 41
	record.session.appSessionID = "app-session-1"
	record.session.transportID = "transport-1"
	record.session.lastStatus = mediaStatus{
		MediaSessionID: 41, PlayerState: "PLAYING", CurrentTime: &position,
		SupportedCommands: supported, Media: mediaDescription{ContentID: mediaURL, Duration: &duration},
	}
	record.session.hasStatus = true
	manager.devices[deviceID] = record
}

func startTLSPeer(t *testing.T, scripts ...peerScript) string {
	t.Helper()
	certificate := localCertificate(t)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for _, script := range scripts {
			connection, err := tlsListener.Accept()
			if err != nil {
				return
			}
			_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
			script(t, connection)
			_ = connection.Close()
		}
	}()
	t.Cleanup(func() {
		_ = tlsListener.Close()
		wait.Wait()
	})
	return listener.Addr().String()
}

func localCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:    x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}

func tlsTestManager(t *testing.T, address string) (*Manager, string) {
	t.Helper()
	addressPort, err := netip.ParseAddrPort(address)
	if err != nil {
		t.Fatal(err)
	}
	prefix := netip.MustParsePrefix("127.0.0.0/8")
	target := endpoint{address: addressPort.Addr(), local: netip.MustParseAddr("127.0.0.1"), port: int(addressPort.Port()), network: prefix}
	manager := &Manager{
		config: Config{DiscoveryInterval: time.Second, Notify: func(string) {}}, now: time.Now,
		dial: func(ctx context.Context, _ endpoint) (net.Conn, error) {
			raw, err := (&net.Dialer{}).DialContext(ctx, "tcp4", address)
			if err != nil {
				return nil, err
			}
			connection := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12})
			if err := connection.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				return nil, err
			}
			return connection, nil
		},
		devices: make(map[string]deviceRecord), connections: make(map[net.Conn]struct{}),
	}
	deviceID := "cast:11111111-2222-4333-8444-555555555555"
	manager.devices[deviceID] = deviceRecord{
		device:   output.Device{ID: deviceID, Online: true, Address: address, LocalAddress: "127.0.0.1"},
		endpoint: target, expiresAt: time.Now().Add(time.Minute), session: &playbackSession{},
	}
	return manager, deviceID
}

func expectPeerMessage(t *testing.T, connection net.Conn, namespace, payloadType, destination string) (castMessage, map[string]any) {
	t.Helper()
	message, err := readCastMessage(connection)
	if err != nil {
		t.Fatal(err)
	}
	if message.namespace != namespace || message.destination != destination {
		t.Fatalf("message route = %q to %q, want %q to %q", message.namespace, message.destination, namespace, destination)
	}
	var payload map[string]any
	if err := json.Unmarshal(message.payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["type"] != payloadType {
		t.Fatalf("message type = %v, want %s", payload["type"], payloadType)
	}
	return message, payload
}

func sendPeerResponse(t *testing.T, connection net.Conn, request castMessage, requestPayload, response map[string]any) {
	t.Helper()
	response["requestId"] = requestPayload["requestId"]
	sendPeerPayload(t, connection, castMessage{
		source: request.destination, destination: request.source, namespace: request.namespace,
	}, response)
}

func sendPeerPayload(t *testing.T, connection net.Conn, route castMessage, payload map[string]any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	route.payload = data
	if err := writeCastMessage(connection, route); err != nil {
		t.Fatal(err)
	}
}

func receiverStatusPayloadForTest() map[string]any {
	return map[string]any{
		"type": "RECEIVER_STATUS",
		"status": map[string]any{"applications": []map[string]any{{
			"appId": defaultReceiverApp, "sessionId": "app-session-1", "transportId": "transport-1",
		}}},
	}
}

func mediaStatusPayloadForTest(sessionID int64, uri, state, idleReason string, supported int64) map[string]any {
	status := map[string]any{
		"mediaSessionId": sessionID, "playerState": state, "currentTime": 5,
		"supportedMediaCommands": supported,
		"media":                  map[string]any{"contentId": uri, "contentType": "audio/flac", "duration": 120, "streamType": "BUFFERED"},
	}
	if idleReason != "" {
		status["idleReason"] = idleReason
	}
	return map[string]any{"type": "MEDIA_STATUS", "status": []map[string]any{status}}
}
