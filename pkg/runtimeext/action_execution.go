package runtimeext

import (
	"context"
	"encoding/json"
	"io"
	"time"
)

// ExecutionPhase is controlled by Runtime. Project code can observe the phase
// but cannot begin, commit or roll back a transaction.
type ExecutionPhase string

const (
	ExecutionPhasePrewrite   ExecutionPhase = "prewrite"
	ExecutionPhaseWriting    ExecutionPhase = "writing"
	ExecutionPhaseCommitted  ExecutionPhase = "committed"
	ExecutionPhaseRolledBack ExecutionPhase = "rolled_back"
)

const (
	ConnectorCallAfterWriteErrorCode          = "backend.connector.call_after_write_forbidden"
	ActionWriteDuringConnectorCallErrorCode   = "backend.action.write_during_connector_call_forbidden"
	ConnectorActionExecutionRequiredErrorCode = "backend.connector.action_execution_required"
	ConnectorActionGrantDeniedErrorCode       = "backend.connector.action_grant_denied"
	ConnectorActionSideEffectOutboxErrorCode  = "backend.connector.action_side_effect_requires_outbox"
	FileActionGrantDeniedErrorCode            = "backend.upload.action_grant_denied"
	RecordNotificationRecipientOperation      = "notification_recipient"
)

// SynchronousConnectorCallLease keeps the Action UoW in prewrite for the
// complete synchronous network call. Runtime Gateways must always release it.
type SynchronousConnectorCallLease interface {
	Release()
}

func (p ExecutionPhase) Valid() bool {
	switch p {
	case ExecutionPhasePrewrite, ExecutionPhaseWriting, ExecutionPhaseCommitted, ExecutionPhaseRolledBack:
		return true
	default:
		return false
	}
}

// ActionExecution is the only Runtime capability source visible to generated
// Action bindings. Generated <ActionName>Capabilities expose a narrower typed
// facade to user business code.
type ActionExecution interface {
	Identity() ExecutionIdentity
	Principal() Principal
	Workspace() Workspace
	Phase() ExecutionPhase
	QueryRecords(context.Context, RecordQuery) (RecordQueryResult, error)
	ApplyRecordMutation(context.Context, RecordMutation) (RecordMutationResult, error)
	StageDurableIntent(context.Context, DurableIntent) (DurableIntentReceipt, error)
	AcquireSynchronousConnectorCall(ActionConnectorCapability) (SynchronousConnectorCallLease, error)
}

// ConditionalUpdateManyExecution is optional so older handwritten Runtimeext
// test doubles do not accidentally acquire set-mutation authority. Generated
// Action bindings expose it only for an authored conditional_update_many grant.
type ConditionalUpdateManyExecution interface {
	ConditionalUpdateMany(context.Context, ConditionalUpdateManyRequest) (ConditionalUpdateManyResult, error)
}

func ApplyConditionalUpdateMany(ctx context.Context, execution ActionExecution, request ConditionalUpdateManyRequest) (ConditionalUpdateManyResult, error) {
	capability, ok := execution.(ConditionalUpdateManyExecution)
	if !ok {
		return ConditionalUpdateManyResult{}, &BusinessError{Code: "backend.action.conditional_update_many_unavailable", Message: "Runtime conditional update-many execution is unavailable"}
	}
	return capability.ConditionalUpdateMany(ctx, request)
}

type FileVerificationExecution interface {
	VerifyFileClean(context.Context, FileVerificationRequest) (FileVerificationEvidence, error)
}

type VerifiedFileExecution interface {
	OpenVerifiedFile(context.Context, VerifiedFileRequest) (VerifiedFile, error)
}

type FileDownloadExecution interface {
	IssueFileDownload(context.Context, FileDownloadRequest) (FileDownloadTicket, error)
}

type DerivedFileExecution interface {
	CreateDerivedFile(context.Context, DerivedFileRequest) (DerivedFileEvidence, error)
}

type BusinessJobExecution interface {
	RunBusinessJob(context.Context, BusinessJobRequest) (BusinessJobReceipt, error)
}

// RecordNotificationRecipientRequest identifies one record whose Runtime-owned
// owner user is needed as a notification recipient. The projection never
// exposes organization ownership or other system metadata to project code.
type RecordNotificationRecipientRequest struct {
	ObjectKey string
	RecordID  string
}

type RecordNotificationRecipientExecution interface {
	ResolveRecordNotificationRecipient(context.Context, RecordNotificationRecipientRequest) (string, error)
}

func ResolveRecordNotificationRecipient(ctx context.Context, execution ActionExecution, request RecordNotificationRecipientRequest) (string, error) {
	resolver, ok := execution.(RecordNotificationRecipientExecution)
	if !ok {
		return "", &BusinessError{Code: "backend.notification.record_recipient_unavailable", Message: "Runtime record notification recipient resolution is unavailable"}
	}
	return resolver.ResolveRecordNotificationRecipient(ctx, request)
}

func VerifyFileClean(ctx context.Context, execution ActionExecution, request FileVerificationRequest) (FileVerificationEvidence, error) {
	verifier, ok := execution.(FileVerificationExecution)
	if !ok {
		return FileVerificationEvidence{}, &BusinessError{Code: "backend.upload.scan_service_unavailable", Message: "Runtime file verification is unavailable"}
	}
	return verifier.VerifyFileClean(ctx, request)
}

func OpenVerifiedFile(ctx context.Context, execution ActionExecution, request VerifiedFileRequest) (VerifiedFile, error) {
	opener, ok := execution.(VerifiedFileExecution)
	if !ok {
		return VerifiedFile{}, &BusinessError{Code: "backend.upload.file_open_unavailable", Message: "Runtime verified file access is unavailable"}
	}
	return opener.OpenVerifiedFile(ctx, request)
}

func IssueFileDownload(ctx context.Context, execution ActionExecution, request FileDownloadRequest) (FileDownloadTicket, error) {
	issuer, ok := execution.(FileDownloadExecution)
	if !ok {
		return FileDownloadTicket{}, &BusinessError{Code: "backend.upload.download_ticket_unavailable", Message: "Runtime file download ticket issuance is unavailable"}
	}
	return issuer.IssueFileDownload(ctx, request)
}

func CreateDerivedFile(ctx context.Context, execution ActionExecution, request DerivedFileRequest) (DerivedFileEvidence, error) {
	creator, ok := execution.(DerivedFileExecution)
	if !ok {
		return DerivedFileEvidence{}, &BusinessError{Code: "backend.upload.derived_file_unavailable", Message: "Runtime derived file storage is unavailable"}
	}
	return creator.CreateDerivedFile(ctx, request)
}

func RunBusinessJob(ctx context.Context, execution ActionExecution, request BusinessJobRequest) (BusinessJobReceipt, error) {
	runner, ok := execution.(BusinessJobExecution)
	if !ok {
		return BusinessJobReceipt{}, &BusinessError{Code: "backend.business_job.unavailable", Message: "Runtime business job execution is unavailable"}
	}
	return runner.RunBusinessJob(ctx, request)
}

type FileVerificationRequest struct {
	FileID        string
	ContentSHA256 string
	ScanReceipt   string
}

type FileVerificationEvidence struct {
	FileID, ContentSHA256, Filename, ContentType, Status, Provider, EvidenceRef, ScanReceipt string
	Size                                                                                     int64
}

// FileRecordBinding binds an opaque Runtime file to one caller-readable
// business record field. Runtime reads that exact record under the Action's
// generated grants before it opens any bytes.
type FileRecordBinding struct {
	ObjectKey   string
	RecordID    string
	FileIDField string
}

type VerifiedFileRequest struct {
	FileVerificationRequest
	Binding FileRecordBinding
}

type VerifiedFile struct {
	FileVerificationEvidence
	Filename    string
	ContentType string
	Content     io.ReadCloser
}

type FileDownloadRequest struct {
	FileVerificationRequest
	Binding FileRecordBinding
}

type FileDownloadTicket struct {
	ProtectedDownload string
	ExpiresAt         time.Time
}

type DerivedFileRequest struct {
	IdempotencyKey string
	ObjectKey      string
	FieldKey       string
	Filename       string
	ContentType    string
	Content        io.Reader
}

type DerivedFileEvidence struct {
	FileVerificationEvidence
	Filename          string
	ContentType       string
	ProtectedDownload string
}

type BusinessJobRequest struct {
	JobKey               string
	ObjectKey            string
	RecordID             string
	ActionKey            string
	Payload              json.RawMessage
	MaxAttempts          int
	RetryDelaySeconds    int
	RetryMaxDelaySeconds int
}

type BusinessJobReceipt struct {
	JobID string
}
