package transport

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	agent "github.com/domainry/domainry-runtime/runtime/application/agent"
	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	agentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/agent"
)

func assembleAgentApplicationPorts(dependencies HTTPServerDependencies, principalDirectories ...identitysdk.PrincipalResolver) (*agent.AgentApplicationService, *agent.AgentProposalApplicationService) {
	if dependencies.Store == nil {
		return nil, nil
	}
	state := agent.NewAgentApplicationService(agentpersistence.NewAgentStateStore(dependencies.Store))
	var principals identitysdk.PrincipalResolver
	if len(principalDirectories) > 0 && principalDirectories[0] != nil {
		principals = principalDirectories[0]
	}
	workflows := dependencies.Records.Applications().Workflows
	proposals := agent.NewAgentProposalApplicationService(state, agent.AgentProposalDependencies{
		GuardedWrites: func(ctx context.Context, principal principalmodel.Principal) []agent.AgentGuardedWriteContract {
			return agentGuardedWriteContracts(dependencies.Records.SchemaForPrincipal(ctx, principal).GuardedWrites)
		},
		ResolvePrincipalRole: func(ctx context.Context, userID, roleKey string) (principalmodel.Principal, error) {
			return resolveAgentPrincipalRole(ctx, principals, userID, roleKey)
		},
		InvokeAction:     agentProposalActionInvoker(dependencies.Records.Applications()),
		RunWorkflow:      agentProposalWorkflowRunner(workflows),
		ResolveLifecycle: agentProposalLifecycleResolver(dependencies.Records.Applications().AgentTasks),
	})
	return state, proposals
}

func agentProposalActionInvoker(applications composition.RuntimeApplications) func(context.Context, agent.AgentActionInvocation) (agent.AgentActionInvocationResult, error) {
	return func(ctx context.Context, request agent.AgentActionInvocation) (agent.AgentActionInvocationResult, error) {
		return invokeAgentProposalAction(ctx, applications, request)
	}
}

func agentProposalWorkflowRunner(workflows *workflowapplication.WorkflowApplicationService) func(context.Context, string, map[string]any, principalmodel.Principal) (any, error) {
	return func(ctx context.Context, key string, payload map[string]any, principal principalmodel.Principal) (any, error) {
		return runAgentProposalWorkflow(ctx, workflows, key, payload, principal)
	}
}

func resolveAgentPrincipalRole(ctx context.Context, principals identitysdk.PrincipalResolver, userID, roleKey string) (principalmodel.Principal, error) {
	if principals == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindUnavailable, "agent.authorization.resolver_unavailable", nil, nil)
	}
	resolution, err := principals.Resolve(ctx, identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(userID), RoleKey: roleKey})
	if err != nil {
		return principalmodel.Principal{}, err
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	return principalmodel.NewPrincipalFromIdentity(resolution.Principal, ""), nil
}

func invokeAgentProposalAction(ctx context.Context, applications composition.RuntimeApplications, request agent.AgentActionInvocation) (agent.AgentActionInvocationResult, error) {
	result, err := applications.Actions.Invoke(ctx, actionmodel.ActionSourceAgent, actionmodel.ActionInvocation{ActionKey: request.ActionKey, ObjectKey: request.ObjectKey, RecordID: request.RecordID, Input: request.Input, Principal: request.Principal, Actor: request.Principal, RequestID: request.RequestID, IdempotencyKey: request.IdempotencyKey})
	return agent.AgentActionInvocationResult{Record: result.Record, Object: result.Object}, err
}

func runAgentProposalWorkflow(ctx context.Context, workflows *workflowapplication.WorkflowApplicationService, key string, payload map[string]any, principal principalmodel.Principal) (any, error) {
	return workflows.RunAgentWorkflow(ctx, key, payload, principal)
}

func agentProposalLifecycleResolver(taskRuns *agentruntime.AgentTaskRunApplicationService) func(context.Context, agent.AgentProposal, principalmodel.Principal) error {
	return func(ctx context.Context, proposal agent.AgentProposal, principal principalmodel.Principal) error {
		taskRunID := strings.TrimSpace(fmt.Sprint(proposal.Metadata["task_run_id"]))
		if taskRunID == "" {
			return nil
		}
		if taskRunID == "<nil>" {
			return nil
		}
		if taskRuns == nil {
			return apperror.New(apperror.KindUnavailable, "agent.task.approval_lifecycle_unavailable", nil, nil)
		}
		workspaceID := strings.TrimSpace(proposal.WorkspaceID)
		_, _, err := taskRuns.ResolveApproval(ctx, workspaceID, taskRunID, agentruntime.AgentTaskApprovalResolution{
			ProposalID: proposal.ProposalID, Decision: proposal.Status, Actor: principal.UserID,
			Reason: proposal.DecisionReason, Execution: proposal.Execution,
		})
		return err
	}
}

func agentGuardedWriteContracts(contracts []appschemamodel.ApplicationSchemaGuardedWriteContract) []agent.AgentGuardedWriteContract {
	out := make([]agent.AgentGuardedWriteContract, 0, len(contracts))
	for _, contract := range contracts {
		out = append(out, agent.AgentGuardedWriteContract{ObjectKey: contract.ObjectKey, Operation: contract.Operation, ActionKey: contract.ActionKey, Endpoint: contract.Endpoint, RequiresRecord: contract.RequiresRecord})
	}
	return out
}
