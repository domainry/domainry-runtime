package workflow

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

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

type workflowSchedulerEdgeStub struct {
	result       workflowmodel.WorkflowProcessResult
	err          error
	config       WorkflowSchedulerWorkerConfig
	processLimit int
	startConfig  WorkflowSchedulerWorkerConfig
	startEnabled bool
	done         chan struct{}
}

func (s *workflowSchedulerEdgeStub) ProcessDueJobs(_ context.Context, limit int, _ principalmodel.Principal, _ string) (workflowmodel.WorkflowProcessResult, error) {
	s.processLimit = limit
	return s.result, s.err
}

func (s *workflowSchedulerEdgeStub) WorkerConfig() WorkflowSchedulerWorkerConfig { return s.config }

func (s *workflowSchedulerEdgeStub) StartWorker(_ context.Context, config WorkflowSchedulerWorkerConfig, enabled bool) <-chan struct{} {
	s.startConfig, s.startEnabled = config, enabled
	if s.done == nil {
		s.done = make(chan struct{})
		close(s.done)
	}
	return s.done
}

func TestWorkflowApplicationListDefinitionsAuthorizationSortingAndProjection(t *testing.T) {
	registry := &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{"z": {Key: "z"}, "a": {Key: "a"}}}
	service := &WorkflowApplicationService{registry: registry}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if _, err := service.Workflows(cancelled, admin); err != context.Canceled {
		t.Fatalf("cancel error=%v", err)
	}
	if _, err := service.Workflows(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	unknown := admin
	unknown.Known = false
	if _, err := service.Workflows(t.Context(), unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown error=%v", err)
	}
	denied := admin
	denied = workflowPrincipalWithPermissions(denied)
	if _, err := service.Workflows(t.Context(), denied); apperror.CodeOf(err) != "backend.workflow.read_permission_required" {
		t.Fatalf("denied error=%v", err)
	}
	items, err := service.Workflows(t.Context(), admin)
	if err != nil || len(items) != 2 || items[0].Key != "a" {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if service.workflowProjectionActions(t.Context(), admin) != nil {
		t.Fatal("nil schema projected actions")
	}
	service.schema = workflowSchemaProviderEdgeStub{snapshot: WorkflowSchemaSnapshot{Actions: []definitionmodel.ActionSchema{{Key: "action"}}}}
	if actions := service.workflowProjectionActions(t.Context(), admin); len(actions) != 1 || actions[0].Key != "action" {
		t.Fatalf("actions=%v", actions)
	}
	limited := admin
	limited = workflowPrincipalWithPermissions(limited)
	if workflowProjectionAdvanced(limited) {
		t.Fatal("unexpected advanced permission")
	}
	limited = workflowPrincipalWithPermissions(limited, "workflow.advanced.configure")
	if !workflowProjectionAdvanced(limited) {
		t.Fatal("advanced permission denied")
	}
}

func TestWorkflowExecutionQueryFilterVisibilityLimitAndFailureMatrix(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"ops.workflow.read"}})
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
	denied := principal
	denied = workflowPrincipalWithPermissions(denied)
	if _, err := service.WorkflowExecutions(t.Context(), denied, "", "", 1); apperror.CodeOf(err) != "backend.workflow.read_permission_required" {
		t.Fatalf("denied error=%v", err)
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

func TestWorkflowSchedulerManualProcessAndWorkerConfiguration(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{"ops.workflow.process"}})
	scheduler := &workflowSchedulerEdgeStub{result: workflowmodel.WorkflowProcessResult{Processed: 2}, config: WorkflowSchedulerWorkerConfig{Enabled: true, PollInterval: time.Minute, BatchSize: 10}}
	registry := &workflowRegistryStub{items: map[string]definitionmodel.WorkflowSchema{"flow": {Key: "flow"}}}
	service := &WorkflowApplicationService{scheduler: scheduler, registry: registry}
	if _, err := service.ProcessWorkflowExecutions(t.Context(), 2, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	unknown := principal
	unknown.Known = false
	if _, err := service.ProcessWorkflowExecutions(t.Context(), 2, unknown); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown error=%v", err)
	}
	denied := principal
	denied = workflowPrincipalWithPermissions(denied)
	if _, err := service.ProcessWorkflowExecutions(t.Context(), 2, denied); apperror.CodeOf(err) != "backend.workflow.process_permission_required" {
		t.Fatalf("denied error=%v", err)
	}
	result, err := service.ProcessWorkflowExecutions(t.Context(), 2, principal)
	if err != nil || result.Processed != 2 || scheduler.processLimit != 2 {
		t.Fatalf("result=%+v limit=%d err=%v", result, scheduler.processLimit, err)
	}
	scheduler.err = errors.New("scheduler")
	if _, err := service.ProcessWorkflowExecutions(t.Context(), 2, principal); !errors.Is(err, scheduler.err) {
		t.Fatalf("scheduler error=%v", err)
	}
	scheduler.err = nil
	done := service.StartWorker(t.Context(), 2*time.Second, 20)
	<-done
	if !scheduler.startEnabled || !scheduler.startConfig.Enabled || scheduler.startConfig.PollInterval != 2*time.Second || scheduler.startConfig.BatchSize != 20 {
		t.Fatalf("config=%+v enabled=%v", scheduler.startConfig, scheduler.startEnabled)
	}
	scheduler.done = nil
	registry.items = map[string]definitionmodel.WorkflowSchema{}
	done = service.StartWorker(t.Context(), 0, 0)
	<-done
	if scheduler.startEnabled || !reflect.DeepEqual(scheduler.startConfig, scheduler.config) {
		t.Fatalf("default config=%+v enabled=%v", scheduler.startConfig, scheduler.startEnabled)
	}
}
