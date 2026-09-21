package browseroutput

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/errorhistory"
	"github.com/jastreamer/jastreamer-server/internal/output"
)

const maximumDiagnosticLogLine = 256 << 10

var nativePlaybackLogPattern = regexp.MustCompile(`^(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}\.\d{6}) diagnostic component=browseroutput event=native_playback_error renderer_id=("(?:\\.|[^"\\])*") play_id=("(?:\\.|[^"\\])*") sequence=([0-9]+) command_id=("(?:\\.|[^"\\])*") playback_errors=("(?:\\.|[^"\\])*")$`)

func (service *Service) reportHistoryEventLocked(value *registration, report Report, playbackErrors string, command *Command) *errorhistory.Event {
	isError := len(report.PlaybackErrors) != 0 || report.Result == "failed" ||
		(report.Result == "" && report.Observation != nil && report.Observation.Event == "error")
	if !isError {
		return nil
	}
	playID := ""
	stage := "playback"
	code := report.ErrorCode
	message := "Browser output reported a playback error."
	var resource *output.Resource
	if command != nil {
		playID = command.PlayID
		stage = command.Action
		message = "Browser output could not complete the renderer command."
		if command.Resource != nil {
			resource = command.Resource
		}
	}
	if report.Observation != nil && report.Observation.PlayID != "" {
		playID = report.Observation.PlayID
	}
	if playID == "" && value.resource != nil {
		playID = value.resource.PlayID
	}
	if resource == nil {
		resource = value.resource
	}
	if len(report.PlaybackErrors) != 0 {
		latest := report.PlaybackErrors[len(report.PlaybackErrors)-1]
		stage = latest.Stage
		code = latest.ErrorName
		message = "Native playback reported a terminal media error."
	}
	details := json.RawMessage(`{}`)
	if playbackErrors != "" {
		details, _ = json.Marshal(map[string]json.RawMessage{"playback_errors": json.RawMessage(playbackErrors)})
	} else {
		details, _ = json.Marshal(map[string]string{"event": observationEvent(report.Observation), "report_code": report.ErrorCode})
	}
	position := reportPosition(report)
	event := &errorhistory.Event{
		ReceivedAt: service.now().UTC(), Kind: "renderer", RendererID: value.id, RendererName: value.name,
		Protocol: output.ProtocolBrowser, Stage: stage, Code: code, Message: message, Outcome: "failed",
		PlayID: playID, CommandID: diagnosticCommandID(value.id, report.Sequence), PositionMS: position, Details: details,
	}
	if resource != nil {
		event.TrackID = resource.TrackID
		event.TrackTitle = resource.Title
	}
	if playbackErrors != "" {
		event.Key = nativePlaybackHistoryKey(value.id, playID, report.Sequence, playbackErrors)
	} else {
		event.Key = browserReportHistoryKey(value.id, playID, report)
	}
	return event
}

func observationEvent(observation *ObservationReport) string {
	if observation == nil {
		return ""
	}
	return observation.Event
}

func reportPosition(report Report) *int64 {
	if len(report.PlaybackErrors) != 0 && report.PlaybackErrors[len(report.PlaybackErrors)-1].PositionMS != nil {
		value := *report.PlaybackErrors[len(report.PlaybackErrors)-1].PositionMS
		return &value
	}
	if report.Observation != nil && report.Observation.HasPosition {
		value := report.Observation.PositionMS
		return &value
	}
	return nil
}

func nativePlaybackHistoryKey(rendererID, playID string, sequence uint64, playbackErrors string) string {
	digest := sha256.Sum256([]byte(playbackErrors))
	return fmt.Sprintf("native:%s:%s:%d:%s", rendererID, playID, sequence, hex.EncodeToString(digest[:16]))
}

func browserReportHistoryKey(rendererID, playID string, report Report) string {
	identity, _ := json.Marshal(struct {
		Result      string             `json:"result"`
		ErrorCode   string             `json:"error_code"`
		Observation *ObservationReport `json:"observation"`
	}{report.Result, report.ErrorCode, report.Observation})
	digest := sha256.Sum256(identity)
	return fmt.Sprintf("browser:%s:%s:%d:%s", rendererID, playID, report.Sequence, hex.EncodeToString(digest[:16]))
}

func (service *Service) recordHistory(event errorhistory.Event) {
	if service.history == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.history.Record(ctx, event); err != nil {
		log.Printf("diagnostic component=browseroutput event=history_record_failed renderer_id=%q command_id=%q error_type=%T", event.RendererID, event.CommandID, err)
	}
}

// ImportErrorHistory imports only structured native_playback_error diagnostics
// emitted by this package. Other log text is never copied into the database.
func ImportErrorHistory(ctx context.Context, history *errorhistory.Service, dataDir string) error {
	if history == nil {
		return errors.New("browser output history import: history service is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var importErrors error
	for _, name := range []string{"server.log.3", "server.log.2", "server.log.1", "server.log"} {
		if err := importErrorHistoryFile(ctx, history, filepath.Join(dataDir, name)); err != nil {
			importErrors = errors.Join(importErrors, err)
		}
	}
	return importErrors
}

func importErrorHistoryFile(ctx context.Context, history *errorhistory.Service, path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("browser output history import: open diagnostic log: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("browser output history import: inspect diagnostic log: %w", err)
	}
	if !info.Mode().IsRegular() {
		return errors.New("browser output history import: diagnostic log is not a regular file")
	}
	// Snapshot the existing rotated log; do not follow concurrent appends.
	reader := bufio.NewReaderSize(io.LimitReader(file, min(info.Size(), 5<<20)), 64<<10)
	return readBoundedLogLines(reader, func(line []byte) error {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		event, ok := parseNativePlaybackLog(line)
		if !ok {
			return nil
		}
		if err := history.Record(ctx, event); err != nil {
			return fmt.Errorf("browser output history import: record diagnostic: %w", err)
		}
		return nil
	})
}

func readBoundedLogLines(reader *bufio.Reader, visit func([]byte) error) error {
	line := make([]byte, 0, 1024)
	discard := false
	for {
		fragment, prefix, err := reader.ReadLine()
		if !discard && len(line)+len(fragment) <= maximumDiagnosticLogLine {
			line = append(line, fragment...)
		} else {
			discard = true
		}
		if !prefix {
			if !discard && len(line) != 0 {
				if visitErr := visit(line); visitErr != nil {
					return visitErr
				}
			}
			line = line[:0]
			discard = false
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("browser output history import: read diagnostic log: %w", err)
		}
	}
}

func parseNativePlaybackLog(line []byte) (errorhistory.Event, bool) {
	match := nativePlaybackLogPattern.FindSubmatch(line)
	if match == nil {
		return errorhistory.Event{}, false
	}
	receivedAt, err := time.ParseInLocation("2006/01/02 15:04:05.000000", string(match[1]), time.UTC)
	if err != nil {
		return errorhistory.Event{}, false
	}
	rendererID, ok := unquoteLogField(match[2])
	if !ok || rendererID == "" || len(rendererID) > 256 {
		return errorhistory.Event{}, false
	}
	playID, ok := unquoteLogField(match[3])
	if !ok || len(playID) > 256 {
		return errorhistory.Event{}, false
	}
	sequence, err := strconv.ParseUint(string(match[4]), 10, 64)
	if err != nil || sequence == 0 {
		return errorhistory.Event{}, false
	}
	commandID, ok := unquoteLogField(match[5])
	if !ok || commandID != diagnosticCommandID(rendererID, sequence) {
		return errorhistory.Event{}, false
	}
	playbackJSON, ok := unquoteLogField(match[6])
	if !ok || len(playbackJSON) > maximumDiagnosticLogLine {
		return errorhistory.Event{}, false
	}
	var playbackErrors []PlaybackError
	if err := json.Unmarshal([]byte(playbackJSON), &playbackErrors); err != nil || len(playbackErrors) == 0 {
		return errorhistory.Event{}, false
	}
	if err := validatePlaybackErrors(Report{Result: "failed", PlaybackErrors: playbackErrors}); err != nil {
		return errorhistory.Event{}, false
	}
	canonical, err := json.Marshal(playbackErrors)
	if err != nil {
		return errorhistory.Event{}, false
	}
	details, err := json.Marshal(map[string]json.RawMessage{"playback_errors": canonical})
	if err != nil {
		return errorhistory.Event{}, false
	}
	latest := playbackErrors[len(playbackErrors)-1]
	position := *latest.PositionMS
	return errorhistory.Event{
		Key: nativePlaybackHistoryKey(rendererID, playID, sequence, string(canonical)), ReceivedAt: receivedAt,
		Kind: "renderer", RendererID: rendererID, Protocol: output.ProtocolBrowser, Stage: latest.Stage,
		Code: latest.ErrorName, Message: "Native playback reported a terminal media error.", Outcome: "failed",
		PlayID: playID, CommandID: commandID, PositionMS: &position, Details: details,
	}, true
}

func unquoteLogField(value []byte) (string, bool) {
	decoded, err := strconv.Unquote(string(value))
	return decoded, err == nil && !strings.ContainsRune(decoded, '\x00')
}
