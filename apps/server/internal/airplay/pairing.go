package airplay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/output"
)

const pairingLifetime = 2 * time.Minute

type helperPairRequest struct {
	Operation   string       `json:"op"`
	Protocol    int          `json:"protocol"`
	Device      helperDevice `json:"device"`
	ClientID    string       `json:"client_id"`
	Credentials string       `json:"credentials,omitempty"`
	Password    string       `json:"password,omitempty"`
}

type pairingSession struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	events  chan helperEvent
	done    chan struct{}
	closeMu sync.Once
}

func (manager *Manager) Pair(ctx context.Context, id string, request output.PairingRequest) (output.PairingStatus, error) {
	manager.pairMu.Lock()
	defer manager.pairMu.Unlock()
	record, err := manager.deviceRecord(id, "Pair")
	if err != nil {
		return output.PairingStatus{}, err
	}
	request.PIN = strings.TrimSpace(request.PIN)
	if len(request.Password) > 1024 {
		return output.PairingStatus{}, output.NewActionError(output.ErrorUnsupported, "Pair", 0, errors.New("AirPlay password is too long"))
	}
	if !record.device.PairingRequired {
		if record.device.PasswordRequired {
			if request.Password == "" {
				return output.PairingStatus{Required: true, Prompt: "Enter the AirPlay password."}, nil
			}
			record.auth.password = request.Password
			if err := manager.persistAuth(ctx, id, record.auth); err != nil {
				return output.PairingStatus{}, actionError("Pair", err)
			}
			return output.PairingStatus{}, nil
		}
		return output.PairingStatus{}, output.NewActionError(output.ErrorUnsupported, "Pair", 0, errors.New("receiver does not require authorization"))
	}
	if request.PIN == "" {
		manager.mu.Lock()
		previous := manager.pending[id]
		delete(manager.pending, id)
		manager.mu.Unlock()
		if previous != nil {
			previous.close()
		}
		session, err := manager.startPairing(record)
		if err != nil {
			return output.PairingStatus{}, output.NewActionError(output.ErrorTransport, "Pair", 0, err)
		}
		manager.mu.Lock()
		manager.pending[id] = session
		manager.mu.Unlock()
		event, err := waitPairEvent(ctx, session)
		if err != nil {
			manager.removePairing(id, session)
			return output.PairingStatus{}, actionError("Pair", err)
		}
		if event.Event != "pairing" {
			manager.removePairing(id, session)
			return output.PairingStatus{}, actionError("Pair", pairEventError(event))
		}
		prompt := strings.TrimSpace(event.Prompt)
		if prompt == "" {
			prompt = "Enter the PIN shown on the AirPlay receiver."
		}
		return output.PairingStatus{Required: true, Prompt: prompt}, nil
	}
	if len(request.PIN) != 4 || request.PIN[0] < '0' || request.PIN[0] > '9' || request.PIN[1] < '0' || request.PIN[1] > '9' || request.PIN[2] < '0' || request.PIN[2] > '9' || request.PIN[3] < '0' || request.PIN[3] > '9' {
		return output.PairingStatus{}, output.NewActionError(output.ErrorResponse, "Pair", 400, errors.New("PIN must contain exactly four digits"))
	}
	manager.mu.Lock()
	session := manager.pending[id]
	delete(manager.pending, id)
	manager.mu.Unlock()
	if session == nil {
		return output.PairingStatus{}, output.NewActionError(output.ErrorResponse, "Pair", 409, errors.New("pairing was not started or expired"))
	}
	defer session.close()
	if err := json.NewEncoder(session.stdin).Encode(map[string]string{"op": "pair_finish", "pin": request.PIN, "password": request.Password}); err != nil {
		return output.PairingStatus{}, output.NewActionError(output.ErrorTransport, "Pair", 0, err)
	}
	event, err := waitPairEvent(ctx, session)
	if err != nil {
		return output.PairingStatus{}, actionError("Pair", err)
	}
	if event.Event != "paired" || event.Credentials == "" || len(event.Credentials) > 8192 {
		return output.PairingStatus{}, actionError("Pair", pairEventError(event))
	}
	record.auth.credentials = event.Credentials
	if request.Password != "" {
		record.auth.password = request.Password
	}
	if err := manager.persistAuth(ctx, id, record.auth); err != nil {
		return output.PairingStatus{}, actionError("Pair", err)
	}
	if record.endpoint.passwordRequired && record.auth.password == "" {
		return output.PairingStatus{Required: true, Prompt: "Enter the AirPlay password."}, nil
	}
	return output.PairingStatus{}, nil
}

func (manager *Manager) startPairing(record deviceRecord) (*pairingSession, error) {
	command := exec.Command(manager.config.HelperPath)
	stdin, err := command.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("create helper pairing input: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		stdin.Close()
		return nil, fmt.Errorf("create helper pairing output: %w", err)
	}
	command.Stderr = &limitedBuffer{remaining: maxProcessError}
	session := &pairingSession{command: command, stdin: stdin, events: make(chan helperEvent, 4), done: make(chan struct{})}
	if err := command.Start(); err != nil {
		stdin.Close()
		return nil, fmt.Errorf("start pairing helper: %w", err)
	}
	request := helperPairRequest{
		Operation: "pair_begin", Protocol: helperProtocolVersion, Device: helperDevice{
			ID: record.device.ID, Name: record.device.Name, Address: record.endpoint.address.String(),
			LocalAddress: record.endpoint.localAddress.String(), Port: record.endpoint.port,
			Properties: cloneProperties(record.endpoint.properties), PairingRequired: record.endpoint.pairingRequired,
			PasswordRequired: record.endpoint.passwordRequired,
		}, ClientID: manager.identity, Credentials: record.auth.credentials, Password: record.auth.password,
	}
	if err := json.NewEncoder(stdin).Encode(request); err != nil {
		session.close()
		_ = command.Wait()
		return nil, fmt.Errorf("configure pairing helper: %w", err)
	}
	go session.monitor(stdout)
	go func() {
		timer := time.NewTimer(pairingLifetime)
		defer timer.Stop()
		select {
		case <-timer.C:
			manager.mu.Lock()
			if manager.pending[record.device.ID] == session {
				delete(manager.pending, record.device.ID)
			}
			manager.mu.Unlock()
			session.close()
		case <-session.done:
			manager.mu.Lock()
			if manager.pending[record.device.ID] == session {
				delete(manager.pending, record.device.ID)
			}
			manager.mu.Unlock()
		}
	}()
	return session, nil
}

func (session *pairingSession) monitor(reader io.Reader) {
	defer close(session.done)
	defer close(session.events)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxHelperLine)
	for scanner.Scan() {
		var event helperEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event.Protocol != helperProtocolVersion {
			session.events <- helperEvent{Event: "error", Kind: "protocol", Message: "invalid pairing helper status"}
			break
		}
		session.events <- event
		if event.Event == "paired" || event.Event == "error" {
			break
		}
	}
	_ = session.command.Wait()
}

func (session *pairingSession) close() {
	session.closeMu.Do(func() {
		_ = session.stdin.Close()
		if session.command.Process != nil {
			_ = session.command.Process.Kill()
		}
	})
}

func waitPairEvent(ctx context.Context, session *pairingSession) (helperEvent, error) {
	select {
	case event, ok := <-session.events:
		if !ok {
			return helperEvent{}, errors.New("pairing helper exited unexpectedly")
		}
		return event, nil
	case <-ctx.Done():
		return helperEvent{}, ctx.Err()
	}
}

func pairEventError(event helperEvent) error {
	if event.Event == "error" {
		return helperFailure{kind: event.Kind, message: event.Message}
	}
	return helperFailure{kind: "protocol", message: "pairing helper returned an unexpected status"}
}

func (manager *Manager) removePairing(id string, session *pairingSession) {
	manager.mu.Lock()
	if manager.pending[id] == session {
		delete(manager.pending, id)
	}
	manager.mu.Unlock()
	session.close()
}

func (manager *Manager) persistAuth(ctx context.Context, id string, auth storedAuth) error {
	if err := saveAuth(ctx, manager.db, id, auth); err != nil {
		return err
	}
	manager.mu.Lock()
	record, ok := manager.devices[id]
	if ok {
		record.auth = auth
		record.device.PairingRequired = record.endpoint.pairingRequired && auth.credentials == ""
		record.device.PasswordRequired = record.endpoint.passwordRequired && auth.password == ""
		manager.devices[id] = record
	}
	manager.mu.Unlock()
	manager.config.Notify("renderers")
	return nil
}
