package api

import (
	"encoding/json"
	"errors"
	"time"
)

func (session *rendererSocketSession) writeActive(write func() error) error {
	active, err := session.signal.writeIfActive(write)
	if err != nil {
		return err
	}
	if active {
		return nil
	}
	return errors.Join(errRendererSessionTerminal, session.terminate(rendererSessionCloseSignal{}, nil))
}

func (session *rendererSocketSession) writeJSON(value rendererOutbound) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return session.writeActive(func() error { return session.writeJSONPayload(payload) })
}

func (session *rendererSocketSession) writeJSONPayload(payload []byte) error {
	if err := writeFrame(session.writer, opcodeText, payload); err != nil {
		return err
	}
	return session.writer.Flush()
}

func (session *rendererSocketSession) handleControl(control rendererControl) error {
	return session.writeActive(func() error {
		if control.opcode == opcodePong {
			return session.connection.SetReadDeadline(time.Now().Add(websocketPongWait))
		}
		if err := writeFrame(session.writer, opcodePong, control.payload); err != nil {
			return err
		}
		return session.writer.Flush()
	})
}

func (session *rendererSocketSession) terminate(
	closing rendererSessionCloseSignal,
	beforeClose func() error,
) error {
	return session.signal.terminate(closing, beforeClose, func(selected rendererSessionCloseSignal) error {
		return writeClose(session.writer, selected.code, selected.reason)
	})
}
