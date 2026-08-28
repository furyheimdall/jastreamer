package transcode

import "sync"

// Approval owns the executable identity used to produce Diagnostic until a
// Provider consumes it. Call Close when the approval is not consumed.
type Approval struct {
	Diagnostic

	mu         sync.Mutex
	executable *executableBinding
}

func newApproval(diagnostic Diagnostic, executable *executableBinding) *Approval {
	return &Approval{Diagnostic: diagnostic, executable: executable}
}

func (approval *Approval) Close() error {
	approval.mu.Lock()
	defer approval.mu.Unlock()
	if approval.executable == nil {
		return nil
	}
	err := approval.executable.Close()
	approval.executable = nil
	return err
}

func (approval *Approval) consume() (*executableBinding, error) {
	approval.mu.Lock()
	defer approval.mu.Unlock()
	if approval.executable == nil || approval.Diagnostic.Status != StatusAvailable {
		return nil, ErrUnavailable
	}
	if approval.executable.fingerprint != approval.Diagnostic.SHA256 {
		return nil, ErrExecutableChanged
	}
	executable := approval.executable
	approval.executable = nil
	return executable, nil
}
