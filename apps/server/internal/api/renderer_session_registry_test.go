package api

import "testing"

func TestRendererSessionSignal_suppresses_payload_after_revocation_is_observed(t *testing.T) {
	// Given: the session has atomically observed credential revocation.
	signal := newRendererSessionSignal(nil)
	_ = signal.Close()
	wrotePayload := false

	// When: a concurrent application path attempts its next write.
	active, err := signal.writeIfActive(func() error {
		wrotePayload = true
		return nil
	})

	// Then: no application write can cross the revocation cutpoint.
	if err != nil || active || wrotePayload {
		t.Fatalf("active/write/error = %t/%t/%v", active, wrotePayload, err)
	}
}

func TestRendererSessionSignal_revocation_overrides_queued_stale_epoch(t *testing.T) {
	// Given: stale-epoch shutdown is queued but has not been consumed by the session writer.
	signal := newRendererSessionSignal(nil)
	staleQueued := make(chan struct{})
	revocationObserved := make(chan struct{})
	go func() {
		signal.stop(rendererSessionCloseSignal{code: closePolicyViolation, reason: rendererStaleCloseReason})
		close(staleQueued)
	}()
	go func() {
		<-staleQueued
		_ = signal.Close()
		close(revocationObserved)
	}()

	// When: the revocation callback has synchronously observed the same Renderer DeviceID.
	<-revocationObserved
	var closing rendererSessionCloseSignal
	err := signal.terminate(rendererSessionCloseSignal{}, nil, func(selected rendererSessionCloseSignal) error {
		closing = selected
		return nil
	})

	// Then: revocation, not the earlier stale epoch, is the terminal outcome.
	if err != nil || closing.code != closePolicyViolation || closing.reason != rendererRevokedCloseReason {
		t.Fatalf("terminal close/error = %d %q/%v", closing.code, closing.reason, err)
	}
}
