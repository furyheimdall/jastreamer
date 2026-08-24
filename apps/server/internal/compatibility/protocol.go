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
)

type Session struct {
	major Major
}

func (session Session) Major() Major {
	return session.major
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
	return []Major{Major2, Major1}
}

func Negotiate(offered []Major) (Session, error) {
	return negotiateMajors(SupportedMajors(), offered)
}

func negotiateMajors(supportedMajors, offered []Major) (Session, error) {
	for _, supported := range supportedMajors {
		if slices.Contains(offered, supported) {
			return Session{major: supported}, nil
		}
	}
	return Session{}, &ProtocolError{
		HTTPStatus: http.StatusUpgradeRequired,
		Code:       "UNSUPPORTED_PROTOCOL_MAJOR",
		Message:    "no supported protocol major was offered",
		Offered:    append([]Major(nil), offered...),
	}
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
