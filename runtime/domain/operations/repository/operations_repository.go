package repository

import (
	"context"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

// OperationsRepository owns the durable cross-instance operation ledger.
type OperationsRepository interface {
	RegisterOperationsCommand(context.Context, operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error)
	GetOperationsReceipt(context.Context, operationsmodel.OperationsScope, string) (operationsmodel.OperationsReceipt, bool, error)
	ListOperationsReceipts(context.Context, operationsmodel.OperationsScope, operationsmodel.OperationsStatus, int) ([]operationsmodel.OperationsReceipt, error)
	SearchOperationsReceipts(context.Context, operationsmodel.OperationsScope, operationsmodel.OperationsReceiptFilter) (operationsmodel.OperationsReceiptPage, error)
	UpdateOperationsReceipt(context.Context, operationsmodel.OperationsReceipt, operationsmodel.OperationsStatus) (bool, error)
}

// OperationsControlRepository stores desired maintenance, owner-pause and
// instance-drain state so all Runtime instances observe the same controls.
type OperationsControlRepository interface {
	GetOperationsControl(context.Context, string, operationsmodel.OperationsControlKind, string) (operationsmodel.OperationsControl, bool, error)
	ListOperationsControls(context.Context, string, operationsmodel.OperationsControlKind, int) ([]operationsmodel.OperationsControl, error)
	PutOperationsControl(context.Context, operationsmodel.OperationsControl, int64) (bool, error)
}

type OperationsLeaseRepository interface {
	OperationsLeaseSnapshot(context.Context, string, time.Time) (operationsmodel.OperationsLeaseSnapshot, error)
	ForceReleaseOperationsLease(context.Context, operationsmodel.OperationsLeaseReleaseRequest) (operationsmodel.OperationsLeaseReleaseResult, bool, error)
}

type OperationsDiagnosticsRepository interface {
	OperationsDiagnosticsSnapshot(context.Context, operationsmodel.OperationsDiagnosticsRequest) (operationsmodel.OperationsDiagnosticsSnapshot, error)
}

type OperationsBreakGlassRepository interface {
	CreateOperationsBreakGlass(context.Context, operationsmodel.OperationsBreakGlassGrant) (bool, error)
	GetOperationsBreakGlass(context.Context, string) (operationsmodel.OperationsBreakGlassGrant, bool, error)
	ListOperationsBreakGlass(context.Context, string, int) ([]operationsmodel.OperationsBreakGlassGrant, error)
	RevokeOperationsBreakGlass(context.Context, operationsmodel.OperationsBreakGlassGrant, int64) (bool, error)
}

// DatabaseRetirementRepository persists the Runtime-global retirement state
// machine and its low-cardinality compatibility access observations.
type DatabaseRetirementRepository interface {
	RegisterDatabaseRetirement(context.Context, operationsmodel.DatabaseRetirement) (bool, error)
	GetDatabaseRetirement(context.Context, string) (operationsmodel.DatabaseRetirement, bool, error)
	ListDatabaseRetirements(context.Context, operationsmodel.DatabaseRetirementState, int) ([]operationsmodel.DatabaseRetirement, error)
	TransitionDatabaseRetirement(context.Context, operationsmodel.DatabaseRetirement, operationsmodel.DatabaseRetirementState) (bool, error)
	RecordDatabaseRetirementAccess(context.Context, string, string, string, time.Time) error
}
