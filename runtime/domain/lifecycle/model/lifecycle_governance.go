package lifecyclemodel

import (
	"encoding/json"
	"time"
)

type PolicyStatus string

const (
	PolicyStatusDraft     PolicyStatus = "draft"
	PolicyStatusPublished PolicyStatus = "published"
	PolicyStatusRetired   PolicyStatus = "retired"
)

type PolicyVersion struct {
	WorkspaceID    string          `json:"workspace_id"`
	Policy         RetentionPolicy `json:"policy"`
	Status         PolicyStatus    `json:"status"`
	Revision       int64           `json:"revision"`
	PublishedBy    string          `json:"published_by"`
	PublishedAt    time.Time       `json:"published_at"`
	ApprovalRef    string          `json:"approval_ref,omitempty"`
	EstimatedRows  int64           `json:"estimated_rows"`
	EstimatedBytes int64           `json:"estimated_bytes"`
	ChangePlanRef  string          `json:"change_plan_ref,omitempty"`
}

type CleanupStatus string

const (
	CleanupStatusPending   CleanupStatus = "pending"
	CleanupStatusRunning   CleanupStatus = "running"
	CleanupStatusPaused    CleanupStatus = "paused"
	CleanupStatusSucceeded CleanupStatus = "succeeded"
	CleanupStatusFailed    CleanupStatus = "failed"
)

type CleanupJob struct {
	ID             string        `json:"id"`
	WorkspaceID    string        `json:"workspace_id"`
	PolicyKey      string        `json:"policy_key"`
	PolicyVersion  string        `json:"policy_version"`
	Operation      Operation     `json:"operation"`
	Status         CleanupStatus `json:"status"`
	DryRun         bool          `json:"dry_run"`
	Checkpoint     string        `json:"checkpoint,omitempty"`
	LeaseOwner     string        `json:"lease_owner,omitempty"`
	LeaseExpiresAt time.Time     `json:"lease_expires_at,omitempty"`
	FencingToken   int64         `json:"fencing_token"`
	EstimatedRows  int64         `json:"estimated_rows"`
	EstimatedBytes int64         `json:"estimated_bytes"`
	Scanned        int64         `json:"scanned"`
	Archived       int64         `json:"archived"`
	Purged         int64         `json:"purged"`
	Skipped        int64         `json:"skipped"`
	Failed         int64         `json:"failed"`
	OldestEligible time.Time     `json:"oldest_eligible,omitempty"`
	LastError      string        `json:"last_error,omitempty"`
	RequestedBy    string        `json:"requested_by"`
	Reason         string        `json:"reason"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type CleanupBatchResult struct {
	Checkpoint     string    `json:"checkpoint,omitempty"`
	Scanned        int64     `json:"scanned"`
	Archived       int64     `json:"archived"`
	Purged         int64     `json:"purged"`
	Skipped        int64     `json:"skipped"`
	Failed         int64     `json:"failed"`
	OldestEligible time.Time `json:"oldest_eligible,omitempty"`
	Done           bool      `json:"done"`
}

type SubjectRequestKind string

const (
	SubjectRequestExport SubjectRequestKind = "export"
	SubjectRequestErase  SubjectRequestKind = "erase"
)

type SubjectRequestStatus string

const (
	SubjectRequestPendingVerification SubjectRequestStatus = "pending_verification"
	SubjectRequestVerified            SubjectRequestStatus = "verified"
	SubjectRequestPreviewed           SubjectRequestStatus = "previewed"
	SubjectRequestApproved            SubjectRequestStatus = "approved"
	SubjectRequestExecuting           SubjectRequestStatus = "executing"
	SubjectRequestSucceeded           SubjectRequestStatus = "succeeded"
	SubjectRequestFailed              SubjectRequestStatus = "failed"
)

type SubjectRequest struct {
	ID                string               `json:"id"`
	WorkspaceID       string               `json:"workspace_id"`
	Kind              SubjectRequestKind   `json:"kind"`
	Status            SubjectRequestStatus `json:"status"`
	SubjectType       string               `json:"subject_type"`
	SubjectID         string               `json:"subject_id"`
	ResolvedIdentity  string               `json:"resolved_identity,omitempty"`
	RequestedBy       string               `json:"requested_by"`
	VerifiedBy        string               `json:"verified_by,omitempty"`
	ApprovedBy        string               `json:"approved_by,omitempty"`
	SecondFactorRef   string               `json:"second_factor_ref,omitempty"`
	Reason            string               `json:"reason"`
	ImpactPreview     json.RawMessage      `json:"impact_preview,omitempty"`
	ResultReference   string               `json:"result_reference,omitempty"`
	DownloadExpiresAt time.Time            `json:"download_expires_at,omitempty"`
	BackupPending     bool                 `json:"backup_pending"`
	LastError         string               `json:"last_error,omitempty"`
	ExecutionAttempt  int64                `json:"execution_attempt"`
	ExecutionLeaseEnd time.Time            `json:"execution_lease_end,omitempty"`
	CreatedAt         time.Time            `json:"created_at"`
	UpdatedAt         time.Time            `json:"updated_at"`
}

type ExternalErasure struct {
	ID           string    `json:"id"`
	RequestID    string    `json:"request_id"`
	WorkspaceID  string    `json:"workspace_id"`
	ConnectorKey string    `json:"connector_key"`
	ProviderRef  string    `json:"provider_ref"`
	Status       string    `json:"status"`
	ReconciledAt time.Time `json:"reconciled_at,omitempty"`
	Evidence     string    `json:"evidence,omitempty"`
}

// DeletionRegistration survives online erasure so restored backups can reapply
// the same subject deletion before the restored workspace is served.
type DeletionRegistration struct {
	RequestID        string    `json:"request_id"`
	WorkspaceID      string    `json:"workspace_id"`
	ResolvedIdentity string    `json:"resolved_identity"`
	BackupPending    bool      `json:"backup_pending"`
	Evidence         string    `json:"evidence"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ArchiveEntry struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	Owner         string          `json:"owner"`
	SourceTable   string          `json:"source_table"`
	ResourceID    string          `json:"resource_id"`
	PolicyKey     string          `json:"policy_key"`
	PolicyVersion string          `json:"policy_version"`
	JobID         string          `json:"job_id"`
	PayloadHash   string          `json:"payload_hash"`
	Payload       json.RawMessage `json:"-"`
	ArchivedAt    time.Time       `json:"archived_at"`
}

type AuditEvidence struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	Event       string          `json:"event"`
	ActorID     string          `json:"actor_id"`
	ResourceID  string          `json:"resource_id"`
	PolicyKey   string          `json:"policy_key,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
}

type Metrics struct {
	EligibleBacklog int64     `json:"eligible_backlog"`
	OldestEligible  time.Time `json:"oldest_eligible,omitempty"`
	PurgedTotal     int64     `json:"purged_total"`
	FailureTotal    int64     `json:"failure_total"`
	LegalHoldCount  int64     `json:"legal_hold_count"`
	Warning         bool      `json:"warning"`
}
