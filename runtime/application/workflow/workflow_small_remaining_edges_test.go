package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowWaitTimerServiceProbe struct {
	id  string
	err error
}

func (p workflowWaitTimerServiceProbe) ScheduleWorkflowWaitTimer(context.Context, WorkflowWaitTimerRequest) (string, error) {
	return p.id, p.err
}

func TestNormalizeWorkflowProcessStatusesSkipsBlankAndDuplicates(t *testing.T) {
	if got := normalizeWorkflowProcessStatuses([]string{" open, ,closed ", "open", "closed,pending"}); !reflect.DeepEqual(got, []string{"open", "closed", "pending"}) {
		t.Fatalf("statuses=%v", got)
	}
}

func TestValidateWorkflowAuthoringFragmentBoundaries(t *testing.T) {
	service := NewWorkflowApplicationService(WorkflowDependencies{WorkflowRegistry: &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}}})
	if _, err := service.ValidateAuthoringFragment(t.Context(), "workflow.trigger_contract", map[string]any{"type": "manual"}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}
	if report, err := service.ValidateAuthoringFragment(t.Context(), "workflow.unknown", map[string]any{}, principal); err != nil || report.Valid || len(report.Issues) != 1 {
		t.Fatalf("invalid report=%+v err=%v", report, err)
	}
	if report, err := service.ValidateAuthoringFragment(t.Context(), "workflow.trigger_contract", map[string]any{"type": "manual"}, principal); err != nil || !report.Valid {
		t.Fatalf("valid report=%+v err=%v", report, err)
	}
}

func TestExecuteCommittedWorkflowIntentsNilLifecycleBoundaries(t *testing.T) {
	service := &WorkflowApplicationService{}
	service.executeCommittedWorkflowIntents(nil, nil, principalmodel.Principal{})
	var missing *WorkflowApplicationService
	missing.executeCommittedWorkflowIntents(context.Background(), nil, principalmodel.Principal{})
	service.executeCommittedWorkflowIntents(context.Background(), []workflowmodel.WorkflowExecution{{WorkflowKey: "missing"}}, principalmodel.Principal{})
}

func TestWorkflowMetadataAndActionObjectKeyEmptyBranches(t *testing.T) {
	metadata := workflowExecutionAuditMetadata(workflowmodel.WorkflowExecution{Result: map[string]any{"created_object_key": "", "created_record_id": "record"}}, principalmodel.Principal{})
	if _, found := metadata["created_object_key"]; found || metadata["created_record_id"] != "record" {
		t.Fatalf("metadata=%v", metadata)
	}
	for _, invalid := range []string{"$input.a..b", "$input.a-b.value"} {
		if _, ok := workflowNestedReferenceValue(invalid, "$input.", map[string]any{}); ok {
			t.Fatalf("invalid nested reference %q accepted", invalid)
		}
	}
	if value, ok := workflowNestedReferenceValue("$input.account.id", "$input.", map[string]any{"account": map[string]any{"id": "account-1"}}); !ok || value != "account-1" {
		t.Fatalf("nested reference value=%v ok=%v", value, ok)
	}
	if value, ok := workflowNestedReferenceValue("$input.account.id", "$input.", map[string]any{"account": "not-an-object"}); !ok || value != nil {
		t.Fatalf("non-object nested reference value=%v ok=%v", value, ok)
	}
}

func TestWorkflowProcessTimerNodeBoundaries(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	process := workflowEngineProcess(nil, nil)
	node := definitionmodel.WorkflowGraphNode{ID: "timer", Type: "timer", Contract: &definitionmodel.WorkflowNodeContract{Timer: &definitionmodel.WorkflowTimerNodeContract{}}}
	newStore := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}}
	}
	if _, _, err := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: newStore()}).ProcessEngine().executeNode(t.Context(), &process, node, principal); err == nil {
		t.Fatal("missing wait timer service accepted")
	}
	store := newStore()
	store.insertNodeErr = errors.New("insert")
	if _, _, err := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, WaitTimers: workflowWaitTimerServiceProbe{id: "timer"}}).ProcessEngine().executeNode(t.Context(), &process, node, principal); err == nil {
		t.Fatal("insert error ignored")
	}
	store = newStore()
	scheduleErr := errors.New("schedule")
	if _, _, err := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, WaitTimers: workflowWaitTimerServiceProbe{err: scheduleErr}}).ProcessEngine().executeNode(t.Context(), &process, node, principal); !errors.Is(err, scheduleErr) {
		t.Fatalf("schedule err=%v", err)
	}
	store = newStore()
	store.updateNodeErr = errors.New("update")
	if _, _, err := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, WaitTimers: workflowWaitTimerServiceProbe{id: "timer"}}).ProcessEngine().executeNode(t.Context(), &process, node, principal); err == nil {
		t.Fatal("update error ignored")
	}
	store = newStore()
	if outcome, waiting, err := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: store, WaitTimers: workflowWaitTimerServiceProbe{id: "timer"}}).ProcessEngine().executeNode(t.Context(), &process, node, principal); err != nil || outcome != "waiting" || !waiting {
		t.Fatalf("outcome=%q waiting=%v err=%v", outcome, waiting, err)
	}
}

func TestWorkflowRemainingProjectionSchedulingAndSimulationBranches(t *testing.T) {
	withoutGraph := ProjectBusinessWorkflowProcessDetail(WorkflowProcessDetail{Nodes: []workflowmodel.WorkflowNodeInstance{{ID: "instance", NodeID: "node"}}})
	if withoutGraph.Nodes[0].Name != "node" {
		t.Fatalf("without graph=%+v", withoutGraph.Nodes)
	}
	graph := &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "other"}, {ID: "node", Contract: &definitionmodel.WorkflowNodeContract{}}}}
	withFallbacks := ProjectBusinessWorkflowProcessDetail(WorkflowProcessDetail{
		Process: workflowmodel.WorkflowProcessInstance{DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: graph}},
		Nodes:   []workflowmodel.WorkflowNodeInstance{{ID: "instance", NodeID: "node"}},
	})
	if withFallbacks.Nodes[0].Name != "node" || withFallbacks.Nodes[0].ActionKey != "" {
		t.Fatalf("fallbacks=%+v", withFallbacks.Nodes)
	}
	service := NewWorkflowApplicationService(WorkflowDependencies{WorkflowRegistry: &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{}}})
	if processed, err := service.processScheduledWorkflowExecutionsForTarget(t.Context(), "scheduled:*", 1, principalmodel.Principal{}, service.worker.Clock.Now()); err != nil || len(processed) != 0 {
		t.Fatalf("processed=%+v err=%v", processed, err)
	}
	engine := NewWorkflowProcessRuntime(WorkflowDependencies{}).ProcessEngine()
	preview, outcomes, err := engine.simulateNode(t.Context(), workflowmodel.WorkflowProcessInstance{}, definitionmodel.WorkflowGraphNode{ID: "timer", Type: "timer"}, principalmodel.Principal{})
	if err != nil || preview.Outcome != "scheduled" || len(outcomes) != 1 {
		t.Fatalf("preview=%+v outcomes=%v err=%v", preview, outcomes, err)
	}
}

func TestWorkflowApplicationResumeTimerNodeDelegates(t *testing.T) {
	service := NewWorkflowApplicationService(WorkflowDependencies{})
	if _, err := service.ResumeTimerNode(t.Context(), "workspace", "process", "timer", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
}
