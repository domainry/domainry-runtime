package composition

import (
	"context"
	"fmt"

	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type recordTimerWorkflowRuntime interface {
	ResumeTimerNode(context.Context, string, string, string, principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error)
	ProcessApprovalDeadlineTimer(context.Context, string, string, string, principalmodel.Principal) error
}

type recordTimerActionRuntime interface {
	Invoke(context.Context, actionmodel.ActionSource, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
}

func executeRecordTimer(ctx context.Context, execution recordtimerapplication.RecordTimerExecution, principal principalmodel.Principal, workflows recordTimerWorkflowRuntime, actionService recordTimerActionRuntime) error {
	switch execution.TargetType {
	case "workflow":
		switch execution.TargetKey {
		case "resume_node":
			processID, _ := execution.Payload["process_id"].(string)
			nodeID, _ := execution.Payload["node_id"].(string)
			_, err := workflows.ResumeTimerNode(ctx, execution.WorkspaceID, processID, nodeID, principal)
			return err
		case "approval_deadline":
			taskID, _ := execution.Payload["task_id"].(string)
			phase, _ := execution.Payload["phase"].(string)
			return workflows.ProcessApprovalDeadlineTimer(ctx, execution.WorkspaceID, taskID, phase, principal)
		default:
			return fmt.Errorf("unsupported workflow timer target %q", execution.TargetKey)
		}
	case "action":
		executionPrincipal := principal.WithExactSystemCapabilities(execution.TargetKey)
		_, err := actionService.Invoke(ctx, actionmodel.ActionSourceRecordTimer, actionmodel.ActionInvocation{ActionKey: execution.TargetKey, ObjectKey: execution.ObjectKey, RecordID: execution.RecordID, Input: execution.Payload, Principal: executionPrincipal, Actor: principal, IdempotencyKey: execution.IdempotencyKey})
		return err
	default:
		return fmt.Errorf("unsupported record timer target type %q", execution.TargetType)
	}
}

type recordTimerTargetRuntimeAdapter struct {
	execute func(context.Context, recordtimerapplication.RecordTimerExecution, principalmodel.Principal) error
}

func (adapter recordTimerTargetRuntimeAdapter) ExecuteRecordTimer(ctx context.Context, execution recordtimerapplication.RecordTimerExecution, principal principalmodel.Principal) error {
	if adapter.execute == nil {
		return fmt.Errorf("record timer runtime is not configured")
	}
	return adapter.execute(ctx, execution, principal)
}

var _ recordtimerapplication.TargetRuntime = recordTimerTargetRuntimeAdapter{}

func newRecordTimerTargetRuntimeAdapter(s *runtimeAssembly) recordTimerTargetRuntimeAdapter {
	return recordTimerTargetRuntimeAdapter{execute: func(ctx context.Context, execution recordtimerapplication.RecordTimerExecution, principal principalmodel.Principal) error {
		principal.WorkspaceID = execution.WorkspaceID
		switch execution.TargetType {
		case "workflow":
			if s.workflowApplicationService == nil {
				return fmt.Errorf("unsupported workflow timer target %q", execution.TargetKey)
			}
		case "action":
			if s.actionService == nil {
				return fmt.Errorf("action timer runtime is not configured")
			}
		}
		return executeRecordTimer(ctx, execution, principal, s.workflowApplicationService, s.actionService)
	}}
}
