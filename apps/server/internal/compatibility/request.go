package compatibility

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type PeerKind string

const (
	PeerControl  PeerKind = "control"
	PeerRenderer PeerKind = "renderer"
)

type Request struct {
	major        Major
	capabilities []string
	id           string
	behavior     string
}

func (request Request) Major() Major {
	return request.major
}

func (request Request) Capabilities() []string {
	return append([]string(nil), request.capabilities...)
}

func (request Request) ID() string {
	return request.id
}

type RequestError struct {
	HTTPStatus int    `json:"-"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	RequestID  string `json:"request_id,omitempty"`
}

func (requestError *RequestError) Error() string {
	return requestError.Code + ": " + requestError.Message
}

type wireRequest struct {
	ProtocolMajor    *int      `json:"protocolMajor"`
	Capabilities     *[]string `json:"capabilities"`
	RequestID        *string   `json:"requestId"`
	CommandID        *string   `json:"commandId"`
	ContinuationMode *string   `json:"continuationPolicy"`
	CommandKind      *string   `json:"commandKind"`
	PositionMS       *int64    `json:"positionMs"`
}

func ParseRequest(kind PeerKind, payload []byte) (Request, error) {
	var wire wireRequest
	if err := json.Unmarshal(payload, &wire); err != nil {
		return Request{}, invalidRequest("request must be one JSON object")
	}
	if wire.ProtocolMajor == nil || *wire.ProtocolMajor < 1 || *wire.ProtocolMajor > 65535 {
		return Request{}, invalidRequest("protocolMajor is required and must be a positive integer")
	}
	if wire.Capabilities == nil {
		return Request{}, invalidRequest("capabilities is required")
	}
	for _, capability := range *wire.Capabilities {
		if strings.TrimSpace(capability) == "" {
			return Request{}, invalidRequest("capabilities must contain non-empty strings")
		}
	}
	request := Request{major: Major(*wire.ProtocolMajor), capabilities: append([]string(nil), (*wire.Capabilities)...)}
	switch kind {
	case PeerControl:
		if wire.RequestID == nil || strings.TrimSpace(*wire.RequestID) == "" {
			return Request{}, invalidRequest("requestId is required")
		}
		request.id = *wire.RequestID
		if wire.ContinuationMode != nil {
			request.behavior = "continuation:" + *wire.ContinuationMode
		}
	case PeerRenderer:
		if wire.CommandID == nil || strings.TrimSpace(*wire.CommandID) == "" {
			return Request{}, invalidRequest("commandId is required")
		}
		if wire.PositionMS != nil && *wire.PositionMS < 0 {
			return Request{}, invalidRequest("positionMs must not be negative")
		}
		request.id = *wire.CommandID
		if wire.CommandKind != nil {
			request.behavior = *wire.CommandKind
		}
	default:
		return Request{}, invalidRequest(fmt.Sprintf("unknown peer kind %q", kind))
	}
	return request, nil
}

func invalidRequest(message string) *RequestError {
	return &RequestError{HTTPStatus: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: message}
}
