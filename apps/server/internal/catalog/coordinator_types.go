package catalog

import (
	"context"
	"errors"
	"time"
)

var (
	ErrScanInProgress          = errors.New("catalog: scan already in progress")
	ErrScanNotFound            = errors.New("catalog: scan job not found")
	ErrScanFinished            = errors.New("catalog: scan job already finished")
	ErrCoordinatorClosed       = errors.New("catalog: coordinator closed")
	ErrInvalidCoordinatorState = errors.New("catalog: invalid coordinator state")
)

type ScanStatus string

const (
	ScanQueued    ScanStatus = "queued"
	ScanRunning   ScanStatus = "running"
	ScanComplete  ScanStatus = "complete"
	ScanFailed    ScanStatus = "failed"
	ScanCancelled ScanStatus = "cancelled"
)

type ScanJobID string

type ScanJob struct {
	ID              ScanJobID  `json:"job_id"`
	RootID          RootID     `json:"root_id"`
	Status          ScanStatus `json:"status"`
	RequestedAt     time.Time  `json:"requested_at"`
	StartedAt       time.Time  `json:"started_at,omitzero"`
	FinishedAt      time.Time  `json:"finished_at,omitzero"`
	CatalogRevision uint64     `json:"catalog_revision"`
	ErrorCode       string     `json:"error_code,omitempty"`
	ErrorDetail     string     `json:"-"`
}

type ScanFunc func(context.Context, Root, Snapshot) (ScanResult, error)

type DesiredRoot struct {
	ID          RootID
	DisplayName string
	Path        string
}

type CoordinatorConfig struct {
	StatePath       string
	AllowedBases    []string
	Now             func() time.Time
	Scan            ScanFunc
	InitialSnapshot Snapshot
}

type persistedRoot struct {
	ID            RootID `json:"root_id"`
	DisplayName   string `json:"display_name"`
	CanonicalPath string `json:"canonical_path"`
}

type coordinatorState struct {
	Roots    []persistedRoot `json:"roots"`
	Jobs     []ScanJob       `json:"jobs"`
	Snapshot Snapshot        `json:"snapshot"`
	NextJob  uint64          `json:"next_job"`
}
