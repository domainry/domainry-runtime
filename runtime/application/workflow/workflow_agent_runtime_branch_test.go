package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type decisionOnlyStore struct{}

func (decisionOnlyStore) CommitWorkflowDecision(context.Context, transactionmodel.WorkflowDecisionCommit) (bool, error) {
	return false, nil
}

type failingAgentProcessStore struct {
	*workflowExecutionProcessStub
	getErr, listErr error
}

func (s *failingAgentProcessStore) GetProcess(ctx context.Context, workspaceID, id string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	if s.getErr != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, s.getErr
	}
	return s.workflowExecutionProcessStub.GetProcess(ctx, workspaceID, id)
}
func (s *failingAgentProcessStore) ListNodes(ctx context.Context, workspaceID, processID string) ([]workflowmodel.WorkflowNodeInstance, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.workflowExecutionProcessStub.ListNodes(ctx, workspaceID, processID)
}

func TestWorkflowExecuteAgentTaskFailureBranches(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: principal.WorkspaceID, Variables: map[string]any{}}
	node := definitionmodel.WorkflowGraphNode{ID: "agent", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "task", TaskVersion: "1"}}}
	wantErr := errors.New("agent dispatch failure")
	processes := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	tests := []struct {
		name   string
		engine *WorkflowProcessEngine
		node   definitionmodel.WorkflowGraphNode
	}{
		{"nil engine", nil, node},
		{"nil runtime", &WorkflowProcessEngine{}, node},
		{"missing prepare", NewWorkflowProcessRuntime(WorkflowDependencies{}).ProcessEngine(), node},
		{"missing state", NewWorkflowProcessRuntime(WorkflowDependencies{Decisions: decisionOnlyStore{}, PrepareAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentmodel.AgentTaskRun, error) {
			return agentmodel.AgentTaskRun{}, nil
		}}).ProcessEngine(), node},
		{"missing contract", NewWorkflowProcessRuntime(WorkflowDependencies{Decisions: &workflowAgentTaskStateStub{}, PrepareAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentmodel.AgentTaskRun, error) {
			return agentmodel.AgentTaskRun{}, nil
		}}).ProcessEngine(), definitionmodel.WorkflowGraphNode{ID: "agent"}},
		{"missing agent contract", NewWorkflowProcessRuntime(WorkflowDependencies{Decisions: &workflowAgentTaskStateStub{}, PrepareAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentmodel.AgentTaskRun, error) {
			return agentmodel.AgentTaskRun{}, nil
		}}).ProcessEngine(), definitionmodel.WorkflowGraphNode{ID: "agent", Contract: &definitionmodel.WorkflowNodeContract{}}},
		{"prepare", NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processes, Decisions: &workflowAgentTaskStateStub{}, PrepareAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentmodel.AgentTaskRun, error) {
			return agentmodel.AgentTaskRun{}, wantErr
		}}).ProcessEngine(), node},
		{"marshal", NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processes, Decisions: &workflowAgentTaskStateStub{}, PrepareAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentmodel.AgentTaskRun, error) {
			return agentmodel.AgentTaskRun{Output: map[string]any{"bad": make(chan int)}}, nil
		}}).ProcessEngine(), node},
		{"commit", NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processes, Decisions: &workflowAgentTaskStateStub{err: wantErr}, PrepareAgentTask: func(_ context.Context, request WorkflowAgentTaskPreparation) (agentmodel.AgentTaskRun, error) {
			now := time.Now()
			return agentmodel.AgentTaskRun{ID: "run", WorkspaceID: request.WorkspaceID, ProcessID: request.ProcessID, TaskKey: "task", Status: agentmodel.AgentTaskRunPending, CreatedAt: now, UpdatedAt: now}, nil
		}}).ProcessEngine(), node},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var err error
			if test.engine == nil {
				_, _, err = (*WorkflowProcessEngine)(nil).executeAgentTaskNode(t.Context(), &process, test.node, principal)
			} else {
				_, _, err = test.engine.executeAgentTaskNode(t.Context(), &process, test.node, principal)
			}
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
	successState := &workflowAgentTaskStateStub{}
	successEngine := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processes, Decisions: successState, PrepareAgentTask: func(_ context.Context, request WorkflowAgentTaskPreparation) (agentmodel.AgentTaskRun, error) {
		now := time.Now().UTC()
		return agentmodel.AgentTaskRun{ID: "run", WorkspaceID: request.WorkspaceID, ProcessID: request.ProcessID, TaskKey: "task", Status: agentmodel.AgentTaskRunPending, CreatedAt: now, UpdatedAt: now}, nil
	}}).ProcessEngine()
	if status, waiting, err := successEngine.executeAgentTaskNode(t.Context(), &process, node, principal); err != nil || status != "waiting" || !waiting {
		t.Fatalf("status=%q waiting=%v err=%v", status, waiting, err)
	}
}

func terminalFixture() (agentmodel.AgentTaskRun, workflowmodel.WorkflowProcessInstance, workflowmodel.WorkflowNodeInstance) {
	contract := &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "task", TaskVersion: "1", AllowedOutcomes: []string{"success", "error"}}}
	graph := &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "agent", Type: "agent_task", Contract: contract}}}
	process := workflowmodel.WorkflowProcessInstance{ID: "process", WorkspaceID: "workspace-1", WorkflowKey: "flow", DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow", Graph: graph}, Variables: map[string]any{}}
	node := workflowmodel.WorkflowNodeInstance{ID: "node", ProcessID: process.ID, NodeID: "agent", Status: "waiting"}
	run := agentmodel.AgentTaskRun{ID: "run", WorkspaceID: process.WorkspaceID, ProcessID: process.ID, NodeInstanceID: node.ID, TaskKey: "task", Status: agentmodel.AgentTaskRunSucceeded, UpdatedAt: time.Now().UTC(), Identity: agentsdk.ExecutionIdentity{Execution: agentsdk.PrincipalReference{UserID: "user"}}}
	return run, process, node
}

func terminalService(process workflowmodel.WorkflowProcessInstance, nodes []workflowmodel.WorkflowNodeInstance, decisions any, getErr, listErr error) *WorkflowApplicationService {
	base := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{process.ID: process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{process.ID: nodes}}
	store := &failingAgentProcessStore{workflowExecutionProcessStub: base, getErr: getErr, listErr: listErr}
	deps := WorkflowDependencies{Processes: store}
	switch typed := decisions.(type) {
	case *workflowAgentTaskStateStub:
		deps.Decisions = typed
	case decisionOnlyStore:
		deps.Decisions = typed
	}
	return NewWorkflowApplicationService(deps)
}

func TestWorkflowCommitAgentTaskTerminalFailureBranches(t *testing.T) {
	run, process, node := terminalFixture()
	wantErr := errors.New("terminal failure")
	if err := (*WorkflowApplicationService)(nil).CommitAgentTaskTerminal(t.Context(), run, "worker", 1); err == nil {
		t.Fatal("nil service accepted")
	}
	if err := (&WorkflowApplicationService{}).CommitAgentTaskTerminal(t.Context(), run, "worker", 1); err == nil {
		t.Fatal("service without process engine accepted")
	}
	if err := NewWorkflowApplicationService(WorkflowDependencies{Decisions: &workflowAgentTaskStateStub{}}).CommitAgentTaskTerminal(t.Context(), run, "worker", 1); err == nil {
		t.Fatal("service without process repository accepted")
	}
	tests := []struct {
		name            string
		run             agentmodel.AgentTaskRun
		process         workflowmodel.WorkflowProcessInstance
		nodes           []workflowmodel.WorkflowNodeInstance
		decisions       any
		getErr, listErr error
	}{
		{"missing state", run, process, []workflowmodel.WorkflowNodeInstance{node}, decisionOnlyStore{}, nil, nil},
		{"nonterminal", withWorkflowTaskStatus(run, agentmodel.AgentTaskRunRunning), process, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"missing process id", withWorkflowProcessID(run, ""), process, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"missing node instance id", withWorkflowNodeInstanceID(run, ""), process, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"get error", run, process, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, wantErr, nil},
		{"get missing", run, workflowmodel.WorkflowProcessInstance{ID: "missing", WorkspaceID: process.WorkspaceID}, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"list error", run, process, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, wantErr},
		{"node not waiting", run, process, []workflowmodel.WorkflowNodeInstance{{ID: "other", Status: "waiting"}, withWorkflowNodeStatus(node, "success")}, &workflowAgentTaskStateStub{}, nil, nil},
		{"graph node missing", run, withWorkflowGraph(process, &definitionmodel.WorkflowGraphSchema{}), []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"contract missing", run, withWorkflowGraph(process, &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "agent"}}}), []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"agent contract missing", run, withWorkflowGraph(process, &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "agent", Contract: &definitionmodel.WorkflowNodeContract{}}}}), []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"outcome", withWorkflowOutcome(run, "manual_review"), process, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"marshal", withWorkflowOutput(run, map[string]any{"bad": make(chan int)}), process, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{}, nil, nil},
		{"commit", run, process, []workflowmodel.WorkflowNodeInstance{node}, &workflowAgentTaskStateStub{err: wantErr}, nil, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := terminalService(test.process, test.nodes, test.decisions, test.getErr, test.listErr)
			if err := service.CommitAgentTaskTerminal(t.Context(), test.run, "worker", 1); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	if got := agentTaskWorkflowOutcome(agentmodel.AgentTaskRun{}); got != "error" {
		t.Fatalf("default outcome=%s", got)
	}
}

func withWorkflowTaskStatus(run agentmodel.AgentTaskRun, status agentmodel.AgentTaskRunStatus) agentmodel.AgentTaskRun {
	run.Status = status
	return run
}
func withWorkflowProcessID(run agentmodel.AgentTaskRun, id string) agentmodel.AgentTaskRun {
	run.ProcessID = id
	return run
}
func withWorkflowNodeInstanceID(run agentmodel.AgentTaskRun, id string) agentmodel.AgentTaskRun {
	run.NodeInstanceID = id
	return run
}
func withWorkflowNodeStatus(node workflowmodel.WorkflowNodeInstance, status string) workflowmodel.WorkflowNodeInstance {
	node.Status = status
	return node
}
func withWorkflowGraph(process workflowmodel.WorkflowProcessInstance, graph *definitionmodel.WorkflowGraphSchema) workflowmodel.WorkflowProcessInstance {
	process.DefinitionSnapshot.Graph = graph
	return process
}
func withWorkflowOutcome(run agentmodel.AgentTaskRun, outcome string) agentmodel.AgentTaskRun {
	run.Outcome = outcome
	return run
}
func withWorkflowOutput(run agentmodel.AgentTaskRun, output map[string]any) agentmodel.AgentTaskRun {
	run.Output = output
	return run
}

func TestWorkflowAgentReferenceAndContinuationFailureBranches(t *testing.T) {
	validator := NewWorkflowReferenceValidator(workflowSchemaProviderEdgeStub{}, func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{}
	}, nil)
	workflow := definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{{ID: "agent", Type: "agent_task"}}}}
	_ = validator.validateWorkflowReferences(t.Context(), workflow)
	if issues := validator.validateWorkflowAgentTaskReference(t.Context(), definitionmodel.WorkflowGraphNode{}); len(issues) != 0 {
		t.Fatalf("direct missing contract issues=%v", issues)
	}
	if issues := validator.validateWorkflowAgentTaskReference(t.Context(), definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{}}); len(issues) != 0 {
		t.Fatalf("missing agent contract issues=%v", issues)
	}
	referenceValidator := NewWorkflowReferenceValidator(workflowSchemaProviderEdgeStub{snapshot: WorkflowSchemaSnapshot{
		AgentTasks:             []agentsdk.AgentTaskDefinition{{Key: "disabled", Version: "1", Enabled: false}, {Key: "other", Version: "1", Enabled: true}, {Key: "task", Version: "1", Enabled: true, AllowedActions: []string{"missing"}}},
		AgentServicePrincipals: []agentsdk.AgentServicePrincipalBinding{{Key: "other", Enabled: true}, {Key: "principal", Enabled: true}},
	}}, nil, nil)
	for _, contract := range []definitionmodel.WorkflowAgentTaskNodeContract{
		{TaskKey: "task", TaskVersion: "1", Identity: definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, AllowedActions: []string{"missing"}},
		{TaskKey: "task", TaskVersion: "1", Identity: definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "principal"}},
	} {
		_ = referenceValidator.validateWorkflowAgentTaskReference(t.Context(), definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &contract}})
	}

	service := &WorkflowApplicationService{}
	workerPrincipal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace", UserID: "worker"}}, accessfixture.Bundle{Key: "worker"})
	if got, err := service.agentTaskContinuationPrincipal(t.Context(), workflowmodel.WorkflowExecution{Result: map[string]any{"agent_task_run_id": ""}}, workflowmodel.WorkflowProcessInstance{}, workerPrincipal); err != nil || got.UserID != "worker" {
		t.Fatalf("fallback=%+v err=%v", got, err)
	}
	for _, result := range []map[string]any{
		{"agent_task_run_id": "run", "execution_user_id": "", "execution_role_key": "role"},
		{"agent_task_run_id": "run", "execution_user_id": "user", "execution_role_key": ""},
		{"agent_task_run_id": "run", "execution_user_id": "user", "execution_role_key": nil},
	} {
		_, _ = service.agentTaskContinuationPrincipal(t.Context(), workflowmodel.WorkflowExecution{Result: result}, workflowmodel.WorkflowProcessInstance{}, workerPrincipal)
	}
	for _, resolved := range []principalmodel.Principal{accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "other"}}, accessfixture.Bundle{Key: "role"}), accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}}, accessfixture.Bundle{Key: "other"})} {
		service.principals = &workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(resolved)}
		_, _ = service.agentTaskContinuationPrincipal(t.Context(), workflowmodel.WorkflowExecution{Result: map[string]any{"agent_task_run_id": "run", "execution_user_id": "user", "execution_role_key": "role"}}, workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace"}, workerPrincipal)
	}

	now := time.Now().UTC()
	definition := workflowWorkerSchema("flow")
	continuation := workflowMutationProcess("running")
	continuation.WorkspaceID = "workspace-1"
	continuation.CurrentNodeIDs = []string{"start"}
	resume := workflowDueExecution(now, definition.Key)
	resume.WorkspaceID = "workspace-1"
	resume.Result = map[string]any{"resume_process_id": continuation.ID, "resume_node_ids": []string{"start"}, "agent_task_run_id": "run"}
	worker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{resume}, whereResults: []bool{true, true}, continuation: continuation, continuationFound: true}
	result, err := workflowRecordWorkerService(now, worker, definition).ProcessDueWorkflowExecutions(t.Context(), 1, workflowExecutionPrincipal())
	if err != nil || result.Processed != 1 || result.Executions[0].Status != "failed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	explicit := resume
	explicit.Result = map[string]any{"resume_process_id": continuation.ID, "resume_node_ids": []string{}, "resume_explicit": true, "agent_task_run_id": "run"}
	explicitWorker := &workflowRuntimeWorkerEdgeStub{workflowExecutionWorkerStub: workflowExecutionWorkerStub{executions: map[string]workflowmodel.WorkflowExecution{}}, list: []workflowmodel.WorkflowExecution{explicit}, whereResults: []bool{true, true}, continuation: continuation, continuationFound: true}
	_, _ = workflowRecordWorkerService(now, explicitWorker, definition).ProcessDueWorkflowExecutions(t.Context(), 1, workflowExecutionPrincipal())
}
