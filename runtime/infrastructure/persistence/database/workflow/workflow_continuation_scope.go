package workflow

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const workflowContinuationQueueKind = "workflow_continuation"

func RegisterWorkflowContinuationScope(ctx context.Context, store *database.RuntimeStore, executor database.WorkerScopeExecutor, workspaceID, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	return store.RegisterWorkerQueueScope(ctx, executor, workflowContinuationQueueKind, workspaceID, updatedAt)
}
