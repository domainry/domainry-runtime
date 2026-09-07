package model

import (
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
)

const ReportExportPrepareUseCase = "report.export.prepare"

// ReportExportPrepareReceipt is Runtime-owned operational state. It binds one
// authenticated caller operation to one Data Exchange job without moving
// Report definitions, audit records, or download records into Runtime.
type ReportExportPrepareReceipt struct {
	ID                    string
	OperationID           string
	WorkspaceID           string
	RequesterUserID       string
	UseCase               string
	ReportKey             string
	ObjectKey             string
	AuditID               string
	CallerKey             string
	RequestFingerprint    string
	Status                string
	PayloadJSON           string
	BusinessJobKey        string
	JobID                 string
	CompletionArtifactID  string
	CompletionFingerprint string
	TerminalErrorCode     string
	LeaseOwner            string
	LeaseExpiresAt        string
	FencingToken          int64
	CreatedAt             string
	UpdatedAt             string
	ExpiresAt             string
}

type ReportExportPrepareClaimRequest struct {
	Receipt            ReportExportPrepareReceipt
	RequestFingerprint string
	LeaseOwner         string
	LeaseTTL           time.Duration
	Now                time.Time
}

type ReportExportPrepareClaimResult struct {
	Decision idempotency.Decision
	Receipt  ReportExportPrepareReceipt
	// AuditOperationConflict distinguishes a different caller key attempting
	// to claim an audit operation that already has an owner.
	AuditOperationConflict bool
}

type ReportExportPreparePayload struct {
	WorkspaceID    string
	ReceiptID      string
	PayloadJSON    string
	BusinessJobKey string
	LeaseOwner     string
	FencingToken   int64
	Now            time.Time
}

type ReportExportPrepareCompletion struct {
	WorkspaceID  string
	ReceiptID    string
	JobID        string
	LeaseOwner   string
	FencingToken int64
	Now          time.Time
	ExpiresAt    time.Time
}

type ReportExportPrepareFailure struct {
	WorkspaceID  string
	ReceiptID    string
	LeaseOwner   string
	FencingToken int64
	ErrorCode    string
	Now          time.Time
	ExpiresAt    time.Time
}

type ReportExportCompletionBinding struct {
	ReceiptID             string
	WorkspaceID           string
	RequesterUserID       string
	ReportKey             string
	ObjectKey             string
	AuditID               string
	JobID                 string
	ArtifactID            string
	PayloadJSON           string
	CompletionFingerprint string
	Now                   time.Time
	ExpiresAt             time.Time
}

type ReportExportCompletionBindingDecision string

const (
	ReportExportCompletionBound    ReportExportCompletionBindingDecision = "bound"
	ReportExportCompletionReplay   ReportExportCompletionBindingDecision = "replay"
	ReportExportCompletionConflict ReportExportCompletionBindingDecision = "conflict"
)

type ReportExportCompletionBindingResult struct {
	Decision ReportExportCompletionBindingDecision
	Receipt  ReportExportPrepareReceipt
}
