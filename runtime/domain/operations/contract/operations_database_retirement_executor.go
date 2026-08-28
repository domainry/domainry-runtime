package contract

import (
	"context"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

type DatabaseRetirementExecutionResult struct {
	ExecutedStatements int    `json:"executed_statements"`
	AuditEventID       string `json:"audit_event_id"`
	Dirty              bool   `json:"dirty"`
	BlockedReason      string `json:"blocked_reason,omitempty"`
}

// DatabaseRetirementExecutor is an Infrastructure port. It accepts only a
// typed, policy-validated plan; callers cannot submit arbitrary SQL.
type DatabaseRetirementExecutor interface {
	ApplyDatabaseRetirementTransition(context.Context, operationsmodel.DatabaseRetirement, operationsmodel.DatabaseRetirement) (operationsmodel.DatabaseRetirement, error)
	PreviewDatabaseRetirement(context.Context, operationsmodel.DatabaseRetirement) (operationsmodel.DatabaseDropPlan, error)
	ExecuteDatabaseRetirement(context.Context, operationsmodel.DatabaseRetirement, operationsmodel.DatabaseDropPlan) (DatabaseRetirementExecutionResult, error)
}
