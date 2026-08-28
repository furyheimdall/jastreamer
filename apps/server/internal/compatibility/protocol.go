package compatibility

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
)

type Major uint16

const (
	Major1 Major = 1
	Major2 Major = 2
	Major3 Major = 3

	SupportedMajorsHeader = "X-Jake-Supported-Protocol-Majors"
	SelectedMajorHeader   = "X-Jake-Selected-Protocol-Major"
)

type Session struct {
	major        Major
	capabilities []string
}

func (session Session) Major() Major {
	return session.major
}

func (session Session) Capabilities() []string {
	return append([]string(nil), session.capabilities...)
}

type ProtocolError struct {
	HTTPStatus int     `json:"-"`
	Code       string  `json:"code"`
	Message    string  `json:"message"`
	Offered    []Major `json:"offered_protocol_majors,omitempty"`
}

func (protocolError *ProtocolError) Error() string {
	return protocolError.Code + ": " + protocolError.Message
}

func SupportedMajors() []Major {
	return []Major{Major3, Major2}
}

func Negotiate(offered []Major) (Session, error) {
	return negotiateMajors(SupportedMajors(), offered)
}

func negotiateMajors(supportedMajors, offered []Major) (Session, error) {
	for _, supported := range supportedMajors {
		if slices.Contains(offered, supported) {
			if supported == Major2 {
				return Session{major: supported, capabilities: []string{"control-api", "render"}}, nil
			}
			return Session{major: supported, capabilities: []string{"control-api", "render", "catalog-browse", "queue-mutation", "transport", "zones", "renderer-assignment", "event-invalidations", "renderer-session", "media-representations"}}, nil
		}
	}
	return Session{}, &ProtocolError{
		HTTPStatus: http.StatusUpgradeRequired,
		Code:       "UNSUPPORTED_PROTOCOL_MAJOR",
		Message:    "no supported protocol major was offered",
		Offered:    append([]Major(nil), offered...),
	}
}

type NegotiationRequest struct {
	Offered []Major
}

type NegotiationResponse struct {
	Session               Session
	SupportedMajorsHeader string
	SelectedMajorHeader   string
}

func NegotiateResponse(request NegotiationRequest) (NegotiationResponse, error) {
	session, err := Negotiate(request.Offered)
	if err != nil {
		return NegotiationResponse{}, err
	}
	return NegotiationResponse{
		Session:               session,
		SupportedMajorsHeader: FormatMajorHeader(SupportedMajors()),
		SelectedMajorHeader:   session.Major().String(),
	}, nil
}

func FormatMajorHeader(majors []Major) string {
	values := make([]string, len(majors))
	for index, major := range majors {
		values[index] = major.String()
	}
	return strings.Join(values, ",")
}

func ParseMajorHeader(raw string) ([]Major, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	majors := make([]Major, 0, len(parts))
	for _, part := range parts {
		value, err := strconv.ParseUint(strings.TrimSpace(part), 10, 16)
		if err != nil || value == 0 {
			return nil, &RequestError{HTTPStatus: http.StatusBadRequest, Code: "INVALID_REQUEST", Message: "protocol majors must be positive integers"}
		}
		majors = append(majors, Major(value))
	}
	return majors, nil
}

func (major Major) String() string {
	return fmt.Sprintf("%d", major)
}
