package runtimeext

import (
	"context"
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

type FileVerificationExecution interface {
	VerifyFileClean(context.Context, FileVerificationRequest) (FileVerificationEvidence, error)
}

func VerifyFileClean(ctx context.Context, execution ActionExecution, request FileVerificationRequest) (FileVerificationEvidence, error) {
	verifier, ok := execution.(FileVerificationExecution)
	if !ok {
		return FileVerificationEvidence{}, &BusinessError{Code: "backend.upload.scan_service_unavailable", Message: "Runtime file verification is unavailable"}
	}
	return verifier.VerifyFileClean(ctx, request)
}

type FileVerificationRequest struct {
	FileID        string
	ContentSHA256 string
	ScanReceipt   string
}

type FileVerificationEvidence struct {
	FileID, ContentSHA256, Status, Provider, EvidenceRef string
	Size                                                 int64
}
