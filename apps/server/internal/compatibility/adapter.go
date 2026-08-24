package compatibility

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type StartOrder string

type FixtureError struct {
	Message string
}

func (fixtureError *FixtureError) Error() string {
	return "invalid fixture: " + fixtureError.Message
}

const (
	OldFirst StartOrder = "old-first"
	NewFirst StartOrder = "new-first"
)

type Adapter struct {
	session          Session
	acceptedRequests uint64
}

type Response struct {
	RequestID     string `json:"request_id"`
	ProtocolMajor Major  `json:"protocol_major"`
	Status        string `json:"status"`
}

func NewAdapter(session Session) *Adapter {
	return &Adapter{session: session}
}

func (adapter *Adapter) AcceptedRequests() uint64 {
	return adapter.acceptedRequests
}

func (adapter *Adapter) Handle(request Request) (Response, error) {
	if request.major != adapter.session.major {
		return Response{}, &ProtocolError{
			HTTPStatus: http.StatusUpgradeRequired,
			Code:       "UNSUPPORTED_PROTOCOL_MAJOR",
			Message:    "request protocol major differs from the negotiated session",
			Offered:    []Major{request.major},
		}
	}
	switch request.behavior {
	case "", "play", "stop", "seek", "set-position", "continuation:stop", "continuation:album", "continuation:similar":
		adapter.acceptedRequests++
		return Response{RequestID: request.id, ProtocolMajor: adapter.session.major, Status: "accepted"}, nil
	default:
		return Response{}, &RequestError{
			HTTPStatus: http.StatusNotImplemented,
			Code:       "UNSUPPORTED_COMMAND",
			Message:    "command is not supported by the negotiated protocol adapter",
			RequestID:  request.id,
		}
	}
}

type FixtureInput struct {
	Kind         PeerKind
	Order        StartOrder
	ServerMajors []Major
	Peer         []byte
	Wire         []byte
}

type FixtureReport struct {
	Status           string     `json:"status"`
	PeerID           string     `json:"peer_id"`
	StartOrder       StartOrder `json:"start_order"`
	ProtocolMajor    Major      `json:"protocol_major"`
	RequestStatus    string     `json:"request_status"`
	RequestErrorCode string     `json:"request_error_code,omitempty"`
	Steps            []string   `json:"steps"`
}

type peerFixture struct {
	ID              string   `json:"id"`
	Component       PeerKind `json:"component"`
	Version         string   `json:"version"`
	SupportedMajors []int    `json:"supportedMajors"`
	Capabilities    []string `json:"capabilities"`
}

func RunFixture(input FixtureInput) (FixtureReport, error) {
	var peer peerFixture
	serverMajors := append([]Major(nil), input.ServerMajors...)
	if len(serverMajors) == 0 {
		serverMajors = SupportedMajors()
	}
	steps := make([]string, 0, 5)
	switch input.Order {
	case OldFirst:
		if err := json.Unmarshal(input.Peer, &peer); err != nil {
			return FixtureReport{}, fmt.Errorf("start peer from fixture: %w", err)
		}
		steps = append(steps, "start-peer")
		steps = append(steps, "start-server")
	case NewFirst:
		steps = append(steps, "start-server")
		if err := json.Unmarshal(input.Peer, &peer); err != nil {
			return FixtureReport{}, fmt.Errorf("start peer from fixture: %w", err)
		}
		steps = append(steps, "start-peer")
	default:
		return FixtureReport{}, &FixtureError{Message: fmt.Sprintf("unknown start order %q", input.Order)}
	}
	if peer.ID == "" || peer.Component != input.Kind || peer.Version == "" || peer.SupportedMajors == nil || peer.Capabilities == nil {
		return FixtureReport{}, &FixtureError{Message: "peer metadata is missing required known fields"}
	}
	majors := make([]Major, len(peer.SupportedMajors))
	for index, major := range peer.SupportedMajors {
		if major < 1 || major > 65535 {
			return FixtureReport{}, &FixtureError{Message: "peer metadata contains an invalid protocol major"}
		}
		majors[index] = Major(major)
	}
	session, err := negotiateMajors(serverMajors, majors)
	if err != nil {
		return FixtureReport{}, fmt.Errorf("negotiate peer %s: %w", peer.ID, err)
	}
	steps = append(steps, "negotiate")
	request, err := ParseRequest(input.Kind, input.Wire)
	if err != nil {
		return FixtureReport{}, fmt.Errorf("parse peer %s request: %w", peer.ID, err)
	}
	steps = append(steps, "parse-request")
	adapter := NewAdapter(session)
	_, handleErr := adapter.Handle(request)
	steps = append(steps, "handle-request")
	report := FixtureReport{
		Status: "compatible", PeerID: peer.ID, StartOrder: input.Order, ProtocolMajor: session.Major(),
		RequestStatus: "accepted", Steps: steps,
	}
	if handleErr == nil {
		return report, nil
	}
	var requestError *RequestError
	if errors.As(handleErr, &requestError) {
		report.RequestStatus = "unsupported"
		report.RequestErrorCode = requestError.Code
		return report, nil
	}
	return FixtureReport{}, fmt.Errorf("handle peer %s request: %w", peer.ID, handleErr)
}
