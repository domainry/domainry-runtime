package workflow

import (
	"context"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestWorkflowRetryExecutionGuardsAndReplay(t *testing.T) {
	principal := workflowExecutionPrincipal()
	previous := workflowmodel.WorkflowExecution{ID: "previous", WorkflowKey: "order.approve", Status: "failed", Attempt: 1, Result: map[string]any{}}
	worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"previous": previous}}
	service := newWorkflowExecutionService(worker)

	missingWorkspace := principal
	missingWorkspace.WorkspaceID = ""
	if _, err := service.RetryWorkflowExecution(t.Context(), previous.ID, missingWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace=%v", err)
	}
	unknown := principal
	unknown.Known = false
	if _, err := service.RetryWorkflowExecution(t.Context(), previous.ID, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown=%v", err)
	}
	denied := principal
	denied = workflowPrincipalWithPermissions(denied)
	if _, err := service.RetryWorkflowExecution(t.Context(), previous.ID, denied); apperror.CodeOf(err) != "backend.workflow.retry_permission_required" {
		t.Fatalf("denied=%v", err)
	}
	worker.getErr = errWorkflowExecutionStore
	if _, err := service.RetryWorkflowExecution(t.Context(), previous.ID, principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("store=%v", err)
	}
	worker.getErr = nil
	if _, err := service.RetryWorkflowExecution(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.workflow.execution_not_found" {
		t.Fatalf("not found=%v", err)
	}

	retry := workflowmodel.WorkflowExecution{ID: "retry", WorkflowKey: previous.WorkflowKey, Status: "completed", Payload: map[string]any{"order": "one"}}
	skipped := previous
	skipped.Status = "skipped"
	skipped.Result = map[string]any{"manual_retry_execution_id": retry.ID}
	worker.executions[skipped.ID], worker.executions[retry.ID] = skipped, retry
	worker.getErrors = map[string]error{retry.ID: errWorkflowExecutionStore}
	if _, err := service.RetryWorkflowExecutionWithKey(t.Context(), skipped.ID, "key", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("retry lookup=%v", err)
	}
	worker.getErrors = nil
	if _, err := service.RetryWorkflowExecutionWithKey(t.Context(), skipped.ID, "key", principal); apperror.CodeOf(err) != "backend.workflow.not_found" {
		t.Fatalf("replay workflow=%v", err)
	}
	service.registry.Set(previous.WorkflowKey, workflowExecutionSchema())
	if result, err := service.RetryWorkflowExecutionWithKey(t.Context(), skipped.ID, "key", principal); err != nil || result.Execution.ID != retry.ID {
		t.Fatalf("replay=%#v err=%v", result, err)
	}
	commandKey := workflowCommandKey("execution.retry", skipped.ID, "key", map[string]any{"attempt": skipped.Attempt + 1})
	for name, resultMap := range map[string]map[string]any{
		"matching key without retry": {workflowCommandResultField("execution.retry"): commandKey},
		"mismatched key":             {workflowCommandResultField("execution.retry"): "other"},
		"missing retry execution":    {workflowCommandResultField("execution.retry"): commandKey, "manual_retry_execution_id": "missing"},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := skipped
			candidate.Result = resultMap
			worker.executions[candidate.ID] = candidate
			if _, err := service.RetryWorkflowExecutionWithKey(t.Context(), candidate.ID, "key", principal); apperror.CodeOf(err) != "backend.workflow.retry_status_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestWorkflowRetryExecutionStatusLimitsAndSuccess(t *testing.T) {
	principal := workflowExecutionPrincipal()
	worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}
	workflow := workflowExecutionSchema()
	service := newWorkflowExecutionService(worker, workflow)

	worker.executions["running"] = workflowmodel.WorkflowExecution{ID: "running", WorkflowKey: workflow.Key, Status: "running"}
	if _, err := service.RetryWorkflowExecutionWithKey(t.Context(), "running", "key", principal); apperror.CodeOf(err) != "backend.workflow.retry_status_invalid" {
		t.Fatalf("status=%v", err)
	}
	worker.executions["unknown-workflow"] = workflowmodel.WorkflowExecution{ID: "unknown-workflow", WorkflowKey: "missing", Status: "failed"}
	if _, err := service.RetryWorkflowExecutionWithKey(t.Context(), "unknown-workflow", "key", principal); apperror.CodeOf(err) != "backend.workflow.not_found" {
		t.Fatalf("workflow=%v", err)
	}
	limited := workflow
	limited.Key = "limited"
	limited.Retry = &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 1}
	service.registry.Set(limited.Key, limited)
	worker.executions["limited"] = workflowmodel.WorkflowExecution{ID: "limited", WorkflowKey: limited.Key, Status: "dead_letter", Attempt: 1}
	if _, err := service.RetryWorkflowExecutionWithKey(t.Context(), "limited", "key", principal); apperror.CodeOf(err) != "backend.workflow.max_attempts_reached" {
		t.Fatalf("limit=%v", err)
	}
	invalid := workflow
	invalid.Key, invalid.Graph = "invalid", nil
	service.registry.Set(invalid.Key, invalid)
	worker.executions["invalid"] = workflowmodel.WorkflowExecution{ID: "invalid", WorkflowKey: invalid.Key, Status: "failed"}
	if _, err := service.RetryWorkflowExecutionWithKey(t.Context(), "invalid", "key", principal); apperror.CodeOf(err) != "backend.workflow.graph_v2_required" {
		t.Fatalf("execution=%v", err)
	}

	previous := workflowmodel.WorkflowExecution{ID: "previous", WorkflowKey: workflow.Key, Status: "failed", Attempt: -1, Payload: map[string]any{"order": "one"}, Result: map[string]any{}}
	worker.executions[previous.ID] = previous
	result, err := service.RetryWorkflowExecutionWithKey(t.Context(), previous.ID, "key", principal)
	if err != nil || result.Execution.ID == "" || result.Execution.Attempt != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	stored := worker.executions[previous.ID]
	if stored.Status != "skipped" || stored.Result["manual_retry_execution_id"] != result.Execution.ID || len(worker.updated) != 1 {
		t.Fatalf("stored=%#v updated=%d", stored, len(worker.updated))
	}

	worker.executions["update-failure"] = workflowmodel.WorkflowExecution{ID: "update-failure", WorkflowKey: workflow.Key, Status: "failed", Attempt: 1, Result: map[string]any{}}
	worker.updateErr = errWorkflowExecutionStore
	if _, err := service.RetryWorkflowExecutionWithKey(t.Context(), "update-failure", "key", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update=%v", err)
	}
}

func TestWorkflowResolveExecutionContracts(t *testing.T) {
	principal := workflowExecutionPrincipal()
	deadLetter := workflowmodel.WorkflowExecution{ID: "dead", WorkflowKey: "order.approve", Status: "dead_letter", Result: map[string]any{}, Payload: map[string]any{"object_key": "order", "record_id": "one"}}
	worker := &workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{"dead": deadLetter}}
	audits := 0
	service := newWorkflowExecutionService(worker)
	service.auditMetadata = func(_ context.Context, _, _, _ string, _ principalmodel.Principal, _ string, _, _, _ map[string]any) {
		audits++
	}

	missingWorkspace := principal
	missingWorkspace.WorkspaceID = ""
	if _, err := service.ResolveWorkflowExecution(t.Context(), deadLetter.ID, "", missingWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace=%v", err)
	}
	unknown := principal
	unknown.Known = false
	if _, err := service.ResolveWorkflowExecution(t.Context(), deadLetter.ID, "", unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown=%v", err)
	}
	denied := principal
	denied = workflowPrincipalWithPermissions(denied)
	if _, err := service.ResolveWorkflowExecution(t.Context(), deadLetter.ID, "", denied); apperror.CodeOf(err) != "backend.workflow.resolve_permission_required" {
		t.Fatalf("denied=%v", err)
	}
	worker.getErr = errWorkflowExecutionStore
	if _, err := service.ResolveWorkflowExecution(t.Context(), deadLetter.ID, "", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("store=%v", err)
	}
	worker.getErr = nil
	if _, err := service.ResolveWorkflowExecution(t.Context(), "missing", "", principal); apperror.CodeOf(err) != "backend.workflow.execution_not_found" {
		t.Fatalf("missing=%v", err)
	}
	worker.executions["running"] = workflowmodel.WorkflowExecution{ID: "running", Status: "running"}
	if _, err := service.ResolveWorkflowExecution(t.Context(), "running", "", principal); apperror.CodeOf(err) != "backend.workflow.resolve_status_invalid" {
		t.Fatalf("status=%v", err)
	}
	worker.executions["resolved"] = workflowmodel.WorkflowExecution{ID: "resolved", Status: "resolved", Message: "done"}
	if result, err := service.ResolveWorkflowExecution(t.Context(), "resolved", "", principal); err != nil || result.Status != "resolved" {
		t.Fatalf("resolved=%#v err=%v", result, err)
	}
	worker.updateErr = errWorkflowExecutionStore
	if _, err := service.ResolveWorkflowExecution(t.Context(), deadLetter.ID, "reason", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("update=%v", err)
	}
	worker.updateErr = nil
	result, err := service.ResolveWorkflowExecution(t.Context(), deadLetter.ID, "", principal)
	if err != nil || result.Status != "resolved" || result.Execution.Result["resolve_reason"] == "" || audits != 1 {
		t.Fatalf("result=%#v audits=%d err=%v", result, audits, err)
	}
}
