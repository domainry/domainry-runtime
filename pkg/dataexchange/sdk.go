package dataexchange

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

const ProtocolVersionV1 = "data-exchange.v1"

type DeploymentMode string

const (
	DeploymentModeModule DeploymentMode = "module"
	DeploymentModeSaaS   DeploymentMode = "saas"
)

// ApplicationRef identifies one Runtime installation without exposing Runtime
// configuration or domain services to the Data Exchange owner.
type ApplicationRef struct {
	ApplicationID string
	RuntimeID     string
}

func (r ApplicationRef) Validate() error {
	if strings.TrimSpace(r.ApplicationID) == "" || strings.TrimSpace(r.RuntimeID) == "" {
		return fmt.Errorf("data exchange application identity is incomplete")
	}
	return nil
}

type Descriptor struct {
	ProtocolVersion string
	Mode            DeploymentMode
	Capabilities    []string
}

type Scope struct {
	WorkspaceID string
	ActorID     string
	RoleKey     string
	RequestID   string
}

func (s Scope) Validate() error {
	if strings.TrimSpace(s.WorkspaceID) == "" || strings.TrimSpace(s.ActorID) == "" {
		return fmt.Errorf("data exchange workspace and actor are required")
	}
	return nil
}

type Job struct {
	ID         string
	Provider   string
	Operation  string
	Status     string
	Checkpoint int
	Total      int
	ArtifactID string
	ErrorCode  string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type ImportRequest struct {
	Scope          Scope
	Provider       string
	ObjectKey      string
	IdempotencyKey string
	Filename       string
	ContentType    string
	Source         io.Reader
	MaxBytes       int64
}

type ExportRequest struct {
	Scope          Scope
	Provider       string
	ObjectKey      string
	IdempotencyKey string
	Options        []byte
}

type JobRequest struct {
	Scope Scope
	JobID string
}

type Artifact struct {
	ID          string
	Filename    string
	ContentType string
	Size        int64
	SHA256      string
	Content     io.ReadCloser
}

// Factory is selected by generated composition. Module and SaaS factories
// expose the same Binding; Runtime never selects topology from environment.
type Factory interface {
	Open(context.Context, ApplicationRef, Host) (Binding, error)
}

// Binding owns file ingestion, jobs, chunks, workers, artifacts and download
// lifecycle. It never owns business authorization or record mutations.
type Binding interface {
	Descriptor() Descriptor
	SubmitImport(context.Context, ImportRequest) (Job, bool, error)
	SubmitExport(context.Context, ExportRequest) (Job, bool, error)
	Job(context.Context, JobRequest) (Job, error)
	Cancel(context.Context, JobRequest) (Job, error)
	Download(context.Context, JobRequest) (Artifact, error)
	Start(context.Context, WorkerConfig) <-chan struct{}
	Close(context.Context) error
}

type WorkerConfig struct {
	Enabled      bool
	PollInterval time.Duration
	BatchSize    int
	LeaseTTL     time.Duration
}

// Host is the only application-facing dependency of a Data Exchange binding.
// Providers are owner adapters implemented by Record, Report, Audit, etc.
type Host interface {
	ImportProvider(string) (ImportProvider, bool)
	ExportProvider(string) (ExportProvider, bool)
}

type ImportBatch struct {
	Scope     Scope
	ObjectKey string
	Headers   []string
	Rows      []ImportRow
	JobID     string
	ChunkID   string
}

type ImportRow struct {
	Number int
	Values []string
}

type ImportBatchResult struct {
	Accepted int
	Rejected int
	Receipt  string
}

// ImportProvider retains schema mapping, validation, authorization,
// idempotency and mutation ownership.
type ImportProvider interface {
	ValidateImportBatch(context.Context, ImportBatch) (ImportBatchResult, error)
	ApplyImportBatch(context.Context, ImportBatch) (ImportBatchResult, error)
}

type ExportPageRequest struct {
	Scope     Scope
	ObjectKey string
	Options   []byte
	Cursor    string
	PageSize  int
	JobID     string
}

type ExportPage struct {
	Columns    []string
	Rows       [][]string
	NextCursor string
	Total      int
}

// ExportProvider returns only owner-authorized and owner-projected values.
// Data Exchange may encode and persist them but cannot reinterpret policy.
type ExportProvider interface {
	ReadExportPage(context.Context, ExportPageRequest) (ExportPage, error)
}
