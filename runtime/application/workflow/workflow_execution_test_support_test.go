package workflow

import (
	"context"
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowExecutionWorkerStub struct {
	workflowcontract.WorkflowWorkerStore
	executions map[string]workflowmodel.WorkflowExecution
	getErr     error
	getErrors  map[string]error
	updateErr  error
	insertErr  error
	updated    []workflowmodel.WorkflowExecution
	inserted   []workflowmodel.WorkflowExecution
}

func (s *workflowExecutionWorkerStub) GetExecution(_ context.Context, _, id string) (workflowmodel.WorkflowExecution, bool, error) {
	if s.getErr != nil {
		return workflowmodel.WorkflowExecution{}, false, s.getErr
	}
	if err := s.getErrors[id]; err != nil {
		return workflowmodel.WorkflowExecution{}, false, err
	}
	execution, found := s.executions[id]
	return execution, found, nil
}
func (s *workflowExecutionWorkerStub) UpdateExecution(_ context.Context, _ string, execution workflowmodel.WorkflowExecution) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.executions[execution.ID] = execution
	s.updated = append(s.updated, execution)
	return nil
}
func (s *workflowExecutionWorkerStub) InsertExecution(_ context.Context, _ string, execution workflowmodel.WorkflowExecution) error {
	if s.insertErr != nil {
		return s.insertErr
	}
	s.executions[execution.ID] = execution
	s.inserted = append(s.inserted, execution)
	return nil
}
func (s *workflowExecutionWorkerStub) TryBeginExecution(context.Context, workflowmodel.WorkflowExecutionClaimRequest) (workflowmodel.WorkflowExecutionClaimResult, error) {
	return workflowmodel.WorkflowExecutionClaimResult{}, nil
}
func (s *workflowExecutionWorkerStub) CompleteExecutionReceipt(context.Context, workflowmodel.WorkflowExecutionReceiptCompletion) error {
	return nil
}

type workflowExecutionProcessStub struct {
	workflowcontract.WorkflowProcessStore
	processes map[string]workflowmodel.WorkflowProcessInstance
	nodes     map[string][]workflowmodel.WorkflowNodeInstance
}

func (s *workflowExecutionProcessStub) InsertProcess(_ context.Context, _ string, process workflowmodel.WorkflowProcessInstance) error {
	s.processes[process.ID] = process
	return nil
}
func (s *workflowExecutionProcessStub) UpdateProcess(_ context.Context, _ string, process workflowmodel.WorkflowProcessInstance) error {
	s.processes[process.ID] = process
	return nil
}
func (s *workflowExecutionProcessStub) GetProcess(_ context.Context, _, id string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	process, found := s.processes[id]
	return process, found, nil
}
func (s *workflowExecutionProcessStub) ListProcesses(context.Context, string, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	return nil, nil
}
func (s *workflowExecutionProcessStub) InsertNode(_ context.Context, _ string, node workflowmodel.WorkflowNodeInstance) error {
	s.nodes[node.ProcessID] = append(s.nodes[node.ProcessID], node)
	return nil
}
func (s *workflowExecutionProcessStub) UpdateNode(context.Context, string, workflowmodel.WorkflowNodeInstance) error {
	return nil
}
func (s *workflowExecutionProcessStub) ListNodes(_ context.Context, _, processID string) ([]workflowmodel.WorkflowNodeInstance, error) {
	return s.nodes[processID], nil
}
func (s *workflowExecutionProcessStub) InsertTask(context.Context, string, workflowmodel.WorkflowTask) error {
	return nil
}
func (s *workflowExecutionProcessStub) UpdateTask(context.Context, string, workflowmodel.WorkflowTask) error {
	return nil
}
func (s *workflowExecutionProcessStub) GetTask(context.Context, string, string) (workflowmodel.WorkflowTask, bool, error) {
	return workflowmodel.WorkflowTask{}, false, nil
}
func (s *workflowExecutionProcessStub) ListTasks(context.Context, string, string, string, string, int) ([]workflowmodel.WorkflowTask, error) {
	return nil, nil
}
func (s *workflowExecutionProcessStub) DecideTask(context.Context, string, string, string, string, string, string) (workflowmodel.WorkflowTask, bool, error) {
	return workflowmodel.WorkflowTask{}, false, nil
}
func (s *workflowExecutionProcessStub) InsertEvent(context.Context, string, workflowmodel.WorkflowProcessEvent) error {
	return nil
}
func (s *workflowExecutionProcessStub) ListEvents(context.Context, string, string, int) ([]workflowmodel.WorkflowProcessEvent, error) {
	return nil, nil
}

func workflowExecutionPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "operator"}}, accessfixture.Bundle{Key: "operator", Permissions: []string{
		"ops.workflow.run", "ops.workflow.simulate", "ops.workflow.retry", "ops.workflow.resolve",
	}},
	)
}

func workflowExecutionSchema() definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{
		Key: "order.approve", Name: "Order approve", Enabled: true,
		TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"},
		InputFields:     []definitionmodel.WorkflowInputField{{Key: "source", Type: "text"}, {Key: "order", Type: "text"}},
		Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "start", Type: "trigger", Name: "Start"},
		}},
	}
}

func newWorkflowExecutionService(worker *workflowExecutionWorkerStub, schemas ...definitionmodel.WorkflowSchema) *WorkflowApplicationService {
	items := map[string]definitionmodel.WorkflowSchema{}
	for _, schema := range schemas {
		items[schema.Key] = schema
	}
	processes := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	return NewWorkflowApplicationService(WorkflowDependencies{
		Workers: worker, Processes: processes, WorkflowRegistry: &workflowRegistryStub{items: items},
		ActionExists: func(context.Context, string) bool { return false },
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any) {
		},
		AuditMetadata: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		},
	})
}

var errWorkflowExecutionStore = errors.New("workflow execution store failed")
