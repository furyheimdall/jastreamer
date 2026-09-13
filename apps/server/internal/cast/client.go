package cast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	receiverID          = "receiver-0"
	defaultReceiverApp  = "CC1AD845"
	connectionNamespace = "urn:x-cast:com.google.cast.tp.connection"
	heartbeatNamespace  = "urn:x-cast:com.google.cast.tp.heartbeat"
	receiverNamespace   = "urn:x-cast:com.google.cast.receiver"
	mediaNamespace      = "urn:x-cast:com.google.cast.media"
	heartbeatInterval   = 5 * time.Second
	heartbeatWriteLimit = 5 * time.Second
)

type client struct {
	connection net.Conn
	source     string
	onClosed   func()
	onMedia    func(string, []mediaStatus)

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan clientResponse
	readErr error
	done    chan struct{}
	close   sync.Once
}

type clientResponse struct {
	message castMessage
}

type payloadHeader struct {
	Type      string `json:"type"`
	RequestID int64  `json:"requestId"`
	Reason    string `json:"reason"`
}

type receiverStatusPayload struct {
	Type      string `json:"type"`
	RequestID int64  `json:"requestId"`
	Status    struct {
		Applications []receiverApplication `json:"applications"`
	} `json:"status"`
}

type receiverApplication struct {
	AppID       string `json:"appId"`
	SessionID   string `json:"sessionId"`
	TransportID string `json:"transportId"`
}

type mediaStatusPayload struct {
	Type      string        `json:"type"`
	RequestID int64         `json:"requestId"`
	Status    []mediaStatus `json:"status"`
}

type mediaStatus struct {
	MediaSessionID    int64            `json:"mediaSessionId"`
	PlayerState       string           `json:"playerState"`
	IdleReason        string           `json:"idleReason"`
	CurrentTime       *float64         `json:"currentTime"`
	SupportedCommands int64            `json:"supportedMediaCommands"`
	PlaybackRate      *float64         `json:"playbackRate"`
	Media             mediaDescription `json:"media"`
}

type mediaDescription struct {
	ContentID   string   `json:"contentId"`
	ContentType string   `json:"contentType"`
	Duration    *float64 `json:"duration"`
	StreamType  string   `json:"streamType"`
}

type remoteError struct {
	responseType string
	reason       string
}

func (err *remoteError) Error() string {
	if err.reason != "" {
		return fmt.Sprintf("Cast receiver returned %s (%s)", err.responseType, err.reason)
	}
	return "Cast receiver returned " + err.responseType
}

func newClient(connection net.Conn, onClosed func(), onMedia func(string, []mediaStatus)) *client {
	return &client{
		connection: connection, source: "sender-" + uuid.NewString(), onClosed: onClosed, onMedia: onMedia,
		pending: make(map[int64]chan clientResponse), done: make(chan struct{}),
	}
}

func (client *client) start() {
	go client.readLoop()
	go client.heartbeatLoop()
}

func (client *client) isAlive() bool {
	select {
	case <-client.done:
		return false
	default:
		return true
	}
}

func (client *client) Close() { client.fail(io.EOF) }

func (client *client) connect(ctx context.Context, destination string) error {
	return client.sendJSON(ctx, destination, connectionNamespace, map[string]any{"type": "CONNECT", "origin": map[string]any{}})
}

func (client *client) receiverStatus(ctx context.Context) (receiverStatusPayload, error) {
	data, err := client.request(ctx, receiverID, receiverNamespace, map[string]any{"type": "GET_STATUS"}, "RECEIVER_STATUS")
	if err != nil {
		return receiverStatusPayload{}, err
	}
	var response receiverStatusPayload
	if err := json.Unmarshal(data, &response); err != nil || response.Type != "RECEIVER_STATUS" {
		return receiverStatusPayload{}, ErrInvalidResponse
	}
	return response, nil
}

func (client *client) launchDefaultReceiver(ctx context.Context) (receiverApplication, error) {
	data, err := client.request(ctx, receiverID, receiverNamespace, map[string]any{"type": "LAUNCH", "appId": defaultReceiverApp}, "RECEIVER_STATUS")
	if err != nil {
		return receiverApplication{}, err
	}
	var response receiverStatusPayload
	if err := json.Unmarshal(data, &response); err != nil {
		return receiverApplication{}, ErrInvalidResponse
	}
	return defaultApplication(response)
}

func (client *client) defaultReceiver(ctx context.Context) (receiverApplication, bool, error) {
	status, err := client.receiverStatus(ctx)
	if err != nil {
		return receiverApplication{}, false, err
	}
	application, err := defaultApplication(status)
	if err == nil {
		return application, true, nil
	}
	if !errors.Is(err, ErrUnavailable) {
		return receiverApplication{}, false, err
	}
	return receiverApplication{}, false, nil
}

func defaultApplication(status receiverStatusPayload) (receiverApplication, error) {
	if len(status.Status.Applications) > 64 {
		return receiverApplication{}, ErrInvalidResponse
	}
	for _, application := range status.Status.Applications {
		if !strings.EqualFold(application.AppID, defaultReceiverApp) {
			continue
		}
		if !validWireString(application.AppID, maxIdentifier) || !validWireString(application.SessionID, maxIdentifier) || !validWireString(application.TransportID, maxIdentifier) {
			return receiverApplication{}, ErrInvalidResponse
		}
		return application, nil
	}
	return receiverApplication{}, ErrUnavailable
}

func (client *client) mediaStatus(ctx context.Context, destination string) ([]mediaStatus, error) {
	data, err := client.request(ctx, destination, mediaNamespace, map[string]any{"type": "GET_STATUS"}, "MEDIA_STATUS")
	if err != nil {
		return nil, err
	}
	return decodeMediaStatus(data)
}

func (client *client) mediaCommand(ctx context.Context, destination string, request map[string]any) ([]mediaStatus, error) {
	data, err := client.request(ctx, destination, mediaNamespace, request, "MEDIA_STATUS")
	if err != nil {
		return nil, err
	}
	return decodeMediaStatus(data)
}

func decodeMediaStatus(data []byte) ([]mediaStatus, error) {
	var response mediaStatusPayload
	if err := json.Unmarshal(data, &response); err != nil || response.Type != "MEDIA_STATUS" || len(response.Status) > 64 {
		return nil, ErrInvalidResponse
	}
	for _, status := range response.Status {
		if status.MediaSessionID < 0 || (status.CurrentTime != nil && !finiteNonnegative(*status.CurrentTime)) ||
			(status.Media.Duration != nil && !finiteNonnegative(*status.Media.Duration)) ||
			(status.PlaybackRate != nil && !finiteNonnegative(*status.PlaybackRate)) ||
			len(status.PlayerState) > 64 || len(status.IdleReason) > 64 || len(status.Media.ContentID) > 8192 || len(status.Media.ContentType) > 255 {
			return nil, ErrInvalidResponse
		}
	}
	return response.Status, nil
}

func (client *client) request(ctx context.Context, destination, namespace string, request map[string]any, expectedType string) ([]byte, error) {
	client.mu.Lock()
	if !client.isAlive() {
		err := client.readErr
		client.mu.Unlock()
		if err == nil {
			err = io.EOF
		}
		return nil, err
	}
	client.nextID++
	requestID := client.nextID
	responses := make(chan clientResponse, 1)
	client.pending[requestID] = responses
	client.mu.Unlock()
	request["requestId"] = requestID
	if err := client.sendJSON(ctx, destination, namespace, request); err != nil {
		client.removePending(requestID)
		client.fail(err)
		return nil, err
	}
	var response clientResponse
	select {
	case response = <-responses:
	case <-ctx.Done():
		select {
		case response = <-responses:
		default:
			client.removePending(requestID)
			return nil, ctx.Err()
		}
	case <-client.done:
		select {
		case response = <-responses:
		default:
			client.removePending(requestID)
			client.mu.Lock()
			err := client.readErr
			client.mu.Unlock()
			if err == nil {
				err = io.EOF
			}
			return nil, err
		}
	}
	if response.message.namespace != namespace {
		return nil, ErrInvalidResponse
	}
	var header payloadHeader
	if err := json.Unmarshal(response.message.payload, &header); err != nil {
		return nil, ErrInvalidResponse
	}
	if header.Type == expectedType {
		return response.message.payload, nil
	}
	if isRemoteError(header.Type) {
		return nil, &remoteError{responseType: header.Type, reason: header.Reason}
	}
	return nil, ErrInvalidResponse
}

func (client *client) readLoop() {
	for {
		message, err := readCastMessage(client.connection)
		if err != nil {
			client.fail(err)
			return
		}
		if message.destination != client.source && message.destination != "*" {
			continue
		}
		var header payloadHeader
		if err := json.Unmarshal(message.payload, &header); err != nil || header.Type == "" || len(header.Type) > 128 || len(header.Reason) > 1024 {
			client.fail(ErrInvalidResponse)
			return
		}
		if message.namespace == heartbeatNamespace {
			switch header.Type {
			case "PING":
				ctx, cancel := context.WithTimeout(context.Background(), heartbeatWriteLimit)
				err := client.sendJSON(ctx, message.source, heartbeatNamespace, map[string]any{"type": "PONG"})
				cancel()
				if err != nil {
					client.fail(err)
					return
				}
			case "PONG":
			default:
				client.fail(ErrInvalidResponse)
				return
			}
			continue
		}
		if message.namespace == connectionNamespace && header.Type == "CLOSE" {
			client.fail(io.EOF)
			return
		}
		if header.RequestID > 0 && client.deliver(header.RequestID, message) {
			continue
		}
		if message.namespace == mediaNamespace && header.Type == "MEDIA_STATUS" && client.onMedia != nil {
			statuses, err := decodeMediaStatus(message.payload)
			if err != nil {
				client.fail(err)
				return
			}
			client.onMedia(message.source, statuses)
		}
	}
}

func (client *client) heartbeatLoop() {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-client.done:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), heartbeatWriteLimit)
			err := client.sendJSON(ctx, receiverID, heartbeatNamespace, map[string]any{"type": "PING"})
			cancel()
			if err != nil {
				client.fail(err)
				return
			}
		}
	}
}

func (client *client) deliver(requestID int64, message castMessage) bool {
	client.mu.Lock()
	responses, ok := client.pending[requestID]
	if ok {
		delete(client.pending, requestID)
	}
	client.mu.Unlock()
	if ok {
		responses <- clientResponse{message: message}
	}
	return ok
}

func (client *client) removePending(requestID int64) {
	client.mu.Lock()
	delete(client.pending, requestID)
	client.mu.Unlock()
}

func (client *client) fail(err error) {
	client.close.Do(func() {
		client.mu.Lock()
		client.readErr = err
		client.mu.Unlock()
		_ = client.connection.Close()
		close(client.done)
		if client.onClosed != nil {
			client.onClosed()
		}
	})
}

func isRemoteError(value string) bool {
	switch value {
	case "INVALID_REQUEST", "INVALID_PLAYER_STATE", "LOAD_FAILED", "LOAD_CANCELLED", "LAUNCH_ERROR", "ERROR":
		return true
	default:
		return false
	}
}

func (client *client) sendJSON(ctx context.Context, destination, namespace string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil || len(data) == 0 || len(data) > maxPayload {
		return ErrInvalidResponse
	}
	client.writeMu.Lock()
	defer client.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		_ = client.connection.SetWriteDeadline(deadline)
	} else {
		_ = client.connection.SetWriteDeadline(time.Now().Add(heartbeatWriteLimit))
	}
	err = writeCastMessage(client.connection, castMessage{
		source: client.source, destination: destination, namespace: namespace, payload: data,
	})
	_ = client.connection.SetWriteDeadline(time.Time{})
	return err
}
