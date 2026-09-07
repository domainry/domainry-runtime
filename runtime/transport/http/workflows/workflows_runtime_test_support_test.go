package workflows

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/idempotency"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowHTTPProcessStore struct {
	workflowcontract.WorkflowProcessStore
	processes map[string]workflowmodel.WorkflowProcessInstance
	nodes     map[string][]workflowmodel.WorkflowNodeInstance
	tasks     map[string]workflowmodel.WorkflowTask
	events    map[string][]workflowmodel.WorkflowProcessEvent
	err       error
}

func (s *workflowHTTPProcessStore) InsertProcess(_ context.Context, _ string, process workflowmodel.WorkflowProcessInstance) error {
	if s.err == nil {
		s.processes[process.ID] = process
	}
	return s.err
}
func (s *workflowHTTPProcessStore) UpdateProcess(_ context.Context, _ string, process workflowmodel.WorkflowProcessInstance) error {
	if s.err == nil {
		s.processes[process.ID] = process
	}
	return s.err
}
func (s *workflowHTTPProcessStore) GetProcess(_ context.Context, _, id string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	if s.err != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, s.err
	}
	process, found := s.processes[strings.TrimSpace(id)]
	return process, found, nil
}
func (s *workflowHTTPProcessStore) ListProcesses(_ context.Context, _ string, filter workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	if s.err != nil {
		return nil, s.err
	}
	processes := []workflowmodel.WorkflowProcessInstance{}
	for _, process := range s.processes {
		if filter.WorkflowKey != "" && process.WorkflowKey != filter.WorkflowKey || filter.ObjectKey != "" && process.ObjectKey != filter.ObjectKey || filter.RecordID != "" && process.RecordID != filter.RecordID || filter.Status != "" && process.Status != filter.Status || filter.InitiatorID != "" && process.InitiatorID != filter.InitiatorID {
			continue
		}
		processes = append(processes, process)
	}
	return processes, nil
}
func (s *workflowHTTPProcessStore) InsertNode(_ context.Context, _ string, node workflowmodel.WorkflowNodeInstance) error {
	if s.err == nil {
		s.nodes[node.ProcessID] = append(s.nodes[node.ProcessID], node)
	}
	return s.err
}
func (s *workflowHTTPProcessStore) UpdateNode(_ context.Context, _ string, node workflowmodel.WorkflowNodeInstance) error {
	if s.err != nil {
		return s.err
	}
	for index := range s.nodes[node.ProcessID] {
		if s.nodes[node.ProcessID][index].ID == node.ID {
			s.nodes[node.ProcessID][index] = node
			return nil
		}
	}
	return nil
}
func (s *workflowHTTPProcessStore) ListNodes(_ context.Context, _, processID string) ([]workflowmodel.WorkflowNodeInstance, error) {
	return append([]workflowmodel.WorkflowNodeInstance(nil), s.nodes[processID]...), s.err
}
func (s *workflowHTTPProcessStore) InsertTask(_ context.Context, _ string, task workflowmodel.WorkflowTask) error {
	if s.err == nil {
		s.tasks[task.ID] = task
	}
	return s.err
}
func (s *workflowHTTPProcessStore) UpdateTask(_ context.Context, _ string, task workflowmodel.WorkflowTask) error {
	if s.err == nil {
		s.tasks[task.ID] = task
	}
	return s.err
}
func (s *workflowHTTPProcessStore) GetTask(_ context.Context, _, id string) (workflowmodel.WorkflowTask, bool, error) {
	if s.err != nil {
		return workflowmodel.WorkflowTask{}, false, s.err
	}
	task, found := s.tasks[strings.TrimSpace(id)]
	return task, found, nil
}
func (s *workflowHTTPProcessStore) ListTasks(_ context.Context, _, processID, assigneeID, status string, limit int) ([]workflowmodel.WorkflowTask, error) {
	if s.err != nil {
		return nil, s.err
	}
	tasks := []workflowmodel.WorkflowTask{}
	for _, task := range s.tasks {
		if processID != "" && task.ProcessID != processID || assigneeID != "" && task.AssigneeUserID != assigneeID || status != "" && status != "open" && task.Status != status || status == "open" && task.Status != "open" && task.Status != "pending" {
			continue
		}
		tasks = append(tasks, task)
		if limit > 0 && len(tasks) >= limit {
			break
		}
	}
	return tasks, nil
}
func (s *workflowHTTPProcessStore) InsertEvent(_ context.Context, _ string, event workflowmodel.WorkflowProcessEvent) error {
	if s.err == nil {
		s.events[event.ProcessID] = append(s.events[event.ProcessID], event)
	}
	return s.err
}
func (s *workflowHTTPProcessStore) ListEvents(_ context.Context, _, processID string, _ int) ([]workflowmodel.WorkflowProcessEvent, error) {
	return append([]workflowmodel.WorkflowProcessEvent(nil), s.events[processID]...), s.err
}

func (s *workflowHTTPProcessStore) CommitWorkflowDecision(_ context.Context, commit transactionmodel.WorkflowDecisionCommit) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	current, found := s.tasks[commit.DecidedTask.ID]
	if !found || current.Status != commit.ExpectedTaskStatus || current.AssigneeUserID != commit.ExpectedAssigneeID {
		return false, nil
	}
	s.tasks[commit.DecidedTask.ID] = commit.DecidedTask
	for _, task := range commit.UpdateTasks {
		s.tasks[task.ID] = task
	}
	if commit.Process != nil {
		s.processes[commit.Process.ID] = *commit.Process
	}
	for _, event := range commit.Events {
		s.events[event.ProcessID] = append(s.events[event.ProcessID], event)
	}
	return true, nil
}

func (s *workflowHTTPProcessStore) CommitWorkflowState(_ context.Context, commit transactionmodel.WorkflowStateCommit) error {
	if s.err != nil {
		return s.err
	}
	if commit.Process != nil {
		s.processes[commit.Process.ID] = *commit.Process
	}
	for _, node := range commit.UpdateNodes {
		_ = s.UpdateNode(context.Background(), commit.WorkspaceID, node)
	}
	for _, task := range commit.UpdateTasks {
		s.tasks[task.ID] = task
	}
	for _, event := range commit.Events {
		s.events[event.ProcessID] = append(s.events[event.ProcessID], event)
	}
	return nil
}

type workflowHTTPWorkerStore struct {
	workflowcontract.WorkflowWorkerStore
	processes     *workflowHTTPProcessStore
	executions    map[string]workflowmodel.WorkflowExecution
	claimRequests []workflowmodel.WorkflowExecutionClaimRequest
	err           error
}

func (s *workflowHTTPWorkerStore) InsertExecution(_ context.Context, _ string, execution workflowmodel.WorkflowExecution) error {
	if s.err == nil {
		s.executions[execution.ID] = execution
	}
	return s.err
}
func (s *workflowHTTPWorkerStore) TryBeginExecution(_ context.Context, request workflowmodel.WorkflowExecutionClaimRequest) (workflowmodel.WorkflowExecutionClaimResult, error) {
	s.claimRequests = append(s.claimRequests, request)
	receipt := request.Receipt
	receipt.ID = "workflow-http-receipt"
	receipt.RequestFingerprint = request.RequestFingerprint
	receipt.LeaseOwner = request.LeaseOwner
	receipt.FencingToken = 1
	return workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: receipt}, s.err
}
func (s *workflowHTTPWorkerStore) CompleteExecutionReceipt(context.Context, workflowmodel.WorkflowExecutionReceiptCompletion) error {
	return s.err
}
func (s *workflowHTTPWorkerStore) GetExecution(_ context.Context, _, id string) (workflowmodel.WorkflowExecution, bool, error) {
	if s.err != nil {
		return workflowmodel.WorkflowExecution{}, false, s.err
	}
	execution, found := s.executions[id]
	return execution, found, nil
}
func (s *workflowHTTPWorkerStore) ListExecutions(_ context.Context, _ string, limit int) ([]workflowmodel.WorkflowExecution, error) {
	if s.err != nil {
		return nil, s.err
	}
	executions := []workflowmodel.WorkflowExecution{}
	for _, execution := range s.executions {
		executions = append(executions, execution)
		if limit > 0 && len(executions) >= limit {
			break
		}
	}
	return executions, nil
}
func (s *workflowHTTPWorkerStore) UpdateExecution(_ context.Context, _ string, execution workflowmodel.WorkflowExecution) error {
	if s.err == nil {
		s.executions[execution.ID] = execution
	}
	return s.err
}
func (s *workflowHTTPWorkerStore) UpdateExecutionWhere(_ context.Context, _ string, execution workflowmodel.WorkflowExecution, _ map[string]any) (bool, error) {
	if s.err == nil {
		s.executions[execution.ID] = execution
	}
	return s.err == nil, s.err
}
func (s *workflowHTTPWorkerStore) ListTasks(ctx context.Context, workspaceID, processID, assigneeID, status string, limit int) ([]workflowmodel.WorkflowTask, error) {
	return s.processes.ListTasks(ctx, workspaceID, processID, assigneeID, status, limit)
}
func (s *workflowHTTPWorkerStore) GetProcess(ctx context.Context, workspaceID, processID string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	return s.processes.GetProcess(ctx, workspaceID, processID)
}
func (s *workflowHTTPWorkerStore) ListProcessEvents(ctx context.Context, workspaceID, processID string, limit int) ([]workflowmodel.WorkflowProcessEvent, error) {
	return s.processes.ListEvents(ctx, workspaceID, processID, limit)
}
func (s *workflowHTTPWorkerStore) UpdateTask(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask) error {
	return s.processes.UpdateTask(ctx, workspaceID, task)
}
func (s *workflowHTTPWorkerStore) InsertProcessEvent(ctx context.Context, workspaceID string, event workflowmodel.WorkflowProcessEvent) error {
	return s.processes.InsertEvent(ctx, workspaceID, event)
}

type workflowHTTPScheduler struct {
	result workflowmodel.WorkflowProcessResult
	err    error
}

func newWorkflowHTTPRuntimeFixture() (*WorkflowsHandler, *workflowHTTPProcessStore, *workflowHTTPWorkerStore, *workflowHTTPScheduler, *workflowHTTPResponse) {
	definitions := &workflowHTTPDefinitionStore{definitions: map[string]workflowmodel.WorkflowDefinition{}, versions: map[string]workflowmodel.WorkflowDefinitionVersion{}}
	processes := &workflowHTTPProcessStore{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}, tasks: map[string]workflowmodel.WorkflowTask{}, events: map[string][]workflowmodel.WorkflowProcessEvent{}}
	workers := &workflowHTTPWorkerStore{processes: processes, executions: map[string]workflowmodel.WorkflowExecution{}}
	scheduler := &workflowHTTPScheduler{}
	registry := &workflowHTTPRegistry{items: map[string]definitionmodel.WorkflowSchema{"order.approve": workflowHTTPSchema("order.approve", "Order approve")}}
	service := workflowapplication.NewWorkflowApplicationService(workflowapplication.WorkflowDependencies{
		Definitions: definitions, Processes: processes, Workers: workers, Decisions: processes,
		WorkflowRegistry: registry, Schema: workflowHTTPSchemaProvider{},
		ObjectMap: func(context.Context) map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{}
		},
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any) {
		},
		AuditMetadata: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		},
	})
	response := &workflowHTTPResponse{}
	principal := workflowHTTPAdminPrincipal()
	handler := NewWorkflowsHandler(WorkflowsDependencies{
		Definitions: service, Processes: service, Executions: service,
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(_ http.ResponseWriter, status int, value any) { response.status, response.value = status, value },
		WriteError: func(_ http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			response.status, response.code = status, code
		},
		WriteServiceError: func(_ http.ResponseWriter, _ *http.Request, err error) { response.err = err },
		DecodeJSON: func(_ http.ResponseWriter, request *http.Request, value any) bool {
			if err := json.NewDecoder(request.Body).Decode(value); err != nil {
				response.status, response.err = http.StatusBadRequest, err
				return false
			}
			return true
		},
	})
	return handler, processes, workers, scheduler, response
}

func seedWorkflowHTTPProcess(processes *workflowHTTPProcessStore, status string) {
	graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
		{ID: "start", Type: "trigger"},
		{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "all"}}},
	}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start-approval", Source: "start", Target: "approval"}}}
	processes.processes["process-1"] = workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-1", ID: "process-1", WorkflowKey: "order.approve", WorkflowName: "Order approve", DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "order.approve", Name: "Order approve", Graph: graph}, InitiatorID: "admin-1", Status: status, CurrentNodeIDs: []string{"approval"}, Variables: map[string]any{}, Result: map[string]any{}}
	processes.nodes["process-1"] = []workflowmodel.WorkflowNodeInstance{{WorkspaceID: "workspace-1", ID: "node-1", ProcessID: "process-1", NodeID: "approval", NodeType: "approval", Status: "waiting"}}
	processes.tasks["task-1"] = workflowmodel.WorkflowTask{WorkspaceID: "workspace-1", ID: "task-1", ProcessID: "process-1", NodeID: "approval", AssigneeUserID: "admin-1", Status: "open"}
	processes.tasks["task-2"] = workflowmodel.WorkflowTask{WorkspaceID: "workspace-1", ID: "task-2", ProcessID: "process-1", NodeID: "approval", AssigneeUserID: "reviewer-2", Status: "open"}
}
