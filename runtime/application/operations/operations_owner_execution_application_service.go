package operations

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type OperationsOwnerExecutionRequest struct {
	Kind         string
	ResourceType string
	ResourceID   string
	Reason       string
	Reference    string
	Payload      any
	Key          string
	// ReplayReadiness re-reads the owner boundary after authorization and
	// before a terminal historical result is returned. It must not repeat the
	// mutation; it verifies that the owner is still reachable and the caller is
	// still allowed to observe the current resource.
	ReplayReadiness func(context.Context, any) error
}

type OperationsOwnerExecutionResult struct {
	Value    any
	Receipt  operationsmodel.OperationsReceipt
	Replayed bool
}

// ExecuteOwnerOperation closes the compatibility route gap without moving an
// owner's policy into Operations. Operations owns authorization intent,
// idempotent orchestration and the durable receipt; the callback remains the
// only place that may perform the owner transition.
func (s *OperationsApplicationService) ExecuteOwnerOperation(ctx context.Context, request OperationsOwnerExecutionRequest, principal principalmodel.Principal, execute func(context.Context) (any, error)) (OperationsOwnerExecutionResult, error) {
	definition, found := s.definition(request.Kind)
	if !found || definition.ExecutionScope != operationsmodel.OperationsExecutionWorkspace || definition.ResourceType != strings.TrimSpace(request.ResourceType) {
		return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.definition_mismatch", nil, nil)
	}
	if err := operationsAuthorize(principal, definition.ActionKey); err != nil {
		return OperationsOwnerExecutionResult{}, err
	}
	receipt, decision, err := s.Submit(ctx, OperationsSubmitRequest{
		Kind: request.Kind, ResourceType: request.ResourceType,
		ResourceID: request.ResourceID, Reason: request.Reason, Reference: request.Reference, Payload: request.Payload,
	}, request.Key, principal)
	if err != nil {
		return OperationsOwnerExecutionResult{}, err
	}
	return s.executeOwnerReceipt(ctx, receipt, decision, request.ReplayReadiness, execute)
}

func (s *OperationsApplicationService) executeOwnerReceipt(ctx context.Context, receipt operationsmodel.OperationsReceipt, decision operationsmodel.OperationsSubmissionDecision, replayReadiness func(context.Context, any) error, execute func(context.Context) (any, error)) (OperationsOwnerExecutionResult, error) {
	result := OperationsOwnerExecutionResult{Receipt: receipt, Replayed: decision == operationsmodel.OperationsSubmissionReplay}
	if receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		resultJSON, readErr := s.receiptResult(ctx, receipt)
		if readErr != nil {
			return OperationsOwnerExecutionResult{}, readErr
		}
		if len(resultJSON) != 0 {
			if err := json.Unmarshal(resultJSON, &result.Value); err != nil {
				return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindInternal, "backend.operations.receipt_result_invalid", err, nil)
			}
		}
		if result.Replayed {
			if replayReadiness == nil {
				return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindInternal, "backend.operations.replay_readiness_unavailable", nil, nil)
			}
			if err := replayReadiness(ctx, result.Value); err != nil {
				return OperationsOwnerExecutionResult{}, err
			}
		}
		return result, nil
	}
	if receipt.Command.Status == operationsmodel.OperationsStatusFailed {
		if result.Replayed {
			if replayReadiness == nil {
				return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindInternal, "backend.operations.replay_readiness_unavailable", nil, nil)
			}
			if err := replayReadiness(ctx, nil); err != nil {
				return OperationsOwnerExecutionResult{}, err
			}
		}
		return result, apperror.New(apperror.KindConflict, receipt.ErrorCode, nil, nil)
	}
	if decision == operationsmodel.OperationsSubmissionReplay && receipt.Command.Status == operationsmodel.OperationsStatusStarted {
		return result, apperror.New(apperror.KindConflict, "backend.operations.in_progress", nil, nil)
	}
	if receipt.Command.Status == operationsmodel.OperationsStatusCreated {
		started, err := s.Start(ctx, receipt.Command.ID, receipt.Command.Scope, operationsOwnerSystemScope())
		if err != nil {
			return OperationsOwnerExecutionResult{}, err
		}
		receipt = started
		result.Receipt = receipt
	}
	if receipt.Command.Status != operationsmodel.OperationsStatusStarted {
		return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindConflict, "backend.operations.receipt_status_invalid", nil, nil)
	}
	ownerContext := requestcontext.WithOwnerExecutionID(ctx, receipt.Command.ID)
	value, ownerErr := execute(ownerContext)
	resultJSON, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		ownerErr = apperror.New(apperror.KindInternal, "backend.operations.owner_result_invalid", marshalErr, nil)
		resultJSON = []byte("{}")
	}
	receipt.Result = resultJSON
	if ownerErr == nil {
		receipt.Command.Status = operationsmodel.OperationsStatusSucceeded
		receipt.NextAction = "verify the owner resource state before continuing"
	} else {
		receipt.Command.Status = operationsmodel.OperationsStatusFailed
		receipt.ErrorCode = apperror.CodeOf(ownerErr)
		receipt.FailureClass = operationsOwnerFailureClass(apperror.KindOf(ownerErr))
		receipt.NextAction = "follow the owner runbook before retrying the same operation key"
	}
	finished, finishErr := s.Finish(ctx, receipt, operationsOwnerSystemScope())
	if finishErr != nil {
		return OperationsOwnerExecutionResult{}, finishErr
	}
	result.Value, result.Receipt = value, finished
	return result, ownerErr
}

func operationsOwnerFailureClass(kind apperror.ErrorKind) operationsmodel.OperationsFailureClass {
	switch kind {
	case apperror.KindUnavailable, apperror.KindRateLimited, apperror.KindInternal:
		return operationsmodel.OperationsFailureRetryable
	case apperror.KindConflict:
		return operationsmodel.OperationsFailureManualIntervention
	default:
		return operationsmodel.OperationsFailureTerminal
	}
}

func operationsOwnerSystemScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "operations.owner_execution")
}
