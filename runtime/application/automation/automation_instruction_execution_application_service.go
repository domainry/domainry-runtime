package automation

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationruntime "github.com/domainry/domainry-runtime/runtime/domain/automation/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"

	workerplatform "github.com/domainry/domainry-foundation/worker"
)

func AutomationWorkflowInstructionResult(workflowKey string, run workflowmodel.WorkflowRunResult, err error) (map[string]any, error) {
	if err != nil {
		return nil, err
	}
	return map[string]any{"workflow_key": workflowKey, "execution_id": run.Execution.ID, "status": run.Execution.Status}, nil
}

const automationInstructionLeaseDuration = 30 * time.Second

type AutomationInstructionExecutionRequest struct {
	Phase       string
	Simulation  bool
	WorkspaceID string
	Rule        automationmodel.AutomationRuleSchema
	Instruction automationmodel.AutomationInstructionSchema
	Record      *recordmodel.Record
	Execute     func(context.Context) (automationmodel.AutomationInstructionResult, error)
}

// AutomationInstructionExecutionApplicationService executes automation instructions.
type AutomationInstructionExecutionApplicationService struct {
	repository automationcontract.AutomationWorkerStore
	worker     workerplatform.Dependencies
}

func NewAutomationInstructionExecutionApplicationService(repository automationcontract.AutomationWorkerStore) *AutomationInstructionExecutionApplicationService {
	return NewAutomationInstructionExecutionApplicationServiceWithWorker(repository, workerplatform.Dependencies{})
}

func NewAutomationInstructionExecutionApplicationServiceWithWorker(repository automationcontract.AutomationWorkerStore, worker workerplatform.Dependencies) *AutomationInstructionExecutionApplicationService {
	return &AutomationInstructionExecutionApplicationService{repository: repository, worker: workerplatform.NormalizeDependencies(worker)}
}

func (s *AutomationInstructionExecutionApplicationService) Execute(ctx context.Context, request AutomationInstructionExecutionRequest) (automationmodel.AutomationInstructionResult, error) {
	if _, err := principalmodel.NewWorkspaceCommandScope(request.WorkspaceID); err != nil {
		return automationmodel.AutomationInstructionResult{}, automationError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	instruction := request.Instruction
	idempotencyKey := ""
	claimed := false
	leaseOwner := ""
	var fencingToken int64
	replayed := false
	var result automationmodel.AutomationInstructionResult
	var resultErr error

	if request.Phase == "after" && !request.Simulation {
		idempotencyKey = automationruntime.AutomationInstructionIdempotencyKey(request.Rule, instruction, request.Record)
		recordID := "unknown"
		if request.Record != nil {
			recordID = valueOrDefault(request.Record.ID, "unknown")
		}
		claim := automationmodel.AutomationInstructionExecution{
			WorkspaceID: request.WorkspaceID, IdempotencyKey: idempotencyKey, RuleKey: request.Rule.Key, ObjectKey: request.Rule.ObjectKey,
			RecordID: recordID, RecordVersion: automationruntime.AutomationRecordVersion(request.Record), Operation: request.Rule.Trigger.Operation, InstructionKey: instruction.Key,
		}
		now := s.worker.Clock.Now()
		existing, didClaim, err := s.repository.ClaimInstruction(ctx, request.WorkspaceID, claim, s.worker.WorkerID.String(), now.Format(time.RFC3339), now.Add(automationInstructionLeaseDuration).Format(time.RFC3339))
		if err != nil {
			resultErr = automationError(apperror.KindInternal, "backend.internal", err, "operation", "claim automation instruction execution")
		} else if !didClaim {
			if existing.Status == "succeeded" {
				result = automationruntime.AutomationInstructionResult(existing.Result)
				result.Key, result.Type, result.Status = instruction.Key, instruction.Type, "idempotent_replay"
				replayed = true
			} else {
				resultErr = automationError(apperror.KindConflict, "backend.automation.instruction_in_progress", nil,
					"rule", request.Rule.Key, "instruction", instruction.Key, "idempotency_key", idempotencyKey)
			}
		} else {
			claimed = true
			workerplatform.SetQueueMetrics("automation_instruction", 0, 0)
			workerplatform.ObserveOutcome("automation_instruction", "claimed")
			leaseOwner = existing.LeaseOwner
			fencingToken = existing.FencingToken
		}
	}

	if resultErr == nil && !replayed {
		instructionCtx := ctx
		stopHeartbeat := func() error { return nil }
		if claimed {
			instructionCtx, stopHeartbeat = workerplatform.WithHeartbeat(ctx, automationInstructionLeaseDuration/3, func(heartbeatCtx context.Context) error {
				now := s.worker.Clock.Now()
				_, err := s.repository.HeartbeatInstruction(heartbeatCtx, request.WorkspaceID, idempotencyKey, leaseOwner, fencingToken, now.Add(automationInstructionLeaseDuration).Format(time.RFC3339), now.Format(time.RFC3339))
				return err
			})
		}
		if request.Simulation && automationruntime.AutomationIsSimulationSideEffect(instruction.Type) {
			result = automationmodel.AutomationInstructionResult{Key: instruction.Key, Type: instruction.Type, Status: "simulated", Data: map[string]any{"dry_run": true}}
		} else {
			result, resultErr = request.Execute(instructionCtx)
		}
		if heartbeatErr := stopHeartbeat(); resultErr == nil && heartbeatErr != nil {
			resultErr = heartbeatErr
		}
	}

	if ctx.Err() != nil {
		resultErr = automationError(apperror.KindBadRequest, "backend.automation.rule_timeout", nil,
			"rule", request.Rule.Key, "instruction", instruction.Key)
		result.Status, result.ErrorCode = "failed", errorCode(resultErr)
	}
	if request.Phase == "after" && claimed {
		status := "succeeded"
		errorCodeValue := ""
		if resultErr != nil {
			status, errorCodeValue = string(idempotency.StatusFailedRetryable), errorCode(resultErr)
		}
		if _, err := s.repository.CompleteInstruction(ctx, request.WorkspaceID, idempotencyKey, leaseOwner, fencingToken, status, automationruntime.AutomationInstructionResultMap(result), errorCodeValue, s.worker.Clock.Now().Format(time.RFC3339)); err != nil {
			if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
				workerplatform.ObserveOutcome("automation_instruction", "lease_lost")
				resultErr = automationError(apperror.KindConflict, "backend.automation.instruction_lease_lost", nil,
					"rule", request.Rule.Key, "instruction", instruction.Key)
			} else {
				resultErr = automationError(apperror.KindInternal, "backend.internal", err, "operation", "complete automation instruction execution")
			}
			result.Status, result.ErrorCode = "failed", errorCode(resultErr)
		} else if resultErr != nil {
			workerplatform.ObserveOutcome("automation_instruction", "retry")
		} else {
			workerplatform.ObserveOutcome("automation_instruction", "completed")
		}
	}
	return result, resultErr
}
