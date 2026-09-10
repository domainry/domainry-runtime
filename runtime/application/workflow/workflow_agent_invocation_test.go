package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type agentWorkflowReceiptProbe struct {
	workflowExecutionWorkerStub
	receipt workflowmodel.WorkflowExecutionReceipt
	found   bool
	err     error
	reads   int
}

func (p *agentWorkflowReceiptProbe) FindExecutionReceipt(_ context.Context, _, _, _ string) (workflowmodel.WorkflowExecutionReceipt, bool, error) {
	p.reads++
	return p.receipt, p.found, p.err
}

func TestAgentWorkflowInspectionDoesNotExecuteAndRejectsUnownedReceipts(t *testing.T) {
	workflow, principal := workflowExecutionSchema(), workflowExecutionPrincipal()
	payload := map[string]any{"source": "conversation"}
	key, _ := agentWorkflowInvocationKey(workflow.Key, "logical-start", principal)
	fingerprint, _ := workflowExecutionFingerprint(workflow, payload, "agent", 1)
	execution := workflowmodel.WorkflowExecution{ID: "execution", ProcessID: "process", WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, ActorID: principal.UserID, Trigger: "agent", IdempotencyKey: key, Status: "waiting"}
	receipt := workflowmodel.WorkflowExecutionReceipt{WorkspaceID: principal.WorkspaceID, WorkflowKey: workflow.Key, IdempotencyKey: key, RequestFingerprint: fingerprint, ExecutionID: execution.ID, Status: string(idempotency.StatusSucceeded), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	probe := &agentWorkflowReceiptProbe{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{execution.ID: execution}}, receipt: receipt, found: true}
	service := newWorkflowExecutionService(&probe.workflowExecutionWorkerStub, workflow)
	service.workerRepo = probe
	got, found, err := service.InspectAgentWorkflowInvocation(t.Context(), workflow.Key, payload, "logical-start", principal)
	if err != nil || !found || got.ProcessID != "process" || got.Status != "waiting" {
		t.Fatal(got, found, err)
	}
	for _, mutate := range []func(){
		func() { probe.receipt.RequestFingerprint = "changed" },
		func() { probe.receipt.WorkspaceID = "other" },
		func() { probe.receipt.WorkflowKey = "other" },
		func() { probe.receipt.Status = string(idempotency.StatusProcessing) },
		func() { probe.receipt.ExpiresAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano) },
		func() { other := execution; other.ActorID = "other"; probe.executions[execution.ID] = other },
		func() { probe.err = errors.New("unavailable") },
	} {
		probe.receipt, probe.err, probe.executions[execution.ID] = receipt, nil, execution
		mutate()
		if _, _, err := service.InspectAgentWorkflowInvocation(t.Context(), workflow.Key, payload, "logical-start", principal); err == nil {
			t.Fatal("invalid receipt was accepted")
		}
	}
	if len(probe.claimRequests) != 0 || len(probe.inserted) != 0 || len(probe.updated) != 0 {
		t.Fatal("inspection acquired or mutated an execution")
	}
	probe.err, probe.found = nil, false
	if _, found, err := service.InspectAgentWorkflowInvocation(t.Context(), workflow.Key, payload, "logical-start", principal); err != nil || found {
		t.Fatal("absence must remain distinct", found, err)
	}
	reads := probe.reads
	if _, _, err := service.InspectAgentWorkflowInvocation(t.Context(), workflow.Key, payload, "logical-start", principalmodel.Principal{}); err == nil || probe.reads != reads {
		t.Fatal("unknown user reached receipts")
	}
}

func TestAgentWorkflowStartUsesOwnerScopedNoReclaimAndClosedInputs(t *testing.T) {
	workflow, principal := workflowExecutionSchema(), workflowExecutionPrincipal()
	probe := &agentWorkflowReceiptProbe{workflowExecutionWorkerStub: workflowExecutionWorkerStub{claim: workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionInProgress}}}
	service := newWorkflowExecutionService(&probe.workflowExecutionWorkerStub, workflow)
	service.workerRepo = probe
	for _, payload := range []map[string]any{{"run_as": "admin"}, {"source": 123}, {"initiating_user_id": "other"}} {
		if _, err := service.RunAgentWorkflowWithKey(t.Context(), workflow.Key, payload, "same", principal); err == nil {
			t.Fatal("invalid input accepted", payload)
		}
	}
	if len(probe.claimRequests) != 0 {
		t.Fatal("bad input acquired a start")
	}
	if _, err := service.RunAgentWorkflowWithKey(t.Context(), workflow.Key, map[string]any{"source": "conversation"}, "same", principal); err == nil {
		t.Fatal("unresolved start should remain unresolved")
	}
	if len(probe.claimRequests) != 1 || !probe.claimRequests[0].PreventReclaim || len(probe.inserted) != 0 {
		t.Fatal("conversation did not request guarded replay", probe.claimRequests)
	}
	other := principal
	other.UserID = "other"
	first, _ := agentWorkflowInvocationKey(workflow.Key, "same", principal)
	second, _ := agentWorkflowInvocationKey(workflow.Key, "same", other)
	if first == second {
		t.Fatal("users shared a logical start")
	}
}
