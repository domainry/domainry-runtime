package workflow

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowListWorkerEdgeStub struct {
	workflowExecutionWorkerStub
	list       []workflowmodel.WorkflowExecution
	listErr    error
	listLimits []int
}

func (s *workflowListWorkerEdgeStub) ListExecutions(_ context.Context, _ string, limit int) ([]workflowmodel.WorkflowExecution, error) {
	s.listLimits = append(s.listLimits, limit)
	return s.list, s.listErr
}

func TestWorkflowProjectionActionsUsesCurrentSchemaSnapshot(t *testing.T) {
	service := &WorkflowApplicationService{}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}
	if service.workflowProjectionActions(t.Context(), principal) != nil {
		t.Fatal("nil schema projected actions")
	}
	service.schema = workflowSchemaProviderEdgeStub{snapshot: WorkflowSchemaSnapshot{Actions: []definitionmodel.ActionSchema{{Key: "action"}}}}
	if actions := service.workflowProjectionActions(t.Context(), principal); len(actions) != 1 || actions[0].Key != "action" {
		t.Fatalf("actions=%v", actions)
	}
}

func TestWorkflowExecutionQueryFilterVisibilityLimitAndFailureMatrix(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}
	worker := &workflowListWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{
		{ID: "denied", ObjectKey: "order", RecordID: "denied"},
		{ID: "allowed", ObjectKey: "order", RecordID: "allowed"},
		{ID: "other", ObjectKey: "other", RecordID: "other"},
	}}
	reader := workflowRecordReaderEdgeStub{records: map[string]recordmodel.Record{"denied": {ID: "denied"}, "allowed": {ID: "allowed"}}}
	service := &WorkflowApplicationService{
		workerRepo: worker,
		objectForAction: func(_ context.Context, _ principalmodel.Principal, key, operation string) (definitionmodel.ObjectSchema, error) {
			if key == "error" {
				return definitionmodel.ObjectSchema{}, errors.New("object")
			}
			return definitionmodel.ObjectSchema{Key: key}, nil
		},
		executionVisibility: NewWorkflowExecutionVisibilityApplicationService(reader, func(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, record recordmodel.Record) bool {
			return record.ID == "allowed"
		}),
	}
	if _, err := service.WorkflowExecutions(t.Context(), principalmodel.Principal{}, "", "", 1); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	unknown := principal
	unknown.Known = false
	if _, err := service.WorkflowExecutions(t.Context(), unknown, "", "", 1); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown error=%v", err)
	}
	if _, err := service.WorkflowExecutions(t.Context(), principal, " error ", "", 1); err == nil || err.Error() != "object" {
		t.Fatalf("object error=%v", err)
	}
	items, err := service.WorkflowExecutions(t.Context(), principal, "", "", 0)
	if err != nil || len(items) != 3 || worker.listLimits[len(worker.listLimits)-1] != 100 {
		t.Fatalf("items=%v limits=%v err=%v", items, worker.listLimits, err)
	}
	if _, err := service.WorkflowExecutions(t.Context(), principal, "", "", 2000); err != nil || worker.listLimits[len(worker.listLimits)-1] != 1000 {
		t.Fatalf("limits=%v err=%v", worker.listLimits, err)
	}
	items, err = service.WorkflowExecutions(t.Context(), principal, " order ", "", 1)
	if err != nil || len(items) != 1 || items[0].ID != "allowed" || worker.listLimits[len(worker.listLimits)-1] != 500 {
		t.Fatalf("items=%v limits=%v err=%v", items, worker.listLimits, err)
	}
	if _, err := service.WorkflowExecutions(t.Context(), principal, "order", "", 500); err != nil || worker.listLimits[len(worker.listLimits)-1] != 500 {
		t.Fatalf("large filtered limits=%v err=%v", worker.listLimits, err)
	}
	items, err = service.WorkflowExecutions(t.Context(), principal, "", "allowed", 10)
	if err != nil || len(items) != 1 || items[0].ID != "allowed" {
		t.Fatalf("record items=%v err=%v", items, err)
	}
	worker.listErr = errors.New("list")
	if _, err := service.WorkflowExecutions(t.Context(), principal, "", "", 10); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("list error=%v", err)
	}
	worker.listErr = nil
	service.executionVisibility = NewWorkflowExecutionVisibilityApplicationService(workflowRecordReaderEdgeStub{errID: "denied"}, func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
		return true
	})
	if _, err := service.WorkflowExecutions(t.Context(), principal, "order", "", 10); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("visibility error=%v", err)
	}
}
