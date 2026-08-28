package contract

import (
	"context"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

// AutomationWorkerStore is the minimal lease persistence capability needed by
// the Automation instruction runtime.
type AutomationWorkerStore interface {
	ClaimInstruction(context.Context, string, automationmodel.AutomationInstructionExecution, string, string, string) (automationmodel.AutomationInstructionExecution, bool, error)
	HeartbeatInstruction(ctx context.Context, workspaceID, idempotencyKey, expectedLeaseOwner string, expectedFencingToken int64, leaseExpiresAt, now string) (automationmodel.AutomationInstructionExecution, error)
	CompleteInstruction(ctx context.Context, workspaceID, idempotencyKey, expectedLeaseOwner string, expectedFencingToken int64, status string, result map[string]any, errorCode, now string) (automationmodel.AutomationInstructionExecution, error)
}
