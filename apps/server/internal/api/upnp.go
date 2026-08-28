package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/upnp"
)

type UPnPScanner interface {
	Scan(context.Context) (upnp.ScanResult, error)
	LastScan() upnp.ScanResult
}

type k17AdapterProvider interface {
	PlaybackAdapter(playback.RendererID, playback.ZoneID) (playback.K17PlaybackAdapter, error)
}

type upnpDevicePayload struct {
	RendererID  upnp.RendererID `json:"renderer_id"`
	Name        string          `json:"name"`
	UDN         string          `json:"udn"`
	Description string          `json:"description_url"`
	Evidence    upnp.Evidence   `json:"evidence"`
	Actions     []upnp.Action   `json:"actions"`
}

type upnpDiagnosticPayload struct {
	USN  string `json:"usn"`
	Code string `json:"code"`
}

type upnpScanPayload struct {
	Devices     []upnpDevicePayload     `json:"devices"`
	Diagnostics []upnpDiagnosticPayload `json:"diagnostics"`
}

func (service *server) scanK17(writer http.ResponseWriter, request *http.Request) {
	if !service.requireAdmin(writer, request) {
		return
	}
	result, err := service.config.UPnP.Scan(request.Context())
	if err != nil {
		writeError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, upnpPayload(result))
}

func (service *server) lastK17Scan(writer http.ResponseWriter, request *http.Request) {
	if !service.requireAdmin(writer, request) {
		return
	}
	writeJSON(writer, http.StatusOK, upnpPayload(service.config.UPnP.LastScan()))
}

func upnpPayload(result upnp.ScanResult) upnpScanPayload {
	payload := upnpScanPayload{Devices: make([]upnpDevicePayload, 0, len(result.Devices)), Diagnostics: make([]upnpDiagnosticPayload, 0, len(result.Diagnostics))}
	for _, device := range result.Devices {
		payload.Devices = append(payload.Devices, upnpDevicePayload{RendererID: device.ID, Name: device.FriendlyName, UDN: device.UDN, Description: device.DescriptionURL, Evidence: device.Evidence, Actions: device.Actions()})
	}
	for _, diagnostic := range result.Diagnostics {
		payload.Diagnostics = append(payload.Diagnostics, upnpDiagnosticPayload{USN: diagnostic.Candidate.USN, Code: upnpDiagnosticCode(diagnostic.Error)})
	}
	return payload
}

func upnpDiagnosticCode(err error) string {
	switch {
	case errors.Is(err, upnp.ErrIdentityRejected):
		return "K17_IDENTITY_REJECTED"
	case errors.Is(err, upnp.ErrFirmwareRejected):
		return "K17_FIRMWARE_REJECTED"
	case errors.Is(err, upnp.ErrProtocolRejected):
		return "K17_PROTOCOL_REJECTED"
	case errors.Is(err, upnp.ErrOffSubnetURL):
		return "UPNP_OFF_SUBNET_URL"
	default:
		return "UPNP_DESCRIPTION_REJECTED"
	}
}
