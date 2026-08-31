package workflow

import (
	"context"
	"errors"
	"testing"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type workflowAgentTaskStateStub struct {
	commit transactionmodel.WorkflowStateCommit
	err    error
}

func (*workflowAgentTaskStateStub) CommitWorkflowDecision(context.Context, transactionmodel.WorkflowDecisionCommit) (bool, error) {
	return false, nil
}

func (s *workflowAgentTaskStateStub) CommitWorkflowState(_ context.Context, commit transactionmodel.WorkflowStateCommit) error {
	s.commit = commit
	return s.err
}

func TestWorkflowAgentTaskNodeStartsCapabilityAndCommitsWaitingCorrelation(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	state := &workflowAgentTaskStateStub{}
	processes := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{}}
	prepared := WorkflowAgentTaskPreparation{}
	runtime := NewWorkflowProcessRuntime(WorkflowDependencies{
		Processes: processes, Decisions: state,
		StartAgentTask: func(_ context.Context, request WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			prepared = request
			return agentsdk.TaskResult{ExternalRunID: request.WorkspaceID + "/" + request.RunID, Status: agentsdk.ProviderRunAccepted}, nil
		},
	})
	process := workflowmodel.WorkflowProcessInstance{ID: "process-1", WorkspaceID: principal.WorkspaceID, DefinitionHash: "definition-hash", Variables: map[string]any{"record_id": "record-1"}}
	node := definitionmodel.WorkflowGraphNode{ID: "agent", Type: "agent_task", Name: "Review", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, Input: map[string]any{"record_id": "{{record_id}}"}, OutputVariable: "review", ExecutionMode: "async"}}}
	outcome, waiting, err := runtime.ProcessEngine().executeNode(t.Context(), &process, node, principal)
	if err != nil || !waiting || outcome != "waiting" {
		t.Fatalf("outcome=%q waiting=%v err=%v", outcome, waiting, err)
	}
	if prepared.NodeID != "agent" || prepared.Iteration != 1 || prepared.DefinitionSnapshotHash != "definition-hash" || len(state.commit.InsertNodes) != 1 || len(state.commit.Events) != 1 {
		t.Fatalf("prepared=%#v commit=%#v", prepared, state.commit)
	}
	if state.commit.InsertNodes[0].Status != "waiting" || state.commit.InsertNodes[0].Input["agent_task_run_id"] != prepared.RunID {
		t.Fatalf("prepared=%#v node=%#v", prepared, state.commit.InsertNodes[0])
	}
	nodeInstanceID, runID := workflowAgentTaskCorrelation(process.ID, node.ID, 1)
	if prepared.NodeInstanceID != nodeInstanceID || prepared.RunID != runID || state.commit.InsertNodes[0].ID != nodeInstanceID {
		t.Fatalf("durable correlation prepared=%#v node=%#v", prepared, state.commit.InsertNodes[0])
	}
	otherNodeInstanceID, otherRunID := workflowAgentTaskCorrelation(process.ID, node.ID, 2)
	if otherNodeInstanceID == nodeInstanceID || otherRunID == runID {
		t.Fatal("Agent task correlation did not advance with node iteration")
	}
	if process.Variables["agent_task:agent:output_variable"] != "review" {
		t.Fatalf("output binding=%#v", process.Variables)
	}
}

func TestAgentCapabilityCompletionAtomicallyUpdatesNodeProcessAndResumeIntent(t *testing.T) {
	principal := workflowProcessQueryPrincipal()
	state := &workflowAgentTaskStateStub{}
	graph := &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
		{ID: "agent", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "customer.review", TaskVersion: "1.0.0", OutputVariable: "review", AllowedOutcomes: []string{"success", "error"}}}},
		{ID: "done", Type: "condition"},
	}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "agent", Target: "done", Branch: "success"}}}
	process := workflowmodel.WorkflowProcessInstance{ID: "process-1", WorkspaceID: principal.WorkspaceID, WorkflowKey: "flow", WorkflowName: "Flow", DefinitionHash: "hash", DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "flow", Graph: graph}, Status: "waiting", CurrentNodeIDs: []string{"agent"}, Variables: map[string]any{}, CreatedAt: "created"}
	processes := &workflowExecutionProcessStub{processes: map[string]workflowmodel.WorkflowProcessInstance{process.ID: process}, nodes: map[string][]workflowmodel.WorkflowNodeInstance{process.ID: {{ID: "node-instance", ProcessID: process.ID, NodeID: "agent", NodeType: "agent_task", Iteration: 1, Status: "waiting", Input: map[string]any{"agent_task_run_id": "run-1"}}}}}
	service := NewWorkflowApplicationService(WorkflowDependencies{Processes: processes, Decisions: state})
	completion := WorkflowAgentTaskCompletion{TaskRunID: "run-1", WorkspaceID: principal.WorkspaceID, ProcessID: process.ID, NodeInstanceID: "node-instance", TaskKey: "customer.review", TaskVersion: "1.0.0", Status: "succeeded", Outcome: "success", Output: map[string]any{"score": 90}, Identity: agentsdk.ExecutionIdentity{Mode: agentsdk.AgentTaskIdentityInherit, Execution: agentsdk.PrincipalReference{UserID: principal.UserID, RoleKey: principal.RoleKey}}, Evidence: agentmodel.AgentTaskExecutionEvidence{AgentKey: "customer-agent", ToolInvocationRefs: []string{"tool-1"}, AuditRefs: []string{"audit-1"}}}
	mismatched := completion
	mismatched.TaskRunID = "other-run"
	if err := service.CompleteAgentTask(t.Context(), mismatched); apperror.CodeOf(err) != "backend.workflow.agent_task_correlation_mismatch" {
		t.Fatalf("mismatched completion error=%v", err)
	}
	state.err = errors.New("commit failed")
	if err := service.CompleteAgentTask(t.Context(), completion); err == nil {
		t.Fatal("failed continuation commit was accepted")
	}
	select {
	case locator := <-WorkflowContinuationWakeups(service):
		t.Fatalf("failed commit emitted wakeup=%#v", locator)
	default:
	}
	state.err = nil
	processes.nodes[process.ID][0].Status = "waiting"
	processes.nodes[process.ID][0].CompletedAt = ""
	if err := service.CompleteAgentTask(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	select {
	case locator := <-WorkflowContinuationWakeups(service):
		if locator.WorkspaceID != completion.WorkspaceID || locator.ExecutionID != "agent_task_resume_"+completion.TaskRunID {
			t.Fatalf("continuation wakeup=%#v", locator)
		}
	default:
		t.Fatal("committed Agent continuation did not wake the worker")
	}
	if len(state.commit.UpdateNodes) != 1 || len(state.commit.InsertExecutions) != 1 || state.commit.Process == nil {
		t.Fatalf("terminal commit=%#v", state.commit)
	}
	if state.commit.UpdateNodes[0].Status != "success" {
		t.Fatalf("node=%#v", state.commit)
	}
	resume := state.commit.InsertExecutions[0]
	if resume.IdempotencyKey != "agent-task-resume:run-1" || resume.Result["resume_explicit"] != true || len(workflowNodeIDsFromAny(resume.Result["resume_node_ids"])) != 1 {
		t.Fatalf("resume=%#v", resume)
	}
	if resume.Result["execution_user_id"] != principal.UserID || resume.Result["execution_role_key"] != principal.RoleKey {
		t.Fatalf("resume execution identity=%#v", resume.Result)
	}
	if state.commit.Process.Variables["review"].(map[string]any)["score"] != 90 {
		t.Fatalf("variables=%#v", state.commit.Process.Variables)
	}
	metadata := state.commit.Events[0].Metadata
	if metadata["task_key"] != completion.TaskKey || metadata["task_version"] != completion.TaskVersion || metadata["agent_key"] != "customer-agent" || metadata["execution_user_id"] != principal.UserID || metadata["outcome"] != "success" {
		t.Fatalf("history metadata=%#v", metadata)
	}
	if err := service.CompleteAgentTask(t.Context(), completion); err != nil {
		t.Fatalf("idempotent completion=%v", err)
	}
	select {
	case locator := <-WorkflowContinuationWakeups(service):
		t.Fatalf("idempotent completion emitted duplicate wakeup=%#v", locator)
	default:
	}
}

func TestAgentCapabilityStatusesMapToDeclaredWorkflowOutcomes(t *testing.T) {
	for _, test := range []struct {
		status  string
		outcome string
		want    string
	}{
		{"succeeded", "", "success"},
		{"manual_review", "", "manual_review"},
		{"rejected", "", "rejected"},
		{"no_result", "", "no_result"},
		{"failed", "cancelled", "error"},
		{"cancelled", "", "error"},
	} {
		if got := workflowAgentCapabilityOutcome(test.status, test.outcome); got != test.want {
			t.Errorf("status=%s outcome=%s got=%s want=%s", test.status, test.outcome, got, test.want)
		}
	}
}

func TestAgentTaskContinuationReauthorizesDurableExecutionIdentity(t *testing.T) {
	worker := workflowExecutionPrincipal()
	worker.UserID, worker.RoleKey, worker.RequestID, worker.CorrelationID, worker.CausationID = "system-worker", "system", "request", "correlation", "causation"
	execution := workflowmodel.WorkflowExecution{Result: map[string]any{"agent_task_run_id": "run-1", "execution_user_id": "service-user", "execution_role_key": "agent-service"}}
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-1"}
	resolved := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "service-user"}}, accessfixture.Bundle{Key: "agent-service"})
	resolver := &workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(resolved)}
	service := NewWorkflowApplicationService(WorkflowDependencies{Principals: resolver})

	principal, err := service.agentTaskContinuationPrincipal(t.Context(), execution, process, worker)
	if err != nil {
		t.Fatal(err)
	}
	if resolver.request.SubjectID != "service-user" || resolver.request.RoleKey != "agent-service" || principal.UserID == worker.UserID || principal.RequestID != worker.RequestID || principal.CorrelationID != worker.CorrelationID || principal.CausationID != worker.CausationID {
		t.Fatalf("resolution request=%#v principal=%#v", resolver.request, principal)
	}
}

func TestAgentTaskContinuationPrincipalFailsClosed(t *testing.T) {
	worker := workflowExecutionPrincipal()
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-1"}
	validResult := map[string]any{"agent_task_run_id": "run-1", "execution_user_id": "service-user", "execution_role_key": "agent-service"}
	tests := []struct {
		name       string
		principals identitysdk.PrincipalResolver
		result     map[string]any
		code       string
	}{
		{name: "missing resolver", result: validResult, code: "backend.workflow.execution_principal_unavailable"},
		{name: "missing durable identity", result: map[string]any{"agent_task_run_id": "run-1"}, code: "backend.workflow.execution_principal_revoked"},
		{name: "resolver failure", principals: &workflowPrincipalResolverTestStub{err: errors.New("directory unavailable")}, result: validResult, code: ""},
		{name: "unknown principal", principals: &workflowPrincipalResolverTestStub{}, result: validResult, code: "backend.workflow.execution_principal_revoked"},
		{name: "wrong workspace", principals: &workflowPrincipalResolverTestStub{resolution: workflowPrincipalResolution(accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-2", UserID: "service-user"}}, accessfixture.Bundle{Key: "agent-service"}))}, result: validResult, code: "backend.workflow.execution_principal_revoked"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewWorkflowApplicationService(WorkflowDependencies{Principals: test.principals})
			_, err := service.agentTaskContinuationPrincipal(t.Context(), workflowmodel.WorkflowExecution{Result: test.result}, process, worker)
			if err == nil || (test.code != "" && apperror.CodeOf(err) != test.code) {
				t.Fatalf("code=%q err=%v", apperror.CodeOf(err), err)
			}
		})
	}
	inherited, err := NewWorkflowApplicationService(WorkflowDependencies{}).agentTaskContinuationPrincipal(t.Context(), workflowmodel.WorkflowExecution{Result: map[string]any{}}, process, worker)
	if err != nil || inherited.UserID != worker.UserID {
		t.Fatalf("inherited=%#v err=%v", inherited, err)
	}
}

func TestWorkflowSimulationShowsAgentTaskWithoutModelTaskOrSideEffects(t *testing.T) {
	prepared := false
	engine := NewWorkflowProcessRuntime(WorkflowDependencies{
		StartAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			prepared = true
			return agentsdk.TaskResult{Status: agentsdk.ProviderRunAccepted}, nil
		},
		ActionExists: func(context.Context, string) bool { return true },
	}).ProcessEngine()
	graph := &definitionmodel.WorkflowGraphSchema{
		Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger"},
			{ID: "agent", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, Input: map[string]any{}, OutputVariable: "review", ExecutionMode: "async", TimeoutSeconds: 30, Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 2}, OnError: "error_branch", AllowedOutcomes: []string{"success", "error"}}}},
			{ID: "done", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "customer.finish"}}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "agent"}, {Source: "agent", Target: "done", Branch: "success"}, {Source: "agent", Target: "done", Branch: "error"}},
	}
	nodes, err := engine.Simulate(t.Context(), definitionmodel.WorkflowSchema{Key: "flow", Graph: graph}, map[string]any{"record_id": "one"}, workflowProcessQueryPrincipal())
	if err != nil || prepared || len(nodes) != 3 {
		t.Fatalf("nodes=%#v prepared=%v err=%v", nodes, prepared, err)
	}
	var preview workflowmodel.WorkflowSimulationNode
	for _, node := range nodes {
		if node.NodeID == "agent" {
			preview = node
		}
	}
	if preview.Outcome != "waiting" || preview.Details["task_key"] != "customer.review" || preview.Details["model_invoked"] != false || preview.Details["durable_task_created"] != false || preview.Details["business_side_effects"] != false {
		t.Fatalf("preview=%#v", preview)
	}
}
