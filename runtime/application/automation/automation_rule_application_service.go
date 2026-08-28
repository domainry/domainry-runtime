package automation

import (
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationpolicy "github.com/domainry/domainry-runtime/runtime/domain/automation/policy"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	automationruntime "github.com/domainry/domainry-runtime/runtime/domain/automation/runtime"
	automationdomain "github.com/domainry/domainry-runtime/runtime/domain/automation/service"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type simulationContextKey string

const (
	simulationEnabledKey simulationContextKey = "automation_simulation"
	simulationTargetKey  simulationContextKey = "automation_simulation_target"
)

func WithSimulation(ctx context.Context, targetNodeID string) context.Context {
	ctx = context.WithValue(ctx, simulationEnabledKey, true)
	return context.WithValue(ctx, simulationTargetKey, strings.TrimSpace(targetNodeID))
}

type RuleExecutionRequest struct {
	Rule               automationmodel.AutomationRuleSchema
	Phase              string
	Input              map[string]any
	Before             map[string]any
	Candidate          map[string]any
	Record             *recordmodel.Record
	Principal          principalmodel.Principal
	WorkspaceID        string
	Initialize         func()
	MatchCondition     automationpolicy.AutomationConditionMatcher
	ExecuteInstruction func(context.Context, automationmodel.AutomationInstructionSchema) (automationmodel.AutomationInstructionResult, error)
	StoreResult        func(string, automationmodel.AutomationInstructionResult)
	PersistExecution   func(context.Context, automationmodel.AutomationRuleExecution) error
}

type AutomationAuditAppender func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)

// AutomationRuleApplicationService coordinates rule execution, worker leases, persistence, and audit.
type AutomationRuleApplicationService struct {
	executionRepository automationrepository.AutomationExecutionRepository
	instructionService  *AutomationInstructionExecutionApplicationService
	audit               AutomationAuditAppender
}

func NewAutomationRuleApplicationService(executionRepository automationrepository.AutomationExecutionRepository, workerRepository automationcontract.AutomationWorkerStore, audit AutomationAuditAppender) *AutomationRuleApplicationService {
	return NewAutomationRuleApplicationServiceWithWorker(executionRepository, workerRepository, audit, workerplatform.Dependencies{})
}

func NewAutomationRuleApplicationServiceWithWorker(executionRepository automationrepository.AutomationExecutionRepository, workerRepository automationcontract.AutomationWorkerStore, audit AutomationAuditAppender, worker workerplatform.Dependencies) *AutomationRuleApplicationService {
	return &AutomationRuleApplicationService{
		executionRepository: executionRepository,
		instructionService:  NewAutomationInstructionExecutionApplicationServiceWithWorker(workerRepository, worker),
		audit:               audit,
	}
}

func (s *AutomationRuleApplicationService) Execute(ctx context.Context, request RuleExecutionRequest) (trace automationprojection.AutomationRuleTrace, retErr error) {
	if err := automationAuthorizeCommand(request.Principal); err != nil {
		return automationprojection.AutomationRuleTrace{}, err
	}
	if strings.TrimSpace(request.WorkspaceID) != strings.TrimSpace(request.Principal.WorkspaceID) {
		return automationprojection.AutomationRuleTrace{}, automationError(apperror.KindForbidden, "backend.workspace_scope_mismatch", nil)
	}
	if ctx == nil {
		return automationprojection.AutomationRuleTrace{}, automationError(apperror.KindInternal, "backend.internal", nil, "operation", "automation execution context is required")
	}
	started := time.Now()
	executionID := fmt.Sprintf("automation_execution_%d", time.Now().UnixNano())
	if request.Phase == "after" && request.Record != nil {
		executionID = "automation_rule:" + automationruntime.AutomationRuleIdempotencyKey(request.Rule, request.Record)
	}
	trace = automationprojection.AutomationRuleTrace{ExecutionID: executionID, RuleKey: request.Rule.Key, Status: "succeeded", Matched: true, InstructionTraces: []automationprojection.AutomationInstructionTrace{}, NodeTraces: []automationprojection.AutomationNodeTrace{}}
	targetNodeID, _ := ctx.Value(simulationTargetKey).(string)
	simulation, _ := ctx.Value(simulationEnabledKey).(bool)
	recordID := ""
	if request.Record != nil {
		recordID = request.Record.ID
	}
	defer func() {
		if s.executionRepository == nil && request.PersistExecution == nil {
			return
		}
		eventID := ""
		if request.Record != nil && request.Phase == "after" {
			eventID = automationruntime.AutomationRuleIdempotencyKey(request.Rule, request.Record)
		}
		execution := automationmodel.AutomationRuleExecution{
			ID: executionID, WorkspaceID: request.WorkspaceID, RuleKey: request.Rule.Key, ObjectKey: request.Rule.ObjectKey, RecordID: recordID,
			Phase: request.Phase, Operation: request.Rule.Trigger.Operation, Status: trace.Status, ActorID: request.Principal.UserID, RoleKey: request.Principal.RoleKey,
			RequestID: request.Principal.RequestID, CorrelationID: valueOrDefault(request.Principal.RequestID, executionID), EventID: eventID,
			DurationMS: trace.DurationMS, ErrorCode: trace.ErrorCode, Candidate: recordcontract.RecordCloneData(request.Candidate), Trace: automationdomain.TraceMap(trace),
		}
		if request.PersistExecution != nil {
			_ = request.PersistExecution(ctx, execution)
			return
		}
		_, _ = s.executionRepository.InsertExecution(ctx, request.WorkspaceID, execution)
	}()
	trace.NodeTraces = append(trace.NodeTraces, automationprojection.AutomationNodeTrace{
		NodeID: "trigger", Kind: "trigger", Status: "succeeded",
		Input:  map[string]any{"object_key": request.Rule.ObjectKey, "phase": request.Phase, "operation": request.Rule.Trigger.Operation},
		Output: map[string]any{"matched_lifecycle": true},
	})
	if targetNodeID == "trigger" {
		return trace, nil
	}
	auditBase := map[string]any{"rule_key": request.Rule.Key, "phase": request.Phase, "operation": request.Rule.Trigger.Operation, "request_id": request.Principal.RequestID, "run_as": "initiator"}
	s.appendAudit(ctx, "automation_rule_started", request.Rule.ObjectKey, recordID, request.Principal, "Started automation rule "+request.Rule.Key, request.Before, request.Candidate, auditBase)
	if request.Initialize != nil {
		request.Initialize()
	}
	conditionStarted := time.Now()
	matched := automationpolicy.AutomationConditionGroupMatches(request.Rule.Conditions, request.MatchCondition)
	conditionStatus := "succeeded"
	if !matched {
		conditionStatus = "skipped"
	}
	trace.NodeTraces = append(trace.NodeTraces, automationprojection.AutomationNodeTrace{
		NodeID: "condition", Kind: "condition", Status: conditionStatus, DurationMS: time.Since(conditionStarted).Milliseconds(),
		Input: map[string]any{"conditions": request.Rule.Conditions}, Output: map[string]any{"matched": matched},
	})
	if targetNodeID == "condition" {
		trace.Matched = matched
		trace.Status = conditionStatus
		trace.DurationMS = time.Since(started).Milliseconds()
		return trace, nil
	}
	if !matched {
		trace.Status, trace.Matched, trace.DurationMS = "skipped", false, time.Since(started).Milliseconds()
		s.appendAudit(ctx, "automation_rule_skipped", request.Rule.ObjectKey, recordID, request.Principal, "Skipped automation rule "+request.Rule.Key, request.Before, request.Candidate, map[string]any{"rule_key": request.Rule.Key, "duration_ms": trace.DurationMS})
		return trace, nil
	}
	for index, instruction := range request.Rule.Instructions {
		instructionStarted := time.Now()
		if err := ctx.Err(); err != nil {
			timeoutErr := automationError(apperror.KindBadRequest, "backend.automation.rule_timeout", nil, "rule", request.Rule.Key, "instruction", instruction.Key)
			trace.Status, trace.ErrorCode, trace.DurationMS = "blocked", errorCode(timeoutErr), time.Since(started).Milliseconds()
			trace.NodeTraces = append(trace.NodeTraces, automationdomain.FailedNodeTrace("action:"+instruction.Key, instruction.Type, instructionStarted, timeoutErr, "", instruction.Input, nil))
			return trace, timeoutErr
		}
		s.appendAudit(ctx, "automation_instruction_started", request.Rule.ObjectKey, recordID, request.Principal, "Started automation instruction "+instruction.Key, nil, nil, map[string]any{
			"rule_key": request.Rule.Key, "instruction_key": instruction.Key, "instruction_type": instruction.Type, "instruction_index": index,
		})
		stepResult, stepErr := s.instructionService.Execute(ctx, AutomationInstructionExecutionRequest{
			Phase: request.Phase, Simulation: simulation, WorkspaceID: request.WorkspaceID, Rule: request.Rule, Instruction: instruction, Record: request.Record,
			Execute: func(instructionCtx context.Context) (automationmodel.AutomationInstructionResult, error) {
				return request.ExecuteInstruction(instructionCtx, instruction)
			},
		})
		alias := valueOrDefault(strings.TrimSpace(instruction.ResultAlias), instruction.Key)
		if alias != "" && request.StoreResult != nil {
			request.StoreResult(alias, stepResult)
		}
		instructionTrace := automationprojection.AutomationInstructionTrace{
			Key: instruction.Key, Type: instruction.Type, Status: stepResult.Status, DurationMS: time.Since(instructionStarted).Milliseconds(),
			ErrorCode: stepResult.ErrorCode, InvocationID: stepResult.InvocationID, Input: recordcontract.RecordCloneData(instruction.Input), Data: stepResult.Data,
		}
		if stepErr != nil {
			instructionTrace.ErrorCode = errorCode(stepErr)
			instructionTrace.ErrorReason = stepErr.Error()
			_, instructionTrace.ErrorParams = automationErrorDetails(stepErr)
		}
		trace.InstructionTraces = append(trace.InstructionTraces, instructionTrace)
		trace.NodeTraces = append(trace.NodeTraces, automationprojection.AutomationNodeTrace{
			NodeID: "action:" + instruction.Key, Kind: instruction.Type, Status: instructionTrace.Status, DurationMS: instructionTrace.DurationMS,
			ErrorCode: instructionTrace.ErrorCode, ErrorReason: instructionTrace.ErrorReason, ErrorParams: instructionTrace.ErrorParams,
			InvocationID: instructionTrace.InvocationID, Input: instructionTrace.Input, Output: instructionTrace.Data,
		})
		if stepErr != nil {
			trace.Status, trace.ErrorCode, trace.DurationMS = "blocked", errorCode(stepErr), time.Since(started).Milliseconds()
			s.appendAudit(ctx, "automation_instruction_blocked", request.Rule.ObjectKey, recordID, request.Principal, "Blocked automation instruction "+instruction.Key, nil, nil, map[string]any{
				"rule_key": request.Rule.Key, "instruction_key": instruction.Key, "instruction_type": instruction.Type, "error_code": trace.ErrorCode, "duration_ms": trace.DurationMS, "invocation_id": stepResult.InvocationID,
			})
			return trace, stepErr
		}
		s.appendAudit(ctx, "automation_instruction_succeeded", request.Rule.ObjectKey, recordID, request.Principal, "Completed automation instruction "+instruction.Key, nil, nil, map[string]any{
			"rule_key": request.Rule.Key, "instruction_key": instruction.Key, "instruction_type": instruction.Type, "invocation_id": stepResult.InvocationID,
		})
		if targetNodeID == "action:"+instruction.Key || targetNodeID == instruction.Key {
			trace.DurationMS = time.Since(started).Milliseconds()
			return trace, nil
		}
	}
	terminalID, terminalKind := "save", "save"
	if request.Phase == "after" {
		terminalID, terminalKind = "outbox", "outbox"
	}
	trace.NodeTraces = append(trace.NodeTraces, automationprojection.AutomationNodeTrace{NodeID: terminalID, Kind: terminalKind, Status: "succeeded", Output: map[string]any{"ready": true}})
	trace.DurationMS = time.Since(started).Milliseconds()
	s.appendAudit(ctx, "automation_rule_succeeded", request.Rule.ObjectKey, recordID, request.Principal, "Completed automation rule "+request.Rule.Key, request.Before, request.Candidate, map[string]any{
		"rule_key": request.Rule.Key, "phase": request.Phase, "operation": request.Rule.Trigger.Operation, "duration_ms": trace.DurationMS, "instruction_count": len(trace.InstructionTraces),
	})
	return trace, nil
}

func (s *AutomationRuleApplicationService) appendAudit(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, message string, before, after, metadata map[string]any) {
	if s.audit != nil {
		s.audit(ctx, event, objectKey, recordID, principal, message, before, after, metadata)
	}
}

func automationError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}

func automationErrorDetails(err error) (string, map[string]string) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode(), appErr.ErrorParams()
	}
	return "backend.internal", nil
}

func errorCode(err error) string {
	code, _ := automationErrorDetails(err)
	return code
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
