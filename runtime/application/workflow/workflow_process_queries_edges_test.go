package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowProcessStoreEdgeStub struct {
	workflowExecutionProcessStub
	listProcesses      []workflowmodel.WorkflowProcessInstance
	listProcessesErr   error
	listNodesErr       error
	listTasksErr       error
	listEventsErr      error
	getProcessErr      error
	getTaskValue       workflowmodel.WorkflowTask
	getTaskFound       bool
	getTaskErr         error
	decideTaskValue    workflowmodel.WorkflowTask
	decidedTask        bool
	decideTaskErr      error
	updateProcessErr   error
	updateProcessDelay time.Duration
	updateTaskErr      error
	insertProcessErr   error
	insertNodeErr      error
	updateNodeErr      error
	insertTaskErr      error
	insertEventErr     error
	events             []workflowmodel.WorkflowProcessEvent
	tasks              []workflowmodel.WorkflowTask
	insertedTasks      []workflowmodel.WorkflowTask
	updatedNodes       []workflowmodel.WorkflowNodeInstance
	updatedTasks       []workflowmodel.WorkflowTask
	listNodesCalls     int
	batchNodeCalls     int
}

func (s *workflowProcessStoreEdgeStub) InsertProcess(ctx context.Context, workspaceID string, process workflowmodel.WorkflowProcessInstance) error {
	if s.insertProcessErr != nil {
		return s.insertProcessErr
	}
	return s.workflowExecutionProcessStub.InsertProcess(ctx, workspaceID, process)
}

func (s *workflowProcessStoreEdgeStub) InsertNode(ctx context.Context, workspaceID string, node workflowmodel.WorkflowNodeInstance) error {
	if s.insertNodeErr != nil {
		return s.insertNodeErr
	}
	return s.workflowExecutionProcessStub.InsertNode(ctx, workspaceID, node)
}

func (s *workflowProcessStoreEdgeStub) UpdateNode(_ context.Context, _ string, node workflowmodel.WorkflowNodeInstance) error {
	s.updatedNodes = append(s.updatedNodes, node)
	return s.updateNodeErr
}

func (s *workflowProcessStoreEdgeStub) InsertTask(_ context.Context, _ string, task workflowmodel.WorkflowTask) error {
	s.insertedTasks = append(s.insertedTasks, task)
	return s.insertTaskErr
}

func (s *workflowProcessStoreEdgeStub) InsertEvent(_ context.Context, _ string, event workflowmodel.WorkflowProcessEvent) error {
	s.events = append(s.events, event)
	return s.insertEventErr
}

func (s *workflowProcessStoreEdgeStub) ListProcesses(_ context.Context, _ string, filter workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	if s.listProcessesErr != nil {
		return nil, s.listProcessesErr
	}
	out := []workflowmodel.WorkflowProcessInstance{}
	for _, process := range s.listProcesses {
		if filter.VisibleToUserID != "" && process.InitiatorID != filter.VisibleToUserID && !workflowTasksContainAssignee(s.tasks, process.ID, filter.VisibleToUserID) {
			continue
		}
		if filter.ApproverID != "" {
			if s.listTasksErr != nil {
				return nil, s.listTasksErr
			}
			if !workflowTasksContainAssignee(s.tasks, process.ID, filter.ApproverID) {
				continue
			}
		}
		out = append(out, process)
	}
	return out, nil
}

func workflowTasksContainAssignee(tasks []workflowmodel.WorkflowTask, processID, userID string) bool {
	for _, task := range tasks {
		if task.AssigneeUserID == userID && (task.ProcessID == "" || task.ProcessID == processID) {
			return true
		}
	}
	return false
}

func (s *workflowProcessStoreEdgeStub) GetProcess(ctx context.Context, workspaceID, id string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	if s.getProcessErr != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, s.getProcessErr
	}
	return s.workflowExecutionProcessStub.GetProcess(ctx, workspaceID, id)
}

func (s *workflowProcessStoreEdgeStub) UpdateProcess(ctx context.Context, workspaceID string, process workflowmodel.WorkflowProcessInstance) error {
	if s.updateProcessDelay > 0 {
		time.Sleep(s.updateProcessDelay)
	}
	if s.updateProcessErr != nil {
		return s.updateProcessErr
	}
	return s.workflowExecutionProcessStub.UpdateProcess(ctx, workspaceID, process)
}

func (s *workflowProcessStoreEdgeStub) ListNodes(ctx context.Context, workspaceID, processID string) ([]workflowmodel.WorkflowNodeInstance, error) {
	s.listNodesCalls++
	if s.listNodesErr != nil {
		return nil, s.listNodesErr
	}
	return s.workflowExecutionProcessStub.ListNodes(ctx, workspaceID, processID)
}

func (s *workflowProcessStoreEdgeStub) ListNodesForProcesses(_ context.Context, _ string, processIDs []string) ([]workflowmodel.WorkflowNodeInstance, error) {
	s.batchNodeCalls++
	if s.listNodesErr != nil {
		return nil, s.listNodesErr
	}
	result := []workflowmodel.WorkflowNodeInstance{}
	for _, processID := range processIDs {
		result = append(result, s.nodes[processID]...)
	}
	return result, nil
}

func (s *workflowProcessStoreEdgeStub) ListTasks(context.Context, string, string, string, string, int) ([]workflowmodel.WorkflowTask, error) {
	return s.tasks, s.listTasksErr
}

func (s *workflowProcessStoreEdgeStub) GetTask(context.Context, string, string) (workflowmodel.WorkflowTask, bool, error) {
	return s.getTaskValue, s.getTaskFound, s.getTaskErr
}

func (s *workflowProcessStoreEdgeStub) DecideTask(context.Context, string, string, string, string, string, string) (workflowmodel.WorkflowTask, bool, error) {
	return s.decideTaskValue, s.decidedTask, s.decideTaskErr
}

func (s *workflowProcessStoreEdgeStub) UpdateTask(_ context.Context, _ string, task workflowmodel.WorkflowTask) error {
	s.updatedTasks = append(s.updatedTasks, task)
	return s.updateTaskErr
}

func (s *workflowProcessStoreEdgeStub) ListEvents(context.Context, string, string, int) ([]workflowmodel.WorkflowProcessEvent, error) {
	return s.events, s.listEventsErr
}

func workflowProcessQueryService(store *workflowProcessStoreEdgeStub, identity identitysdk.Projection) *WorkflowApplicationService {
	return NewWorkflowApplicationService(WorkflowDependencies{
		Processes: store, Identity: identity, Schema: workflowSchemaProviderEdgeStub{},
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any) {
		},
		AuditMetadata: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		},
	})
}

func workflowProcessQueryPrincipal() principalmodel.Principal {
	return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}
}

func TestWorkflowProcessSummaryAssigneeAndVisibilityOutcomes(t *testing.T) {
	process := workflowmodel.WorkflowProcessInstance{
		ID: "process", WorkspaceID: "workspace", InitiatorID: "initiator", Status: "waiting", UpdatedAt: time.Now().Add(-time.Minute).Format(time.RFC3339Nano),
		CurrentNodeIDs: []string{"named", "unnamed"}, Result: map[string]any{"retry_count": 2}, Variables: map[string]any{"approval_decision": " approved "},
		DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "named", Name: "Named"}, {ID: "unnamed"}, {ID: "failed", Name: "Failed"}}}},
	}
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{NodeID: "ignored", Status: "completed"}, {NodeID: "failed", Status: "failed"}, {NodeID: "configuration", Status: "configuration_error"}}}}}
	service := workflowProcessQueryService(store, nil)
	enriched := service.enrichWorkflowProcessSummary(t.Context(), process)
	if len(enriched.CurrentNodeNames) != 1 || enriched.WaitingSeconds <= 0 || enriched.WorkflowRetryCount != 2 || enriched.BusinessOutcome != "approved" || len(enriched.FailedNodeNames) != 0 {
		t.Fatalf("enriched=%+v", enriched)
	}
	process.UpdatedAt = "invalid"
	_ = service.enrichWorkflowProcessSummary(t.Context(), process)
	process.Status = "configuration_error"
	enriched = service.enrichWorkflowProcessSummary(t.Context(), process)
	if len(enriched.FailedNodeNames) != 2 {
		t.Fatalf("failed summary=%+v", enriched)
	}
	store.listNodesErr = errors.New("nodes")
	enriched = service.enrichWorkflowProcessSummary(t.Context(), process)
	if enriched.WaitingSeconds != 0 {
		t.Fatalf("invalid waiting=%+v", enriched)
	}
	store.listNodesErr = nil
	process.Status = "completed"
	_ = service.enrichWorkflowProcessSummary(t.Context(), process)
	process.Status = "failed"
	store.listNodesErr = errors.New("nodes")
	_ = service.enrichWorkflowProcessSummary(t.Context(), process)
	store.listNodesErr = nil

	tasks := []workflowmodel.WorkflowTask{{ID: "task", AssigneeUserID: "assignee"}}
	if actual := service.workflowTasksWithAssigneeNames(t.Context(), tasks); actual[0].AssigneeName != "" {
		t.Fatalf("nil identity tasks=%v", actual)
	}
	service.identity = workflowIdentityEdgeStub{user: identitysdk.User{Name: "Assignee"}, found: true}
	if actual := service.workflowTasksWithAssigneeNames(t.Context(), tasks); actual[0].AssigneeName != "Assignee" {
		t.Fatalf("named tasks=%v", actual)
	}
	service.identity = workflowIdentityEdgeStub{err: errors.New("identity")}
	_ = service.workflowTasksWithAssigneeNames(t.Context(), tasks)
	service.identity = workflowIdentityEdgeStub{}
	_ = service.workflowTasksWithAssigneeNames(t.Context(), tasks)

	principal := workflowProcessQueryPrincipal()
	initiated := process
	initiated.InitiatorID = principal.UserID
	if !service.workflowProcessVisible(t.Context(), initiated, principal) {
		t.Fatal("initiator hidden")
	}
	operator := workflowPrincipalWithPermissions(principal, "runtime.workflows.get_ops_workflow_process")
	if service.workflowProcessVisible(t.Context(), process, operator) {
		t.Fatal("route Action must not broaden business-process visibility")
	}
	store.tasks = []workflowmodel.WorkflowTask{{ID: "task"}}
	if !service.workflowProcessVisible(t.Context(), process, principal) {
		t.Fatal("approver hidden")
	}
	store.tasks = nil
	if service.workflowProcessVisible(t.Context(), process, principal) {
		t.Fatal("unrelated process visible")
	}
	store.listTasksErr = errors.New("tasks")
	if service.workflowProcessVisible(t.Context(), process, principal) {
		t.Fatal("failed task lookup visible")
	}
}

func TestWorkflowProcessListAndMyTasksAuthorizationFilterAndFailureOutcomes(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", InitiatorID: principal.UserID}
	store := &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}, listProcesses: []workflowmodel.WorkflowProcessInstance{process}, tasks: []workflowmodel.WorkflowTask{{ID: "task", AssigneeUserID: principal.UserID}}}
	service := workflowProcessQueryService(store, workflowIdentityEdgeStub{user: identitysdk.User{Name: "User"}, found: true})
	if _, err := service.WorkflowProcesses(t.Context(), principalmodel.Principal{}, workflowmodel.WorkflowProcessFilter{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("process authorization=%v", err)
	}
	items, err := service.WorkflowProcesses(t.Context(), principal, workflowmodel.WorkflowProcessFilter{WorkflowKey: " flow ", ObjectKey: " order ", RecordID: " record ", Status: " waiting ", InitiatorID: " user ", ApproverID: " user "})
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	store.tasks = nil
	items, err = service.WorkflowProcesses(t.Context(), principal, workflowmodel.WorkflowProcessFilter{ApproverID: "user"})
	if err != nil || len(items) != 0 {
		t.Fatalf("filtered items=%v err=%v", items, err)
	}
	store.listTasksErr = errors.New("tasks")
	if _, err := service.WorkflowProcesses(t.Context(), principal, workflowmodel.WorkflowProcessFilter{ApproverID: "user"}); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("approver error=%v", err)
	}
	store.listTasksErr = nil
	store.listProcessesErr = errors.New("processes")
	if _, err := service.WorkflowProcesses(t.Context(), principal, workflowmodel.WorkflowProcessFilter{}); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("list error=%v", err)
	}
	store.listProcessesErr = nil
	store.listProcesses = []workflowmodel.WorkflowProcessInstance{{ID: "hidden", WorkspaceID: "workspace", InitiatorID: "other"}}
	store.tasks = nil
	if items, err := service.WorkflowProcesses(t.Context(), principal, workflowmodel.WorkflowProcessFilter{}); err != nil || len(items) != 0 {
		t.Fatalf("hidden items=%v err=%v", items, err)
	}
	if _, err := service.MyWorkflowTasks(t.Context(), principalmodel.Principal{}, "", 10); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("task authorization=%v", err)
	}
	store.tasks = []workflowmodel.WorkflowTask{{ID: "task", AssigneeUserID: principal.UserID}}
	tasks, err := service.MyWorkflowTasks(t.Context(), principal, "", 10)
	if err != nil || len(tasks) != 1 || tasks[0].AssigneeName != "User" {
		t.Fatalf("tasks=%v err=%v", tasks, err)
	}
	if _, err := service.MyWorkflowTasks(t.Context(), principal, " closed ", 10); err != nil {
		t.Fatal(err)
	}
	store.listTasksErr = errors.New("tasks")
	if _, err := service.MyWorkflowTasks(t.Context(), principal, "open", 10); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("task list error=%v", err)
	}
}

func TestWorkflowProcessListLoadsFailedNodesInOneBatch(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	first := workflowmodel.WorkflowProcessInstance{ID: "first", WorkspaceID: "workspace", InitiatorID: principal.UserID, Status: "failed"}
	second := workflowmodel.WorkflowProcessInstance{ID: "second", WorkspaceID: "workspace", InitiatorID: principal.UserID, Status: "configuration_error"}
	store := &workflowProcessStoreEdgeStub{
		workflowExecutionProcessStub: workflowExecutionProcessStub{nodes: map[string][]workflowmodel.WorkflowNodeInstance{
			"first":  {{ProcessID: "first", NodeID: "one", Status: "failed"}},
			"second": {{ProcessID: "second", NodeID: "two", Status: "configuration_error"}},
		}},
		listProcesses: []workflowmodel.WorkflowProcessInstance{first, second},
	}
	items, err := workflowProcessQueryService(store, nil).WorkflowProcesses(t.Context(), principal, workflowmodel.WorkflowProcessFilter{})
	if err != nil || len(items) != 2 || len(items[0].FailedNodeNames) != 1 || len(items[1].FailedNodeNames) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if store.batchNodeCalls != 1 || store.listNodesCalls != 0 {
		t.Fatalf("batch calls=%d per-process calls=%d", store.batchNodeCalls, store.listNodesCalls)
	}
}

func TestWorkflowProcessDetailLookupVisibilityAndChildFailureOutcomes(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", InitiatorID: principal.UserID}
	base := func() *workflowProcessStoreEdgeStub {
		return &workflowProcessStoreEdgeStub{workflowExecutionProcessStub: workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{"process": process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{"process": {{ID: "node"}}}}, tasks: []workflowmodel.WorkflowTask{{ID: "task"}}, events: []workflowmodel.WorkflowProcessEvent{{ID: "event"}}}
	}
	if _, err := workflowProcessQueryService(base(), nil).WorkflowProcess(t.Context(), "process", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization=%v", err)
	}
	store := base()
	store.getProcessErr = errors.New("get")
	if _, err := workflowProcessQueryService(store, nil).WorkflowProcess(t.Context(), "process", principal); apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get error=%v", err)
	}
	store = base()
	delete(store.processes, "process")
	if _, err := workflowProcessQueryService(store, nil).WorkflowProcess(t.Context(), "process", principal); apperror.CodeOf(err) != "backend.workflow.process_not_found" {
		t.Fatalf("not found=%v", err)
	}
	store = base()
	store.processes["process"] = workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace", InitiatorID: "other"}
	store.tasks = nil
	if _, err := workflowProcessQueryService(store, nil).WorkflowProcess(t.Context(), "process", principal); apperror.CodeOf(err) != "backend.workflow.process_access_denied" {
		t.Fatalf("access error=%v", err)
	}
	for name, mutate := range map[string]func(*workflowProcessStoreEdgeStub){
		"nodes":  func(store *workflowProcessStoreEdgeStub) { store.listNodesErr = errors.New("nodes") },
		"tasks":  func(store *workflowProcessStoreEdgeStub) { store.listTasksErr = errors.New("tasks") },
		"events": func(store *workflowProcessStoreEdgeStub) { store.listEventsErr = errors.New("events") },
	} {
		t.Run(name, func(t *testing.T) {
			store := base()
			mutate(store)
			if _, err := workflowProcessQueryService(store, nil).WorkflowProcess(t.Context(), " process ", principal); apperror.CodeOf(err) != "backend.internal" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	detail, err := workflowProcessQueryService(base(), workflowIdentityEdgeStub{user: identitysdk.User{Name: "User"}, found: true}).WorkflowProcess(t.Context(), "process", principal)
	if err != nil || detail.Process.ID != "process" || len(detail.Nodes) != 1 || len(detail.Tasks) != 1 || len(detail.Events) != 1 {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
}

func (s *workflowProcessStoreEdgeStub) ClaimWorkflowTimer(ctx context.Context, workspaceID string, process workflowmodel.WorkflowProcessInstance, node workflowmodel.WorkflowNodeInstance, expectedUpdatedAt string) (bool, error) {
	current, ok := s.processes[process.ID]
	if !ok || current.Status != "waiting" || current.UpdatedAt != expectedUpdatedAt {
		return false, nil
	}
	if s.updateProcessErr != nil {
		return false, s.updateProcessErr
	}
	if err := s.UpdateNode(ctx, workspaceID, node); err != nil {
		return false, err
	}
	err := s.UpdateProcess(ctx, workspaceID, process)
	return err == nil, err
}
