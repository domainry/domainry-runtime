package schedulermodulehost

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type windowedWorkflowRuntime struct {
	target string
	window time.Time
	limit  int
}

func (w *windowedWorkflowRuntime) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return workflowmodel.WorkflowProcessResult{}, nil
}

func (w *windowedWorkflowRuntime) ProcessDueWorkflowExecutionsForScheduledWindow(_ context.Context, target string, window time.Time, limit int, _ principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	w.target, w.window, w.limit = target, window, limit
	return workflowmodel.WorkflowProcessResult{Executions: []workflowmodel.WorkflowExecution{{ID: "workflow-execution-1"}}}, nil
}

func TestDownstreamDispatcherUsesWindowedWorkflowOwnerAndReturnsReceipt(t *testing.T) {
	workflows := &windowedWorkflowRuntime{}
	dispatcher := NewDownstreamDispatcher(workflows)
	window := time.Date(2026, time.September, 1, 2, 3, 4, 0, time.FixedZone("test", 8*60*60))
	receipt, err := dispatcher.Dispatch(t.Context(), PublishedDefinition{Data: map[string]any{"target_type": "workflow", "target_key": "scheduled:orders.sync"}}, "run-1", window, 0, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}})
	if err != nil || receipt != "workflow-execution-1" || workflows.target != "scheduled:orders.sync" || !workflows.window.Equal(window.UTC()) || workflows.limit != 25 {
		t.Fatalf("receipt=%q workflows=%#v err=%v", receipt, workflows, err)
	}
}
