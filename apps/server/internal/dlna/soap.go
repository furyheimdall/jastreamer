package dlna

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/jastreamer/jastreamer-server/internal/output"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const actionTimeout = 5 * time.Second

const soapEnvelopeNamespace = "http://schemas.xmlsoap.org/soap/envelope/"

type soapArgument struct {
	name  string
	value string
}

type soapCall struct {
	candidate advertisement
	url       string
	service   string
	action    string
	arguments []soapArgument
}

type soapResponseEnvelope struct {
	XMLName xml.Name
	Bodies  []soapResponseBody `xml:"Body"`
}

type soapResponseBody struct {
	XMLName  xml.Name
	Elements []soapBodyElement `xml:",any"`
}

type soapBodyElement struct {
	XMLName    xml.Name
	FaultCode  string `xml:"faultcode"`
	FaultText  string `xml:"faultstring"`
	UPnPDetail struct {
		Code        string `xml:"errorCode"`
		Description string `xml:"errorDescription"`
	} `xml:"detail>UPnPError"`
}

func (manager *Manager) executeSOAP(ctx context.Context, call soapCall) ([]byte, error) {
	if _, err := trustedAbsoluteURL(call.url, call.candidate.source.Addr(), call.candidate.network); err != nil {
		return nil, output.NewActionError(output.ErrorTransport, call.action, 0, ErrUnavailable)
	}
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:`)
	body.WriteString(call.action)
	body.WriteString(` xmlns:u="`)
	if err := xml.EscapeText(&body, []byte(call.service)); err != nil {
		return nil, output.NewActionError(output.ErrorResponse, call.action, 0, ErrInvalidResponse)
	}
	body.WriteString(`">`)
	for _, argument := range call.arguments {
		body.WriteByte('<')
		body.WriteString(argument.name)
		body.WriteByte('>')
		if err := xml.EscapeText(&body, []byte(argument.value)); err != nil {
			return nil, output.NewActionError(output.ErrorResponse, call.action, 0, ErrInvalidResponse)
		}
		body.WriteString("</")
		body.WriteString(argument.name)
		body.WriteByte('>')
	}
	body.WriteString("</u:")
	body.WriteString(call.action)
	body.WriteString(`></s:Body></s:Envelope>`)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, call.url, &body)
	if err != nil {
		return nil, output.NewActionError(output.ErrorTransport, call.action, 0, ErrUnavailable)
	}
	request.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	request.Header.Set("SOAPAction", `"`+call.service+`#`+call.action+`"`)
	response, err := manager.soapClientFor(call.candidate.source.Addr(), call.candidate.network).Do(request)
	if err != nil {
		return nil, classifyActionError(ctx, call.action, err)
	}
	defer response.Body.Close()
	data, err := readBoundedXML(response.Body)
	if err != nil {
		return nil, output.NewActionError(output.ErrorResponse, call.action, 0, err)
	}
	faultCode, fault, err := validateSOAPResponse(data, call.service, call.action)
	if err != nil {
		return nil, output.NewActionError(output.ErrorResponse, call.action, 0, err)
	}
	if fault {
		if faultCode == 401 {
			return nil, output.NewActionError(output.ErrorUnsupported, call.action, faultCode, ErrUnsupported)
		}
		return nil, output.NewActionError(output.ErrorFault, call.action, faultCode, ErrUnavailable)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, output.NewActionError(output.ErrorResponse, call.action, 0, ErrInvalidResponse)
	}
	return data, nil
}

func validateSOAPResponse(data []byte, service, action string) (int, bool, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	decoder.Strict = true
	var envelope soapResponseEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return 0, false, ErrInvalidResponse
	}
	if envelope.XMLName.Local != "Envelope" || envelope.XMLName.Space != soapEnvelopeNamespace ||
		len(envelope.Bodies) != 1 ||
		envelope.Bodies[0].XMLName.Local != "Body" || envelope.Bodies[0].XMLName.Space != soapEnvelopeNamespace ||
		len(envelope.Bodies[0].Elements) != 1 {
		return 0, false, ErrInvalidResponse
	}
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, false, ErrInvalidResponse
		}
		switch value := token.(type) {
		case xml.StartElement:
			return 0, false, ErrInvalidResponse
		case xml.CharData:
			if len(bytes.TrimSpace(value)) != 0 {
				return 0, false, ErrInvalidResponse
			}
		}
	}
	element := envelope.Bodies[0].Elements[0]
	if element.XMLName.Local == "Fault" && element.XMLName.Space == soapEnvelopeNamespace {
		if strings.TrimSpace(element.FaultCode) == "" || strings.TrimSpace(element.FaultText) == "" {
			return 0, false, ErrInvalidResponse
		}
		code, _ := strconv.Atoi(strings.TrimSpace(element.UPnPDetail.Code))
		return code, true, nil
	}
	if element.XMLName.Local != action+"Response" || element.XMLName.Space != service {
		return 0, false, ErrInvalidResponse
	}
	return 0, false, nil
}

func classifyActionError(ctx context.Context, action string, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return output.NewActionError(output.ErrorTimeout, action, 0, ErrTimeout)
	}
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return output.NewActionError(output.ErrorCancelled, action, 0, context.Canceled)
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return output.NewActionError(output.ErrorTimeout, action, 0, ErrTimeout)
	}
	return output.NewActionError(output.ErrorTransport, action, 0, ErrUnavailable)
}

func didlMetadata(resource output.Resource) (string, error) {
	if resource.URL == "" || resource.Mime == "" {
		return "", ErrInvalidResource
	}
	title := strings.TrimSpace(resource.Title)
	if title == "" {
		title = "Track"
	}
	var value bytes.Buffer
	value.WriteString(`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/"><item id="0" parentID="0" restricted="1"><dc:title>`)
	escapeXML(&value, title)
	value.WriteString(`</dc:title>`)
	if resource.Artist != "" {
		value.WriteString(`<upnp:artist>`)
		escapeXML(&value, resource.Artist)
		value.WriteString(`</upnp:artist>`)
	}
	if resource.Album != "" {
		value.WriteString(`<upnp:album>`)
		escapeXML(&value, resource.Album)
		value.WriteString(`</upnp:album>`)
	}
	if resource.ArtworkURL != "" {
		value.WriteString(`<upnp:albumArtURI>`)
		escapeXML(&value, resource.ArtworkURL)
		value.WriteString(`</upnp:albumArtURI>`)
	}
	value.WriteString(`<upnp:class>object.item.audioItem.musicTrack</upnp:class><res protocolInfo="http-get:*:`)
	escapeXML(&value, resource.Mime)
	value.WriteString(`:*"`)
	if resource.DurationMS > 0 {
		value.WriteString(` duration="`)
		value.WriteString(formatClock(resource.DurationMS))
		value.WriteByte('"')
	}
	if resource.Size > 0 {
		value.WriteString(` size="`)
		value.WriteString(strconv.FormatInt(resource.Size, 10))
		value.WriteByte('"')
	}
	value.WriteByte('>')
	escapeXML(&value, resource.URL)
	value.WriteString(`</res></item></DIDL-Lite>`)
	return value.String(), nil
}

func escapeXML(buffer *bytes.Buffer, value string) {
	_ = xml.EscapeText(buffer, []byte(value))
}

func parseClock(raw string) (int64, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.EqualFold(value, "NOT_IMPLEMENTED") {
		return 0, false, nil
	}
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return 0, false, ErrInvalidResponse
	}
	hours, hoursErr := strconv.ParseInt(parts[0], 10, 64)
	minutes, minutesErr := strconv.ParseInt(parts[1], 10, 64)
	secondsText := parts[2]
	secondsWhole, fraction, hasFraction := strings.Cut(secondsText, ".")
	seconds, secondsErr := strconv.ParseInt(secondsWhole, 10, 64)
	if hoursErr != nil || minutesErr != nil || secondsErr != nil || hours < 0 || minutes < 0 || minutes > 59 || seconds < 0 || seconds > 59 {
		return 0, false, ErrInvalidResponse
	}
	milliseconds := int64(0)
	if hasFraction {
		if len(fraction) == 0 || len(fraction) > 9 {
			return 0, false, ErrInvalidResponse
		}
		for _, digit := range fraction {
			if digit < '0' || digit > '9' {
				return 0, false, ErrInvalidResponse
			}
		}
		padded := fraction + "000"
		milliseconds, _ = strconv.ParseInt(padded[:3], 10, 64)
	}
	const maxInt64 = int64(^uint64(0) >> 1)
	tail := minutes*60*1000 + seconds*1000 + milliseconds
	if hours > (maxInt64-tail)/(60*60*1000) {
		return 0, false, ErrInvalidResponse
	}
	return hours*60*60*1000 + tail, true, nil
}

func formatClock(milliseconds int64) string {
	hours := milliseconds / (60 * 60 * 1000)
	minutes := milliseconds / (60 * 1000) % 60
	seconds := milliseconds / 1000 % 60
	fraction := milliseconds % 1000
	if fraction == 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
	}
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, seconds, fraction)
}
