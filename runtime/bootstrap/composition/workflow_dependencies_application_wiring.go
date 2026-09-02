package composition

import (
	"context"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	apperror "github.com/domainry/domainry-foundation/apperror"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agenthost"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type runtimeAgentRecordVisibility struct{ records *runtimeAssembly }

func (a runtimeAgentRecordVisibility) CanReadAgentRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (bool, error) {
	if a.records == nil {
		return false, nil
	}
	if a.records.recordApplicationService == nil {
		return false, nil
	}
	return a.records.recordApplicationService.RecordScopeAllows(ctx, objectKey, recordID, principal)
}

func workflowDependencies(records *runtimeAssembly) workflowapplication.WorkflowDependencies {
	if records == nil {
		return workflowapplication.WorkflowDependencies{}
	}
	ensureRecordSchemaSnapshotProvider(records)
	objectMap := func(context.Context) map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{}
	}
	if records.schemaService != nil {
		objectMap = func(ctx context.Context) map[string]definitionmodel.ObjectSchema {
			if ctx.Err() != nil {
				return map[string]definitionmodel.ObjectSchema{}
			}
			result := map[string]definitionmodel.ObjectSchema{}
			for _, object := range records.Schema().Objects {
				result[object.Key] = object
			}
			return result
		}
	}
	return workflowapplication.WorkflowDependencies{
		Definitions:            records.workflowDefinitionRepo,
		Processes:              records.workflowProcessRepo,
		Workers:                records.workflowWorkerRepo,
		Decisions:              records.workflowDecisionRepo,
		WorkflowRegistry:       runtimeWorkflowRegistry{records: records},
		WaitTimers:             runtimeWorkflowRecordTimers{recordTimers: records.recordTimerService},
		ApprovalDeadlineTimers: runtimeWorkflowRecordTimers{recordTimers: records.recordTimerService},
		CompileNotification:    records.workflowNotificationCompiler,
		TaskNotificationCommit: records.workflowTaskNotificationCommitter,
		StartAgentTask: func(ctx context.Context, preparation workflowapplication.WorkflowAgentTaskPreparation) (agentsdk.TaskResult, error) {
			if records.agentTaskDispatchService == nil || records.agentTaskRunner == nil {
				return agentsdk.TaskResult{}, apperror.New(apperror.KindUnavailable, "agent.task.dispatch_unavailable", nil, nil)
			}
			maxAttempts := 1
			if preparation.Contract.Retry != nil {
				if preparation.Contract.Retry.MaxAttempts > maxAttempts {
					maxAttempts = preparation.Contract.Retry.MaxAttempts
				}
			}
			command, err := records.agentTaskDispatchService.PrepareRequest(ctx, agentapplication.AgentTaskDispatchRequest{
				RunID: preparation.RunID, WorkspaceID: preparation.WorkspaceID, ProcessID: preparation.ProcessID, NodeInstanceID: preparation.NodeInstanceID,
				NodeID: preparation.NodeID, Iteration: preparation.Iteration, DefinitionSnapshotHash: preparation.DefinitionSnapshotHash, ManifestHash: preparation.ManifestHash,
				TaskKey: preparation.Contract.TaskKey, TaskVersion: preparation.Contract.TaskVersion,
				Identity: agentsdk.AgentTaskIdentity{Mode: preparation.Contract.Identity.Mode, PrincipalKey: preparation.Contract.Identity.PrincipalKey}, Input: preparation.Input,
				AllowedObjects: preparation.Contract.AllowedObjects, AllowedActions: preparation.Contract.AllowedActions, AllowedOutcomes: preparation.Contract.AllowedOutcomes,
				TimeoutSeconds: preparation.Contract.TimeoutSeconds, MaxAttempts: maxAttempts, Initiator: preparation.Initiator, CorrelationID: preparation.CorrelationID,
			})
			if err != nil {
				return agentsdk.TaskResult{}, err
			}
			return records.agentTaskRunner.Start(agentsdk.WithAuthorizedServiceAction(ctx, agentsdk.ActionAgentTaskExecutionStart, agentsdk.AgentRuntimeServiceAudience), command)
		},
		WakeWorkflowContinuation: func(workspaceID, executionID string) {
			workflowapplication.WakeWorkflowContinuation(records.workflowApplicationService, workflowapplication.WorkflowContinuationLocator{WorkspaceID: workspaceID, ExecutionID: executionID})
		},
		Identity:     records.identityDirectory,
		Principals:   records.agentPrincipals,
		Schema:       runtimeWorkflowSchemaProvider{records: records},
		RecordReader: workflowapplication.NewWorkflowRecordReaderAdapter(records.recordRepo),
		ObjectForAction: func(ctx context.Context, principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
			if err := ctx.Err(); err != nil {
				return definitionmodel.ObjectSchema{}, err
			}
			return records.RecordQueryPolicyDomainService.ObjectForAction(principal, objectKey, action)
		},
		ObjectMap: objectMap,
		CanAccessRecord: func(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
			return ctx.Err() == nil && records.RecordQueryPolicyDomainService.CanAccessRecord(principal, object, record)
		},
		ActionExists: func(ctx context.Context, key string) bool {
			if ctx.Err() != nil {
				return false
			}
			records.mu.RLock()
			defer records.mu.RUnlock()
			_, ok := records.actions[strings.TrimSpace(key)]
			return ok
		},
		InvokeAction: func(ctx context.Context, invocation workflowapplication.WorkflowBusinessActionInvocation) (workflowapplication.WorkflowBusinessActionInvocationResult, error) {
			if records.actionService == nil {
				return workflowapplication.WorkflowBusinessActionInvocationResult{}, apperror.New(apperror.KindInternal, "backend.internal", nil, map[string]string{"operation": "invoke workflow domain action"})
			}
			executionPrincipal := invocation.Principal.WithExactSystemCapabilities(invocation.ActionKey)
			result, err := records.actionService.Invoke(ctx, actionmodel.ActionSourceWorkflow, actionmodel.ActionInvocation{
				ActionKey: invocation.ActionKey, ObjectKey: invocation.ObjectKey, RecordID: invocation.RecordID,
				Input: invocation.Input, Principal: executionPrincipal, Actor: invocation.Actor,
				ProcessID: invocation.ProcessID, NodeID: invocation.NodeID,
				IdempotencyKey: invocation.IdempotencyKey,
			})
			return workflowActionInvocationResult(result), err
		},
		Audit:         records.auditApplicationService.Append,
		AuditMetadata: records.auditApplicationService.AppendWithMetadata,
		Worker:        records.workerDependencies,
	}
}
