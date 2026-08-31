package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type decisionOnlyStore struct{}

func (decisionOnlyStore) CommitWorkflowDecision(context.Context, transactionmodel.WorkflowDecisionCommit) (bool, error) {
	return false, nil
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
		{"missing state", NewWorkflowProcessRuntime(WorkflowDependencies{Decisions: decisionOnlyStore{}, StartAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			return agentsdk.TaskResult{Status: agentsdk.ProviderRunAccepted}, nil
		}}).ProcessEngine(), node},
		{"missing contract", NewWorkflowProcessRuntime(WorkflowDependencies{Decisions: &workflowAgentTaskStateStub{}, StartAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			return agentsdk.TaskResult{Status: agentsdk.ProviderRunAccepted}, nil
		}}).ProcessEngine(), definitionmodel.WorkflowGraphNode{ID: "agent"}},
		{"missing agent contract", NewWorkflowProcessRuntime(WorkflowDependencies{Decisions: &workflowAgentTaskStateStub{}, StartAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			return agentsdk.TaskResult{Status: agentsdk.ProviderRunAccepted}, nil
		}}).ProcessEngine(), definitionmodel.WorkflowGraphNode{ID: "agent", Contract: &definitionmodel.WorkflowNodeContract{}}},
		{"start", NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processes, Decisions: &workflowAgentTaskStateStub{}, StartAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			return agentsdk.TaskResult{}, wantErr
		}}).ProcessEngine(), node},
		{"invalid status", NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processes, Decisions: &workflowAgentTaskStateStub{}, StartAgentTask: func(context.Context, WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			return agentsdk.TaskResult{Status: agentsdk.ProviderRunCompleted}, nil
		}}).ProcessEngine(), node},
		{"commit", NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processes, Decisions: &workflowAgentTaskStateStub{err: wantErr}, StartAgentTask: func(_ context.Context, request WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			return agentsdk.TaskResult{ExternalRunID: request.WorkspaceID + "/" + request.RunID, Status: agentsdk.ProviderRunAccepted}, nil
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
	successEngine := NewWorkflowProcessRuntime(WorkflowDependencies{Processes: processes, Decisions: successState, StartAgentTask: func(_ context.Context, request WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
		return agentsdk.TaskResult{ExternalRunID: request.WorkspaceID + "/" + request.RunID, Status: agentsdk.ProviderRunAccepted}, nil
	}}).ProcessEngine()
	if status, waiting, err := successEngine.executeAgentTaskNode(t.Context(), &process, node, principal); err != nil || status != "waiting" || !waiting {
		t.Fatalf("status=%q waiting=%v err=%v", status, waiting, err)
	}
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
