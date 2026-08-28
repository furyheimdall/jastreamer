package api_test

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jastreamer/jastreamer-server/internal/playback"
	"github.com/jastreamer/jastreamer-server/internal/security"
)

func Test_RendererSession_revocation_wins_concurrent_stale_terminal_paths(t *testing.T) {
	states := []struct {
		name   string
		tracks int
	}{
		{name: "receipt", tracks: 1},
		{name: "result", tracks: 1},
		{name: "heartbeat", tracks: 0},
	}
	for _, state := range states {
		t.Run(state.name, func(t *testing.T) {
			// Given: two established Renderer sessions and a barrier after the target revocation is observed.
			scenario := newRendererSessionScenario(t, state.tracks)
			target, welcome := scenario.openSocket(t, 0, nil)
			var command rendererFrame
			if state.tracks > 0 {
				command = target.readApplicationFrame(t)
			}
			if state.name == "result" {
				if err := scenario.fixture.store.RecordRendererCommandAcknowledgement(t.Context(), playback.RendererCommandAcknowledgement{
					RendererID: playback.RendererID(scenario.renderer.Device.ID), Epoch: playback.SessionEpoch(welcome.SessionEpoch),
					CommandID: command.CommandID, Sequence: playback.CommandSequence(command.Sequence),
					Status: playback.CommandAckReceived, RecordedAt: time.Now().UTC(),
				}); err != nil {
					t.Fatalf("establish result state: %v", err)
				}
			}
			peerCredential := pairRole(t, scenario.fixture, security.RoleRenderer, "Unaffected renderer")
			peerScenario := rendererSessionScenario{
				fixture: scenario.fixture, server: scenario.server, renderer: peerCredential,
			}
			peer, _ := peerScenario.openSocket(t, 0, nil)

			observed := make(chan struct{})
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			stopObserving := scenario.fixture.manager.ObserveRevocations(func(id security.DeviceID) {
				if id != scenario.renderer.Device.ID {
					return
				}
				close(observed)
				<-release
			})
			defer stopObserving()
			revokeDone := make(chan error, 1)

			// When: revocation reaches the session while stale-epoch traffic is injected at the observer barrier.
			go func() {
				revokeDone <- scenario.fixture.manager.Revoke(t.Context(), scenario.fixture.admin.Token, scenario.renderer.Device.ID)
			}()
			observerDeadline := time.NewTimer(2 * time.Second)
			defer observerDeadline.Stop()
			select {
			case <-observed:
			case <-observerDeadline.C:
				t.Fatal("renderer revocation observer was not reached")
			}
			if _, err := scenario.fixture.store.OpenRendererSession(t.Context(), playback.RendererSessionRequest{
				RendererID: playback.RendererID(scenario.renderer.Device.ID), ConnectedAt: time.Now().UTC(),
			}); err != nil {
				t.Fatalf("make established session epoch stale: %v", err)
			}
			opcode := byte(0x9)
			payload := []byte("revocation-race")
			switch state.name {
			case "receipt":
				opcode = 0x1
				encoded, encodeErr := json.Marshal(struct {
					ProtocolMajor int             `json:"protocolMajor"`
					Type          string          `json:"type"`
					CommandID     string          `json:"commandId"`
					Sequence      int64           `json:"sequence"`
					Status        string          `json:"status"`
					Error         json.RawMessage `json:"error"`
				}{
					ProtocolMajor: 3, Type: "command.ack", CommandID: command.CommandID,
					Sequence: command.Sequence, Status: "received", Error: json.RawMessage("null"),
				})
				if encodeErr != nil {
					t.Fatalf("encode stale receipt: %v", encodeErr)
				}
				payload = encoded
			case "result":
				opcode = 0x1
				position := int64(0)
				observedState := "playing"
				encoded, encodeErr := json.Marshal(rendererPendingResult{
					ProtocolMajor: 3, Type: "command.result", CommandID: command.CommandID,
					ResultID: "result-during-revocation", Status: "succeeded",
					ObservedState: &observedState, PositionMS: &position,
				})
				if encodeErr != nil {
					t.Fatalf("encode stale result: %v", encodeErr)
				}
				payload = encoded
			case "heartbeat":
			default:
				t.Fatalf("unknown revocation race state %q", state.name)
			}
			_ = target.trySendFrame(opcode, payload)
			closeOpcode, closePayload := target.readFrame(t)

			// Then: revocation is the first terminal frame, emits nothing later, and does not affect the peer.
			if closeOpcode != 0x8 || len(closePayload) < 2 || binary.BigEndian.Uint16(closePayload[:2]) != 1008 ||
				string(closePayload[2:]) != "device revoked" {
				t.Fatalf("first terminal frame = %#x %x", closeOpcode, closePayload)
			}
			if err := target.connection.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatalf("set post-revocation deadline: %v", err)
			}
			var trailing [1]byte
			n, readErr := target.reader.Read(trailing[:])
			var networkErr net.Error
			if n != 0 || readErr == nil || (errors.As(readErr, &networkErr) && networkErr.Timeout()) {
				t.Fatalf("post-revocation bytes/read = %x/%v", trailing[:n], readErr)
			}
			peer.sendFrame(t, 0x9, []byte("peer-active"))
			peerOpcode, peerPayload := peer.readFrame(t)
			if peerOpcode != 0xa || string(peerPayload) != "peer-active" {
				t.Fatalf("unrelated renderer heartbeat = %#x %q", peerOpcode, peerPayload)
			}

			releaseOnce.Do(func() { close(release) })
			if err := <-revokeDone; err != nil {
				t.Fatalf("revoke renderer credential: %v", err)
			}
			reconnectRequest, err := http.NewRequest(http.MethodGet, scenario.server.URL+"/api/v1/renderers/"+
				string(scenario.renderer.Device.ID)+"/session", nil)
			if err != nil {
				t.Fatalf("create revoked reconnect: %v", err)
			}
			reconnectRequest.Header.Set("Authorization", "Bearer "+scenario.renderer.Token)
			reconnect, err := scenario.server.Client().Do(reconnectRequest)
			if err != nil {
				t.Fatalf("request revoked reconnect: %v", err)
			}
			reconnectBody, bodyErr := io.ReadAll(reconnect.Body)
			closeErr := reconnect.Body.Close()
			if bodyErr != nil || closeErr != nil || reconnect.StatusCode != http.StatusUnauthorized ||
				!strings.Contains(string(reconnectBody), `"code":"TOKEN_REVOKED"`) {
				t.Fatalf("revoked reconnect = %d %q read=%v close=%v", reconnect.StatusCode, reconnectBody, bodyErr, closeErr)
			}
		})
	}
}
