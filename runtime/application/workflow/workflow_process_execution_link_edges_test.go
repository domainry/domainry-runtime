package workflow

import (
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestFailedWorkflowProcessExecutionProjectsLatestFailedNode(t *testing.T) {
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", ObjectKey: "order", RecordID: "record", DefinitionHash: "hash", CreatedAt: "created"}
	store := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{
		"process": {
			{ID: "ignored", NodeID: "ignored", Status: "completed"},
			{ID: "configuration", NodeID: "configuration", Status: "configuration_error"},
			{ID: "failed", NodeID: "failed", ErrorCode: "provider.timeout"},
		},
	}}
	service := &WorkflowApplicationService{processRepo: store}
	execution := service.failedWorkflowProcessExecution(t.Context(), definitionmodel.WorkflowSchema{Key: "flow", Name: "Flow", RunAs: "service"}, process, map[string]any{"value": true}, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "actor"}}, "manual", 2, "key", errors.New("failed"))
	if execution.ID != "process" || execution.Status != "failed" || execution.NodeID != "failed" || execution.Result["node_instance_id"] != "failed" {
		t.Fatalf("execution=%+v", execution)
	}
	store.nodes["process"] = []workflowmodel.WorkflowNodeInstance{
		{ID: "configuration", NodeID: "configuration", Status: "configuration_error"},
		{ID: "ignored", NodeID: "ignored", Status: "completed"},
	}
	execution = service.failedWorkflowProcessExecution(t.Context(), definitionmodel.WorkflowSchema{Key: "flow"}, process, nil, principalmodel.Principal{}, "manual", 0, "", errors.New("failed"))
	if execution.NodeID != "configuration" {
		t.Fatalf("configuration execution=%+v", execution)
	}
	store.nodes["process"] = nil
	execution = service.failedWorkflowProcessExecution(t.Context(), definitionmodel.WorkflowSchema{Key: "flow"}, process, nil, principalmodel.Principal{}, "manual", 0, "", errors.New("failed"))
	if execution.NodeID != "" {
		t.Fatalf("unexpected node=%+v", execution)
	}
}

func TestSyncWorkflowExecutionWithProcessOutcomeMatrix(t *testing.T) {
	if err := (&WorkflowApplicationService{}).syncWorkflowExecutionWithProcess(t.Context(), workflowmodel.WorkflowProcessInstance{}, nil); err != nil {
		t.Fatal(err)
	}
	baseExecution := workflowmodel.WorkflowExecution{ID: "process", Result: map[string]any{"existing": true}, NodeID: "old", LastError: "old", NextRunAt: "later"}
	for name, testCase := range map[string]struct {
		process     workflowmodel.WorkflowProcessInstance
		decisionErr error
		getErr      error
		found       bool
		updateErr   error
	}{
		"missing":        {process: workflowmodel.WorkflowProcessInstance{ID: "process"}},
		"get error":      {process: workflowmodel.WorkflowProcessInstance{ID: "process"}, getErr: errors.New("get")},
		"running node":   {process: workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", Status: "running", CurrentNodeIDs: []string{"node"}, UpdatedAt: "updated"}, found: true},
		"decision error": {process: workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", Status: "waiting", UpdatedAt: "updated"}, found: true, decisionErr: errors.New("decision")},
		"completed":      {process: workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", Status: "completed", UpdatedAt: "updated"}, found: true},
		"rejected":       {process: workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", Status: "rejected", UpdatedAt: "updated"}, found: true},
		"cancelled":      {process: workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", Status: "cancelled", UpdatedAt: "updated"}, found: true},
		"update error":   {process: workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", Status: "running", UpdatedAt: "updated"}, found: true, updateErr: errors.New("update")},
	} {
		t.Run(name, func(t *testing.T) {
			executions := map[string]workflowmodel.WorkflowExecution{}
			if testCase.found {
				executions["process"] = baseExecution
			}
			worker := &workflowExecutionWorkerStub{executions: executions, getErr: testCase.getErr, updateErr: testCase.updateErr}
			service := &WorkflowApplicationService{workerRepo: worker}
			err := service.syncWorkflowExecutionWithProcess(t.Context(), testCase.process, testCase.decisionErr)
			if testCase.getErr != nil && !errors.Is(err, testCase.getErr) {
				t.Fatalf("get error=%v", err)
			}
			if testCase.updateErr != nil && !errors.Is(err, testCase.updateErr) {
				t.Fatalf("update error=%v", err)
			}
			if testCase.getErr == nil && testCase.updateErr == nil && err != nil {
				t.Fatal(err)
			}
			if !testCase.found || testCase.getErr != nil || testCase.updateErr != nil {
				return
			}
			updated := worker.updated[0]
			if len(testCase.process.CurrentNodeIDs) > 0 && updated.NodeID != "node" {
				t.Fatalf("updated=%+v", updated)
			}
			if len(testCase.process.CurrentNodeIDs) == 0 {
				if updated.NodeID != "" {
					t.Fatalf("node not cleared: %+v", updated)
				}
				if _, exists := updated.Result["node_id"]; exists {
					t.Fatalf("result node not cleared: %v", updated.Result)
				}
			}
			if testCase.decisionErr != nil && updated.LastError != "decision" {
				t.Fatalf("decision error=%+v", updated)
			}
			terminal := testCase.process.Status == "completed" || testCase.process.Status == "rejected" || testCase.process.Status == "cancelled"
			if testCase.decisionErr == nil && terminal && (updated.LastError != "" || updated.NextRunAt != "") {
				t.Fatalf("terminal execution=%+v", updated)
			}
		})
	}
}
