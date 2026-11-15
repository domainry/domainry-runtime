package action

import (
	"context"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"testing"
)

func workflowEvidenceActionFixture() (*businessActionExecution, *int, runtimeext.WorkflowAgentTaskEvidenceRequest) {
	calls := 0
	e := &businessActionExecution{unitOfWork: newActionTestUnitOfWork(), invocation: actionmodel.ActionInvocation{Source: actionmodel.ActionSourceWorkflow, ProcessID: "process", NodeID: "save", ObjectKey: "turn", RecordID: "turn-a", Principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "agent", RoleKey: "agent_service"}}}, workflowGrants: []runtimeext.WorkflowGrant{{Key: "case.decide", Operations: []string{runtimeext.WorkflowReadAgentTaskEvidenceOperation}}}}
	e.dependencies.ReadWorkflowAgentTaskEvidence = func(_ context.Context, r runtimeext.WorkflowAgentTaskEvidenceRequest, i actionmodel.ActionInvocation) (runtimeext.WorkflowAgentTaskEvidence, error) {
		calls++
		if i.ProcessID != "process" || i.RecordID != "turn-a" || i.Principal.WorkspaceID != "workspace" {
			panic("caller replaced trusted scope")
		}
		return runtimeext.WorkflowAgentTaskEvidence{Available: true, TaskRunID: "owner-run"}, nil
	}
	return e, &calls, runtimeext.WorkflowAgentTaskEvidenceRequest{WorkflowKey: "case.decide", NodeID: "decide", TaskKey: "support.decide", TaskVersion: "1"}
}
func TestWorkflowAgentEvidenceActionRequiresCurrentWorkflowAndExactReadGrant(t *testing.T) {
	e, calls, r := workflowEvidenceActionFixture()
	proof, err := runtimeext.ReadWorkflowAgentTaskEvidence(t.Context(), e, r)
	if err != nil || !proof.Available || *calls != 1 {
		t.Fatal(proof, err)
	}
	for _, change := range []func(*businessActionExecution){func(e *businessActionExecution) { e.invocation.Source = actionmodel.ActionSourceHTTP }, func(e *businessActionExecution) { e.invocation.ProcessID = "" }, func(e *businessActionExecution) { e.invocation.NodeID = "" }, func(e *businessActionExecution) { e.invocation.Principal.Known = false }, func(e *businessActionExecution) { e.workflowGrants[0].Key = "other" }, func(e *businessActionExecution) {
		e.workflowGrants[0].Operations = []string{runtimeext.WorkflowStartOperation}
	}} {
		e, calls, r := workflowEvidenceActionFixture()
		change(e)
		if _, err := e.ReadWorkflowAgentTaskEvidence(t.Context(), r); apperror.CodeOf(err) != runtimeext.WorkflowAgentTaskEvidenceDeniedErrorCode || *calls != 0 {
			t.Fatal("undeclared or external caller reached evidence", err)
		}
	}
	e, calls, r = workflowEvidenceActionFixture()
	_ = e.unitOfWork.phases.beginWriting()
	if _, err = e.ReadWorkflowAgentTaskEvidence(t.Context(), r); apperror.CodeOf(err) != "backend.action.execution_phase_invalid" || *calls != 0 {
		t.Fatal("evidence read after writes", err)
	}
}

func TestRecordOwnerResolutionPreservesOnlyLocalPrewriteEvidenceReads(t *testing.T) {
	e, calls, request := workflowEvidenceActionFixture()
	e.targetGrant = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceRecordOwner}
	e.dependencies.ResolveRecordTargetOrganization = func(_ context.Context, _ definitionmodel.ActionSchema, invocation actionmodel.ActionInvocation) (string, error) {
		if invocation.RecordID != "turn-a" || invocation.Principal.WorkspaceID != "workspace" {
			t.Fatal("owner resolution lost the authorized record")
		}
		return "store-a", nil
	}
	if err := e.initializeTargetOrganization(t.Context()); err != nil {
		t.Fatal(err)
	}
	if e.unitOfWork.transaction == nil || e.unitOfWork.phases.current() != runtimeext.ExecutionPhasePrewrite || !e.targetResolved || e.targetOrganization.ID != "store-a" {
		t.Fatal("record owner resolution must lock its transaction without claiming a business write")
	}
	if _, err := e.unitOfWork.acquireSynchronousConnectorCall(); apperror.CodeOf(err) != runtimeext.ConnectorCallAfterWriteErrorCode {
		t.Fatal("a metadata transaction must not permit external calls", err)
	}
	if proof, err := e.ReadWorkflowAgentTaskEvidence(t.Context(), request); err != nil || !proof.Available || *calls != 1 {
		t.Fatal("record ownership initialization blocked the trusted Workflow receipt", err)
	}
	if _, err := e.unitOfWork.beginDeferredWriting(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := e.ReadWorkflowAgentTaskEvidence(t.Context(), request); apperror.CodeOf(err) != "backend.action.execution_phase_invalid" || *calls != 1 {
		t.Fatal("receipt access reopened after business writing began", err)
	}
}
