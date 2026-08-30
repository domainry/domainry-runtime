package runtime

import (
	"context"
	"errors"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	agentsaashost "github.com/domainry/domainry-agent-sdk/saashost"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

type runtimeAgentHost struct{ runtimeID string }

func (h runtimeAgentHost) RuntimeID() string { return h.runtimeID }

func openAgentBinding(ctx context.Context, runtimeID string, factory agentsdk.Factory) (agentsdk.Binding, error) {
	if factory == nil {
		return nil, nil
	}
	application := agentsdk.ApplicationRef{RuntimeID: runtimeID}
	host := runtimeAgentHost{runtimeID: runtimeID}
	var binding agentsdk.Binding
	var err error
	if moduleFactory, ok := factory.(agentmodulehost.Factory); ok {
		binding, err = moduleFactory.OpenModule(ctx, application, host)
	} else if saasFactory, ok := factory.(agentsaashost.Factory); ok {
		binding, err = saasFactory.OpenSaaS(ctx, application, host)
	} else {
		binding, err = factory.Open(ctx, application)
	}
	if err != nil {
		return nil, err
	}
	if binding == nil {
		return nil, errors.New("Agent SDK Factory returned no Binding")
	}
	if err := binding.Descriptor().Validate(); err != nil {
		_ = binding.Close(context.WithoutCancel(ctx))
		return nil, err
	}
	return binding, nil
}

type runtimeAgentTaskRunner struct{ runner agentsdk.TaskRunner }

func (a runtimeAgentTaskRunner) Start(ctx context.Context, request agentapplication.AgentTaskRunnerRequest) (agentapplication.AgentTaskRunnerResult, error) {
	result, err := a.runner.Start(ctx, agentsdk.TaskRequest{
		TaskRunID: request.TaskRunID, ProcessID: request.ProcessID, WorkspaceID: request.WorkspaceID,
		Task:     agentsdk.TaskDefinition{Key: request.Task.Key, Version: request.Task.Version, Instruction: request.Task.Instruction},
		Identity: agentsdk.ExecutionIdentity{Mode: request.Identity.Mode, Initiator: runtimeAgentPrincipalReference(request.Identity.Initiator), Execution: runtimeAgentPrincipalReference(request.Identity.Execution), ServicePrincipalKey: request.Identity.ServicePrincipalKey, ServiceRotationVersion: request.Identity.ServiceRotationVersion},
		Input:    request.Input, ExecutionCredential: request.ExecutionCredential, CorrelationID: request.CorrelationID, IdempotencyKey: request.IdempotencyKey, Deadline: request.Deadline,
	})
	return runtimeAgentTaskResult(result), err
}

func runtimeAgentPrincipalReference(reference agentmodel.AgentPrincipalReference) agentsdk.PrincipalReference {
	return agentsdk.PrincipalReference{UserID: reference.UserID, RoleKey: reference.RoleKey, WorkspaceID: reference.WorkspaceID, AuthorizationRevision: reference.AuthorizationRevision}
}
func (a runtimeAgentTaskRunner) Poll(ctx context.Context, id, key string) (agentapplication.AgentTaskRunnerResult, error) {
	result, err := a.runner.Poll(ctx, id, key)
	return runtimeAgentTaskResult(result), err
}
func (a runtimeAgentTaskRunner) Cancel(ctx context.Context, id, key string) (agentapplication.AgentTaskRunnerResult, error) {
	result, err := a.runner.Cancel(ctx, id, key)
	return runtimeAgentTaskResult(result), err
}
func runtimeAgentTaskResult(result agentsdk.TaskResult) agentapplication.AgentTaskRunnerResult {
	return agentapplication.AgentTaskRunnerResult{ExternalRunID: result.ExternalRunID, Status: agentapplication.AgentProviderRunStatus(result.Status), Outcome: result.Outcome, Output: result.Output, RawEvidence: result.RawEvidence, Model: result.Model, Usage: result.Usage, ErrorClass: result.ErrorClass, ErrorCode: result.ErrorCode, Retryable: result.Retryable}
}

type runtimeInteractiveAgentRunner struct{ runner agentsdk.InteractiveRunner }

func (a runtimeInteractiveAgentRunner) Run(ctx context.Context, request agentapplication.InteractiveAgentRunRequest) (agentapplication.InteractiveAgentResult, error) {
	candidates := make([]agentsdk.RouteCandidate, len(request.Candidates))
	for i, c := range request.Candidates {
		candidates[i] = agentsdk.RouteCandidate{RouteType: c.RouteType, TargetKey: c.TargetKey, Version: c.Version}
	}
	result, err := a.runner.Run(ctx, agentsdk.InteractiveRequest{RunID: request.RunID, SessionID: request.SessionID, Context: agentsdk.GlobalContext{WorkspaceID: request.Context.Principal.WorkspaceID, ActorID: request.Context.Principal.UserID, Locale: request.Context.Locale, Timezone: request.Context.Timezone, Values: map[string]any{"contract_version": request.Context.ContractVersion, "context_revision": request.Context.ContextRevision, "entrypoint_key": request.Context.EntrypointKey, "agent_key": request.Context.AgentKey, "surface": request.Context.Surface, "route_key": request.Context.RouteKey, "object_key": request.Context.ObjectKey, "record_id": request.Context.RecordID, "selected_record_ids": request.Context.SelectedRecordIDs, "allowed_task_keys": request.Context.AllowedTaskKeys, "allowed_workflow_keys": request.Context.AllowedWorkflowKeys, "available_operation_ids": request.Context.AvailableOperations}}, Message: request.Message, ExecutionCredential: request.ExecutionCredential, IdempotencyKey: request.IdempotencyKey, Candidates: candidates, MaxSteps: request.MaxSteps, MaxToolCalls: request.MaxToolCalls, Deadline: request.Deadline})
	converted := agentapplication.InteractiveAgentResult{RunID: result.RunID, ExternalRunID: result.ExternalRunID, Status: result.Status, Message: result.Message, Structured: result.Structured, Model: result.Model, Usage: result.Usage, EvidenceRefs: result.EvidenceRefs}
	if result.Route != nil {
		converted.Route = &agentapplication.AgentRouteResult{RouteType: result.Route.RouteType, TargetKey: result.Route.TargetKey, TargetVersion: result.Route.TargetVersion, Input: result.Route.Input, Reason: result.Route.Reason, IdempotencyKey: result.Route.IdempotencyKey}
	}
	if result.Handoff != nil {
		converted.Handoff = &agentmodel.InteractiveAgentHandoff{ContractVersion: agentmodel.InteractiveHandoffContractVersion, RouteType: agentmodel.AgentRouteTask, TargetKey: result.Handoff.TaskKey, Input: result.Handoff.Input, IdempotencyKey: request.IdempotencyKey, TaskRunID: result.Handoff.TaskRunID}
	}
	return converted, err
}
