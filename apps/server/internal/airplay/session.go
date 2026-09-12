package airplay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

const (
	processStopTimeout    = 10 * time.Second
	processStartTimeout   = 20 * time.Second
	maxHelperLine         = 64 << 10
	maxProcessError       = 16 << 10
	maxRendererErrorRunes = 512
	maxArtworkBytes       = 5 << 20
)

type playbackSession struct {
	manager  *Manager
	device   deviceRecord
	prepared preparedResource

	opMu sync.Mutex
	mu   sync.RWMutex

	state      string
	positionMS int64
	active     *senderProcess
	lastError  string
}

type helperDevice struct {
	ID               string            `json:"id"`
	Name             string            `json:"name"`
	Address          string            `json:"address"`
	LocalAddress     string            `json:"local_address"`
	Port             int               `json:"port"`
	Properties       map[string]string `json:"properties"`
	PairingRequired  bool              `json:"pairing_required"`
	PasswordRequired bool              `json:"password_required"`
}

type helperMetadata struct {
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	DurationMS int64  `json:"duration_ms"`
}

type helperStreamRequest struct {
	Operation   string         `json:"op"`
	Protocol    int            `json:"protocol"`
	Device      helperDevice   `json:"device"`
	ClientID    string         `json:"client_id"`
	Credentials string         `json:"credentials,omitempty"`
	Password    string         `json:"password,omitempty"`
	Metadata    helperMetadata `json:"metadata"`
	ArtworkFD   *int           `json:"artwork_fd,omitempty"`
}

type helperEvent struct {
	Event       string `json:"event"`
	Protocol    int    `json:"protocol"`
	Kind        string `json:"kind"`
	Message     string `json:"message"`
	PositionMS  int64  `json:"position_ms"`
	LatencyMS   int64  `json:"latency_ms"`
	Route       string `json:"route"`
	Credentials string `json:"credentials"`
	Prompt      string `json:"prompt"`
}

type terminalEvent struct {
	event string
	pos   int64
	err   error
}

type senderProcess struct {
	session *playbackSession
	baseMS  int64
	helper  *exec.Cmd
	decoder *exec.Cmd
	stdin   io.WriteCloser
	start   func() error

	playing       chan struct{}
	terminal      chan terminalEvent
	done          chan struct{}
	stopRequested chan struct{}
	decoderDone   chan struct{}
	decoderErr    error
	decoderOutput *limitedBuffer
	helperOutput  *limitedBuffer
	playingOnce   sync.Once
	terminalOne   sync.Once
	stopOnce      sync.Once
}

func newPlaybackSession(manager *Manager, device deviceRecord, prepared preparedResource) *playbackSession {
	return &playbackSession{manager: manager, device: device, prepared: prepared, state: "STOPPED"}
}

func (session *playbackSession) playID() string { return session.prepared.resource.PlayID }

func (session *playbackSession) play(ctx context.Context) error {
	session.opMu.Lock()
	defer session.opMu.Unlock()
	session.mu.RLock()
	state, position, active := session.state, session.positionMS, session.active
	session.mu.RUnlock()
	if state == "PLAYING" && active != nil {
		return nil
	}
	if position >= session.prepared.resource.DurationMS {
		position = 0
	}
	return session.startLocked(ctx, position, "Play")
}

func (session *playbackSession) startLocked(ctx context.Context, positionMS int64, action string) error {
	process, err := session.newSenderProcess(positionMS)
	if err != nil {
		return actionError(action, err)
	}
	session.mu.Lock()
	session.active = process
	session.positionMS = positionMS
	session.state = "TRANSITIONING"
	session.lastError = ""
	session.mu.Unlock()
	session.manager.config.Notify("renderers")
	if err := process.start(); err != nil {
		session.mu.Lock()
		if session.active == process {
			session.active = nil
			session.state = "STOPPED"
		}
		session.mu.Unlock()
		return actionError(action, err)
	}
	waitContext, cancel := context.WithTimeout(ctx, processStartTimeout)
	defer cancel()
	select {
	case <-process.playing:
		return nil
	case terminal := <-process.terminal:
		if terminal.err == nil {
			terminal.err = errors.New("AirPlay session stopped before playback began")
		}
		return actionError(action, terminal.err)
	case <-waitContext.Done():
		process.kill()
		return classifyContext(action, waitContext.Err())
	}
}

func (session *playbackSession) pause(ctx context.Context) error {
	session.opMu.Lock()
	defer session.opMu.Unlock()
	session.mu.RLock()
	state, active := session.state, session.active
	session.mu.RUnlock()
	if state == "PAUSED_PLAYBACK" || state == "STOPPED" || active == nil {
		return nil
	}
	session.setTransitioning()
	terminal, err := stopAndWait(ctx, active, "Pause")
	if err != nil {
		return err
	}
	session.mu.Lock()
	if terminal.event == "eof" {
		session.state = "STOPPED"
		session.positionMS = session.prepared.resource.DurationMS
	} else {
		session.state = "PAUSED_PLAYBACK"
		session.positionMS = clampPosition(active.baseMS+terminal.pos, session.prepared.resource.DurationMS)
	}
	session.mu.Unlock()
	session.manager.config.Notify("renderers")
	return nil
}

func (session *playbackSession) stop(ctx context.Context) error {
	session.opMu.Lock()
	defer session.opMu.Unlock()
	session.mu.RLock()
	active := session.active
	session.mu.RUnlock()
	if active != nil {
		session.setTransitioning()
		if _, err := stopAndWait(ctx, active, "Stop"); err != nil {
			return err
		}
	}
	session.mu.Lock()
	session.state = "STOPPED"
	session.positionMS = 0
	session.mu.Unlock()
	session.manager.config.Notify("renderers")
	return nil
}

func (session *playbackSession) seek(ctx context.Context, positionMS int64) error {
	if positionMS < 0 || positionMS > session.prepared.resource.DurationMS {
		return output.NewActionError(output.ErrorUnsupported, "Seek", 0, errors.New("seek position is outside the track"))
	}
	session.opMu.Lock()
	defer session.opMu.Unlock()
	session.mu.RLock()
	wasPlaying := session.state == "PLAYING" || session.state == "TRANSITIONING"
	active := session.active
	session.mu.RUnlock()
	if active != nil {
		session.setTransitioning()
		if _, err := stopAndWait(ctx, active, "Seek"); err != nil {
			return err
		}
	}
	session.mu.Lock()
	session.positionMS = positionMS
	if wasPlaying && positionMS < session.prepared.resource.DurationMS {
		session.state = "TRANSITIONING"
	} else if positionMS == session.prepared.resource.DurationMS {
		session.state = "STOPPED"
	} else {
		session.state = "PAUSED_PLAYBACK"
	}
	session.mu.Unlock()
	if wasPlaying && positionMS < session.prepared.resource.DurationMS {
		return session.startLocked(ctx, positionMS, "Seek")
	}
	session.manager.config.Notify("renderers")
	return nil
}

func (session *playbackSession) observe() output.Observation {
	session.mu.RLock()
	state, position, lastError := session.state, session.positionMS, session.lastError
	session.mu.RUnlock()
	status := "OK"
	if lastError != "" {
		status = "ERROR_OCCURRED"
	}
	return output.Observation{
		State: state, PositionMS: clampPosition(position, session.prepared.resource.DurationMS),
		DurationMS: session.prepared.resource.DurationMS, URI: session.prepared.resource.URL,
		HasURI: true, HasPosition: true, ObservedAt: time.Now().UTC(), TransportStatus: status,
	}
}

func (session *playbackSession) setTransitioning() {
	session.mu.Lock()
	session.state = "TRANSITIONING"
	session.mu.Unlock()
	session.manager.config.Notify("renderers")
}

func (session *playbackSession) stopAsync() {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), processStopTimeout)
		defer cancel()
		_ = session.stop(ctx)
	}()
}

func stopAndWait(ctx context.Context, process *senderProcess, action string) (terminalEvent, error) {
	waitContext, cancel := context.WithTimeout(ctx, processStopTimeout)
	defer cancel()
	process.requestStop()
	select {
	case terminal := <-process.terminal:
		if terminal.err != nil {
			return terminal, actionError(action, terminal.err)
		}
		return terminal, nil
	case <-waitContext.Done():
		process.kill()
		return terminalEvent{}, classifyContext(action, waitContext.Err())
	}
}

func (session *playbackSession) newSenderProcess(positionMS int64) (*senderProcess, error) {
	format, err := selectAudioFormat(session.device.endpoint.properties)
	if err != nil {
		return nil, err
	}
	source, _, err := session.manager.library.Open(context.Background(), session.prepared.track.ID)
	if err != nil {
		return nil, fmt.Errorf("open library track: %w", err)
	}
	pcmReader, pcmWriter, err := os.Pipe()
	if err != nil {
		source.Close()
		return nil, fmt.Errorf("create PCM pipe: %w", err)
	}
	var artwork *os.File
	if session.prepared.track.ArtworkID != "" {
		candidate, mime, artworkErr := session.manager.library.Artwork(context.Background(), session.prepared.track.ArtworkID)
		if artworkErr == nil {
			if info, statErr := candidate.Stat(); statErr == nil && info.Size() <= maxArtworkBytes && mime == "image/jpeg" {
				artwork = candidate
			} else {
				candidate.Close()
			}
		}
	}
	extraFiles := []*os.File{pcmReader}
	var artworkFD *int
	if artwork != nil {
		fd := 4
		artworkFD = &fd
		extraFiles = append(extraFiles, artwork)
	}
	request := helperStreamRequest{
		Operation: "stream", Protocol: helperProtocolVersion, Device: helperDevice{
			ID: session.device.device.ID, Name: session.device.device.Name,
			Address: session.device.endpoint.address.String(), LocalAddress: session.device.endpoint.localAddress.String(),
			Port: session.device.endpoint.port, Properties: format.normalizedProperties(session.device.endpoint.properties),
			PairingRequired: session.device.endpoint.pairingRequired, PasswordRequired: session.device.endpoint.passwordRequired,
		}, ClientID: session.manager.identity, Credentials: session.device.auth.credentials, Password: session.device.auth.password,
		Metadata:  helperMetadata{Title: session.prepared.track.Title, Artist: session.prepared.track.Artist, Album: session.prepared.track.Album, DurationMS: session.prepared.track.DurationMS - positionMS},
		ArtworkFD: artworkFD,
	}
	helper := exec.Command(session.manager.config.HelperPath)
	helper.ExtraFiles = extraFiles
	helperInput, err := helper.StdinPipe()
	if err != nil {
		closeFiles(source, pcmReader, pcmWriter, artwork)
		return nil, fmt.Errorf("create helper control pipe: %w", err)
	}
	helperOutput, err := helper.StdoutPipe()
	if err != nil {
		closeFiles(source, pcmReader, pcmWriter, artwork)
		return nil, fmt.Errorf("create helper status pipe: %w", err)
	}
	helperErrors := &limitedBuffer{buffer: &bytes.Buffer{}, remaining: maxProcessError}
	helper.Stderr = helperErrors
	decoderOutput := &limitedBuffer{buffer: &bytes.Buffer{}, remaining: maxProcessError}
	decoder := exec.Command(session.manager.config.FFmpegPath,
		"-nostdin", "-hide_banner", "-loglevel", "error", "-xerror", "-i", "/proc/self/fd/3",
		"-ss", formatFFmpegTime(positionMS), "-map", "0:a:0", "-vn", "-sn", "-dn",
		"-ac", strconv.Itoa(format.channels), "-ar", strconv.Itoa(format.sampleRate),
		"-c:a", fmt.Sprintf("pcm_s%dbe", format.sampleSize), "-f", fmt.Sprintf("s%dbe", format.sampleSize), "pipe:1")
	decoder.ExtraFiles = []*os.File{source}
	decoder.Stdout = pcmWriter
	decoder.Stderr = decoderOutput
	process := &senderProcess{
		session: session, baseMS: positionMS, helper: helper, decoder: decoder, stdin: helperInput,
		playing: make(chan struct{}), terminal: make(chan terminalEvent, 1), done: make(chan struct{}),
		stopRequested: make(chan struct{}), decoderDone: make(chan struct{}),
		decoderOutput: decoderOutput, helperOutput: helperErrors,
	}
	processStart := func() error {
		if err := helper.Start(); err != nil {
			closeFiles(source, pcmReader, pcmWriter, artwork)
			return fmt.Errorf("start AirPlay helper: %w", err)
		}
		_ = pcmReader.Close()
		if artwork != nil {
			_ = artwork.Close()
		}
		if err := json.NewEncoder(helperInput).Encode(request); err != nil {
			process.kill()
			_ = helper.Wait()
			closeFiles(source, pcmWriter)
			return fmt.Errorf("configure AirPlay helper: %w", err)
		}
		if err := decoder.Start(); err != nil {
			process.kill()
			_ = helper.Wait()
			closeFiles(source, pcmWriter)
			return fmt.Errorf("start FFmpeg: %w", err)
		}
		go func() {
			process.decoderErr = decoder.Wait()
			close(process.decoderDone)
		}()
		_ = source.Close()
		_ = pcmWriter.Close()
		go process.monitor(helperOutput)
		return nil
	}
	process.start = processStart
	return process, nil
}

// start is assigned while all inherited descriptors remain open in the parent.
// It is a field rather than a method so construction failure can close them first.
func (process *senderProcess) requestStop() {
	process.stopOnce.Do(func() {
		close(process.stopRequested)
		_ = json.NewEncoder(process.stdin).Encode(map[string]string{"op": "stop"})
		if process.decoder.Process != nil {
			_ = process.decoder.Process.Kill()
		}
	})
}

func (process *senderProcess) stopping() bool {
	select {
	case <-process.stopRequested:
		return true
	default:
		return false
	}
}

func (process *senderProcess) kill() {
	if process.decoder.Process != nil {
		_ = process.decoder.Process.Kill()
	}
	if process.helper.Process != nil {
		_ = process.helper.Process.Kill()
	}
}

func (process *senderProcess) waitDecoder() error {
	timer := time.NewTimer(processStopTimeout)
	defer timer.Stop()
	select {
	case <-process.decoderDone:
		if process.decoderErr == nil {
			return nil
		}
		detail := strings.TrimSpace(process.decoderOutput.buffer.String())
		if detail == "" {
			return helperFailure{kind: "transport", message: fmt.Sprintf("FFmpeg decoder exited: %v", process.decoderErr)}
		}
		return helperFailure{kind: "transport", message: fmt.Sprintf("FFmpeg decoder exited: %v: %s", process.decoderErr, detail)}
	case <-timer.C:
		if process.decoder.Process != nil {
			_ = process.decoder.Process.Kill()
		}
		return helperFailure{kind: "transport", message: "FFmpeg decoder did not exit after closing the PCM stream"}
	}
}

func (process *senderProcess) finishedDecoderError() error {
	select {
	case <-process.decoderDone:
		return process.waitDecoder()
	default:
		return nil
	}
}

func (process *senderProcess) monitor(reader io.Reader) {
	defer close(process.done)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxHelperLine)
	var monitorFailure *helperFailure
	for scanner.Scan() {
		var event helperEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Protocol != helperProtocolVersion {
			failure := helperProcessFailure("protocol", "AirPlay helper returned an invalid status message", process.helperOutput)
			monitorFailure = &failure
			process.kill()
			break
		}
		process.handle(event)
		if event.Event == "error" {
			process.kill()
			break
		}
	}
	if err := scanner.Err(); err != nil && monitorFailure == nil {
		failure := helperProcessFailure("protocol", fmt.Sprintf("read AirPlay helper status: %v", err), process.helperOutput)
		monitorFailure = &failure
		process.kill()
	}
	waitErr := process.helper.Wait()
	var decoderErr error
	if process.decoder.Process != nil {
		decoderErr = process.finishedDecoderError()
		if decoderErr == nil {
			_ = process.decoder.Process.Kill()
			_ = process.waitDecoder()
		}
	}
	_ = process.stdin.Close()
	if monitorFailure != nil {
		process.complete(terminalEvent{err: *monitorFailure})
	} else if decoderErr != nil && !process.stopping() {
		process.complete(terminalEvent{err: decoderErr})
	} else if waitErr != nil {
		process.complete(terminalEvent{err: helperProcessFailure("transport", fmt.Sprintf("AirPlay helper exited: %v", waitErr), process.helperOutput)})
	} else {
		process.complete(terminalEvent{err: helperProcessFailure("transport", "AirPlay helper exited without a terminal status", process.helperOutput)})
	}
}

func (process *senderProcess) handle(event helperEvent) {
	switch event.Event {
	case "playing":
		process.session.mu.Lock()
		if process.session.active == process {
			process.session.state = "PLAYING"
			process.session.positionMS = process.baseMS
		}
		process.session.mu.Unlock()
		process.playingOnce.Do(func() { close(process.playing) })
		process.session.manager.config.Notify("renderers")
	case "position":
		process.session.mu.Lock()
		if process.session.active == process {
			process.session.positionMS = clampPosition(process.baseMS+event.PositionMS, process.session.prepared.resource.DurationMS)
		}
		process.session.mu.Unlock()
	case "stopped":
		process.complete(terminalEvent{event: event.Event, pos: event.PositionMS})
	case "eof":
		if process.stopping() {
			process.complete(terminalEvent{event: "stopped", pos: event.PositionMS})
			return
		}
		err := process.waitDecoder()
		if process.stopping() {
			process.complete(terminalEvent{event: "stopped", pos: event.PositionMS})
		} else if err != nil {
			process.complete(terminalEvent{event: "error", err: err})
			process.kill()
		} else {
			process.complete(terminalEvent{event: "eof", pos: event.PositionMS})
		}
	case "error":
		if event.Kind == "auth" {
			go process.session.manager.invalidateAuth(process.session.device.device.ID, process.session.device.auth)
		}
		failure := error(helperFailure{kind: event.Kind, message: event.Message})
		if decoderErr := process.finishedDecoderError(); decoderErr != nil && !process.stopping() {
			failure = decoderErr
		}
		process.complete(terminalEvent{event: "error", err: failure})
	}
}

func (process *senderProcess) complete(event terminalEvent) {
	process.terminalOne.Do(func() {
		process.session.mu.Lock()
		if process.session.active == process {
			process.session.active = nil
			if event.event == "eof" {
				process.session.state = "STOPPED"
				process.session.positionMS = process.session.prepared.resource.DurationMS
			} else if event.err != nil {
				process.session.state = "STOPPED"
				process.session.lastError = "AirPlay sender failed"
			}
		}
		process.session.mu.Unlock()
		process.terminal <- event
		process.session.manager.config.Notify("renderers")
	})
}

type helperFailure struct {
	kind    string
	message string
}

func (failure helperFailure) Error() string {
	if failure.message == "" {
		return "AirPlay helper failed"
	}
	return failure.message
}

func (failure helperFailure) SafeRendererError() string {
	message := compactRendererError(failure.message)
	if message == "" {
		return ""
	}
	return "AirPlay sender detail: " + message
}

func helperProcessFailure(kind, message string, output *limitedBuffer) helperFailure {
	if output != nil && output.buffer != nil {
		if detail := strings.TrimSpace(output.buffer.String()); detail != "" {
			message += ": " + detail
		}
	}
	return helperFailure{kind: kind, message: compactRendererError(message)}
}

func compactRendererError(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	runes := []rune(message)
	if len(runes) <= maxRendererErrorRunes {
		return message
	}
	return strings.TrimSpace(string(runes[:maxRendererErrorRunes-3])) + "..."
}

func actionError(action string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return classifyContext(action, err)
	}
	var failure helperFailure
	if errors.As(err, &failure) {
		switch failure.kind {
		case "auth":
			return output.NewActionError(output.ErrorResponse, action, 401, err)
		case "timeout":
			return output.NewActionError(output.ErrorTimeout, action, 0, err)
		case "cancelled":
			return output.NewActionError(output.ErrorCancelled, action, 0, err)
		case "protocol":
			return output.NewActionError(output.ErrorFault, action, 0, err)
		case "unsupported":
			return output.NewActionError(output.ErrorUnsupported, action, 0, err)
		}
	}
	return output.NewActionError(output.ErrorTransport, action, 0, err)
}

func classifyContext(action string, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return output.NewActionError(output.ErrorTimeout, action, 0, err)
	}
	return output.NewActionError(output.ErrorCancelled, action, 0, err)
}

func clampPosition(value, duration int64) int64 {
	if value < 0 {
		return 0
	}
	if value > duration {
		return duration
	}
	return value
}

func formatFFmpegTime(milliseconds int64) string {
	return strconv.FormatInt(milliseconds/1000, 10) + "." + fmt.Sprintf("%03d", milliseconds%1000)
}

func cloneProperties(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func closeFiles(files ...*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
		}
	}
}
