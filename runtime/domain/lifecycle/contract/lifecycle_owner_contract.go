package contract

import (
	"context"
	"encoding/json"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

type CleanupPreview struct {
	Rows           int64     `json:"rows"`
	Bytes          int64     `json:"bytes"`
	OldestEligible time.Time `json:"oldest_eligible,omitempty"`
}

type OwnerLifecycleExecutor interface {
	Owner(context.Context) string
	Preview(context.Context, string, lifecyclemodel.PolicyVersion, time.Time) (CleanupPreview, error)
	ProcessBatch(context.Context, lifecyclemodel.CleanupJob, lifecyclemodel.PolicyVersion, []lifecyclemodel.LegalHold, int) (lifecyclemodel.CleanupBatchResult, error)
}

type SubjectIdentityResolver interface {
	ResolveSubject(context.Context, string, string, string) (string, error)
}

type SubjectDataHandler interface {
	Owner(context.Context) string
	PreviewSubject(context.Context, string, string) (json.RawMessage, error)
	ExportSubject(context.Context, string, string) (json.RawMessage, error)
	EraseSubject(context.Context, string, string, []lifecyclemodel.LegalHold) (json.RawMessage, error)
}

type ExternalErasureHandler interface {
	RequestExternalErasure(context.Context, lifecyclemodel.SubjectRequest) ([]lifecyclemodel.ExternalErasure, error)
}

type SubjectArtifactStore interface {
	PutSubjectExport(context.Context, string, string, json.RawMessage, time.Time) (string, error)
	ReadSubjectExport(context.Context, string, string, time.Time) (json.RawMessage, error)
	DeleteExpiredSubjectExports(context.Context, time.Time) (int, error)
	DeleteExpiredUploadStaging(context.Context, time.Time) (int, error)
}

type SubjectFileReference struct {
	WorkspaceID string `json:"workspace_id"`
	ObjectKey   string `json:"object_key"`
	RecordID    string `json:"record_id"`
	FieldKey    string `json:"field_key"`
	Reference   string `json:"reference"`
}

type SubjectFileEvidence struct {
	Reference   string `json:"reference"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
	Content     []byte `json:"content,omitempty"`
}

type SubjectFileStore interface {
	ExportSubjectFile(context.Context, SubjectFileReference) (SubjectFileEvidence, error)
	DeleteSubjectFile(context.Context, SubjectFileReference) (SubjectFileEvidence, error)
}

type UploadArtifact struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	ObjectKey   string    `json:"object_key"`
	FieldKey    string    `json:"field_key"`
	Filename    string    `json:"filename"`
	ContentType string    `json:"content_type"`
	SHA256      string    `json:"sha256"`
	Size        int64     `json:"size"`
	CreatedAt   time.Time `json:"created_at"`
}

const (
	FileScanPending     = "pending"
	FileScanClean       = "clean"
	FileScanQuarantined = "quarantined"
	FileScanFailed      = "failed"
)

// FileScanEvidence is Runtime-owned evidence for one immutable upload. Receipt
// is a Runtime signature over workspace, file identity, content identity and
// the scanner outcome; project code cannot mint or mutate it.
type FileScanEvidence struct {
	FileID      string    `json:"file_id"`
	WorkspaceID string    `json:"workspace_id,omitempty"`
	Filename    string    `json:"filename,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	ObjectKey   string    `json:"object_key,omitempty"`
	FieldKey    string    `json:"field_key,omitempty"`
	SHA256      string    `json:"content_sha256"`
	Size        int64     `json:"size"`
	Status      string    `json:"status"`
	Provider    string    `json:"provider,omitempty"`
	EvidenceRef string    `json:"evidence_ref,omitempty"`
	ScannedAt   time.Time `json:"scanned_at,omitempty"`
	Receipt     string    `json:"scan_receipt,omitempty"`
}

type UploadCleanupResult struct {
	Scanned          int `json:"scanned"`
	Referenced       int `json:"referenced"`
	Orphaned         int `json:"orphaned"`
	Deleted          int `json:"deleted"`
	ExpiredDownloads int `json:"expired_downloads"`
}

type UploadArtifactStore interface {
	RegisterUpload(context.Context, UploadArtifact) error
	ReconcileUploadArtifacts(context.Context, time.Time, int) (UploadCleanupResult, error)
}

type FileScanStore interface {
	FindFileScan(context.Context, string, string) (FileScanEvidence, error)
	RecordFileScan(context.Context, FileScanEvidence) error
}
