package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/logging"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func (s *WorkflowApplicationService) beginWorkflowExecution(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal, trigger, key string, attempt int, ignore bool) (workflowmodel.WorkflowExecutionClaimResult, workflowmodel.WorkflowExecution, bool, error) {
	if strings.TrimSpace(key) == "" || ignore {
		return workflowmodel.WorkflowExecutionClaimResult{}, workflowmodel.WorkflowExecution{}, false, nil
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "workflow.execute", ResourceType: "workflow", TargetID: workflow.Key, Payload: payload,
		Preconditions: map[string]any{"definition_hash": workflowpolicy.WorkflowDefinitionHash(workflow), "trigger": trigger, "attempt": attempt},
	})
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, workflowmodel.WorkflowExecution{}, false, internalError("fingerprint workflow execution", err)
	}
	owner := strings.TrimSpace(principal.RequestID)
	if owner == "" {
		owner = workflowProcessID(ctx, "workflow-claim")
	}
	claim, err := s.workerRepo.TryBeginExecution(ctx, workflowmodel.WorkflowExecutionClaimRequest{
		Receipt:            workflowmodel.WorkflowExecutionReceipt{WorkspaceID: workflowWorkspaceID(principal.WorkspaceID), WorkflowKey: workflow.Key, IdempotencyKey: key},
		RequestFingerprint: fingerprint, LeaseOwner: owner, LeaseTTL: 5 * time.Minute, Now: time.Now().UTC(),
	})
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, workflowmodel.WorkflowExecution{}, false, internalError("claim workflow execution", err)
	}
	switch claim.Decision {
	case idempotency.DecisionAcquired:
		return claim, workflowmodel.WorkflowExecution{}, false, nil
	case idempotency.DecisionReplay:
		s.auditWorkflowIdempotency(ctx, workflow.Key, principal, claim, "replayed")
		if strings.TrimSpace(claim.Receipt.ExecutionID) == "" {
			return claim, workflowmodel.WorkflowExecution{}, false, conflict(idempotency.ErrorCodeReceiptUnavailable)
		}
		execution, found, getErr := s.workerRepo.GetExecution(ctx, claim.Receipt.WorkspaceID, claim.Receipt.ExecutionID)
		if getErr != nil {
			return claim, workflowmodel.WorkflowExecution{}, false, internalError("read workflow execution replay", getErr)
		}
		if !found {
			return claim, workflowmodel.WorkflowExecution{}, false, conflict(idempotency.ErrorCodeReceiptUnavailable)
		}
		return claim, execution, true, nil
	case idempotency.DecisionFingerprintConflict:
		s.auditWorkflowIdempotency(ctx, workflow.Key, principal, claim, "fingerprint_conflict")
		return claim, workflowmodel.WorkflowExecution{}, false, conflict(idempotency.ErrorCodeKeyReused)
	case idempotency.DecisionInProgress:
		s.auditWorkflowIdempotency(ctx, workflow.Key, principal, claim, "in_progress")
		return claim, workflowmodel.WorkflowExecution{}, false, conflict(idempotency.ErrorCodeInProgress)
	default:
		return claim, workflowmodel.WorkflowExecution{}, false, conflict(idempotency.ErrorCodeReceiptUnavailable)
	}
}

func (s *WorkflowApplicationService) completeWorkflowExecutionReceipt(ctx context.Context, claim workflowmodel.WorkflowExecutionClaimResult, execution workflowmodel.WorkflowExecution, principal principalmodel.Principal) error {
	if claim.Decision != idempotency.DecisionAcquired || strings.TrimSpace(claim.Receipt.ID) == "" {
		return nil
	}
	if err := s.workerRepo.CompleteExecutionReceipt(ctx, workflowmodel.WorkflowExecutionReceiptCompletion{
		WorkspaceID: claim.Receipt.WorkspaceID, ReceiptID: claim.Receipt.ID, ExecutionID: execution.ID, LeaseOwner: claim.Receipt.LeaseOwner,
		FencingToken: claim.Receipt.FencingToken, Now: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(90 * 24 * time.Hour),
	}); err != nil {
		return err
	}
	s.auditWorkflowIdempotency(ctx, claim.Receipt.WorkflowKey, principal, claim, "succeeded")
	return nil
}

func (s *WorkflowApplicationService) auditWorkflowIdempotency(ctx context.Context, workflowKey string, principal principalmodel.Principal, claim workflowmodel.WorkflowExecutionClaimResult, status string) {
	if strings.TrimSpace(claim.Receipt.ID) == "" {
		return
	}
	facts := idempotency.AuditFacts{
		WorkspaceID: claim.Receipt.WorkspaceID, Scope: "workflow.execute", Key: claim.Receipt.IdempotencyKey,
		RequestFingerprint: claim.Receipt.RequestFingerprint, Status: status, FencingToken: claim.Receipt.FencingToken,
	}
	logging.LogIdempotency(ctx, facts, principal.RequestID)
	if s.auditMetadata == nil {
		return
	}
	metadata := idempotency.AuditMetadata(facts)
	s.auditMetadata(ctx, "workflow_execution.idempotency_"+status, "workflow", workflowKey, principal, "Observed idempotent workflow execution", nil, nil, metadata)
}

func workflowWorkspaceID(value string) string {
	return strings.TrimSpace(value)
}

func workflowCommandKey(scope, target, callerKey string, payload any) string {
	key, _ := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "workflow.command." + strings.TrimSpace(scope), ResourceType: "workflow_command", TargetID: strings.TrimSpace(target),
		Payload: map[string]any{"caller_key": strings.TrimSpace(callerKey), "command": payload},
	})
	return key
}

func workflowCommandResultField(scope string) string {
	return "idempotency_command_" + strings.NewReplacer(".", "_", ":", "_").Replace(strings.TrimSpace(scope))
}

func workflowProcessHasCommand(process workflowmodel.WorkflowProcessInstance, scope, key string) bool {
	return strings.TrimSpace(workflowStringValue(process.Result[workflowCommandResultField(scope)])) == strings.TrimSpace(key)
}

func workflowDecisionReplay(ctx context.Context, workspaceID string, processes interface {
	GetProcess(context.Context, string, string) (workflowmodel.WorkflowProcessInstance, bool, error)
}, task workflowmodel.WorkflowTask, req workflowmodel.WorkflowTaskDecisionRequest) (workflowmodel.WorkflowProcessInstance, bool) {
	process, found, err := processes.GetProcess(ctx, workspaceID, task.ProcessID)
	if err != nil || !found {
		return workflowmodel.WorkflowProcessInstance{}, false
	}
	key := workflowCommandKey("task.decision", task.ID, req.IdempotencyKey, workflowTaskDecisionCommandPayload(strings.ToLower(strings.TrimSpace(req.Decision)), req))
	return process, workflowProcessHasCommand(process, "task.decision:"+task.ID, key)
}

func workflowRecordCommand(process *workflowmodel.WorkflowProcessInstance, scope, key string) {
	if process.Result == nil {
		process.Result = map[string]any{}
	}
	process.Result[workflowCommandResultField(scope)] = strings.TrimSpace(key)
}

func workflowStringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
