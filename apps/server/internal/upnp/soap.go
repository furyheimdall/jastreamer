package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type soapRequest struct {
	URL        string
	Service    string
	Action     string
	Arguments  map[string]string
	HTTPClient *http.Client
}

type soapFaultEnvelope struct {
	Fault struct {
		Code        int    `xml:"detail>UPnPError>errorCode"`
		Description string `xml:"detail>UPnPError>errorDescription"`
	} `xml:"Body>Fault"`
}

func executeSOAP(ctx context.Context, request soapRequest) ([]byte, error) {
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:`)
	body.WriteString(request.Action)
	body.WriteString(` xmlns:u="`)
	body.WriteString(request.Service)
	body.WriteString(`">`)
	for name, value := range request.Arguments {
		body.WriteByte('<')
		body.WriteString(name)
		body.WriteByte('>')
		if err := xml.EscapeText(&body, []byte(value)); err != nil {
			return nil, fmt.Errorf("escape SOAP %s: %w", request.Action, err)
		}
		body.WriteString("</")
		body.WriteString(name)
		body.WriteByte('>')
	}
	body.WriteString("</u:")
	body.WriteString(request.Action)
	body.WriteString(`></s:Body></s:Envelope>`)
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, request.URL, &body)
	if err != nil {
		return nil, fmt.Errorf("create SOAP %s: %w", request.Action, err)
	}
	httpRequest.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	httpRequest.Header.Set("SOAPAction", `"`+request.Service+`#`+request.Action+`"`)
	response, err := request.HTTPClient.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maxXMLBody+1))
	if err != nil {
		return nil, fmt.Errorf("read SOAP %s: %w", request.Action, err)
	}
	if len(data) > maxXMLBody {
		return nil, ErrInvalidDescription
	}
	if response.StatusCode >= http.StatusBadRequest {
		var envelope soapFaultEnvelope
		if decodeErr := decodeBoundedXML(bytes.NewReader(data), &envelope); decodeErr != nil {
			return nil, &DiagnosticError{Kind: DiagnosticResponse, Action: Action(request.Action), Cause: errors.New("invalid SOAP fault")}
		}
		return nil, &SOAPFault{Action: Action(request.Action), Code: envelope.Fault.Code, Description: envelope.Fault.Description}
	}
	if response.StatusCode != http.StatusOK {
		return nil, &DiagnosticError{Kind: DiagnosticResponse, Action: Action(request.Action), Cause: fmt.Errorf("HTTP status %d", response.StatusCode)}
	}
	return data, nil
}

func (inspector *Inspector) getProtocolInfo(ctx context.Context, candidate Candidate, controlURL string) (string, error) {
	data, err := executeSOAP(ctx, soapRequest{URL: controlURL, Service: connectionManagerService, Action: "GetProtocolInfo", HTTPClient: inspector.client(candidate), Arguments: map[string]string{}})
	if err != nil {
		return "", fmt.Errorf("query protocolInfo: %w", err)
	}
	var response protocolInfoResponse
	if err := decodeBoundedXML(bytes.NewReader(data), &response); err != nil {
		return "", ErrProtocolRejected
	}
	return strings.TrimSpace(response.Sink), nil
}

func parseDuration(raw string) (time.Duration, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return 0, ErrInvalidDescription
	}
	hours, hoursErr := strconv.ParseInt(parts[0], 10, 64)
	minutes, minutesErr := strconv.ParseInt(parts[1], 10, 64)
	seconds, secondsErr := strconv.ParseInt(parts[2], 10, 64)
	if hoursErr != nil || minutesErr != nil || secondsErr != nil || hours < 0 || minutes < 0 || minutes > 59 || seconds < 0 || seconds > 59 {
		return 0, ErrInvalidDescription
	}
	return time.Duration(hours)*time.Hour + time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, nil
}
