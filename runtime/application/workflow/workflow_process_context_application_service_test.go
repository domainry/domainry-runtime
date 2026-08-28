package workflow

import (
	"context"
	"errors"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowDecisionAndRetryRejectCancelledContextBeforeIO(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	engine := NewWorkflowProcessEngine(WorkflowDependencies{})
	if _, err := engine.DecideTask(ctx, "task-1", workflowmodel.WorkflowTaskDecisionRequest{Decision: "approved"}, principalmodel.Principal{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("decision must stop before repository I/O, got %v", err)
	}
	if _, err := NewWorkflowApplicationService(WorkflowDependencies{}).RetryWorkflowProcess(ctx, "process-1", principalmodel.Principal{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("retry must stop before repository I/O, got %v", err)
	}
}
