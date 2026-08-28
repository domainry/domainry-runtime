package operations

import (
	"context"
	"encoding/json"
	"strings"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

type OperationsOwnerExecutionRequest struct {
	Kind         string
	ResourceType string
	ResourceID   string
	Reason       string
	Reference    string
	Payload      any
	Key          string
}

type OperationsOwnerExecutionResult struct {
	Value    any
	Receipt  operationsmodel.OperationsReceipt
	Replayed bool
}

type DirectAuthoringUpsertRequest struct {
	CapabilityKey        string
	ResourceID           string
	BuilderTaskID        string
	IdempotencyKey       string
	ExpectedResourceHash string
	Payload              any
}

// ExecuteOwnerOperation closes the compatibility route gap without moving an
// owner's policy into Operations. Operations owns authorization intent,
// idempotent orchestration and the durable receipt; the callback remains the
// only place that may perform the owner transition.
func (s *OperationsApplicationService) ExecuteOwnerOperation(ctx context.Context, request OperationsOwnerExecutionRequest, principal principalmodel.Principal, execute func(context.Context) (any, error)) (OperationsOwnerExecutionResult, error) {
	definition, found := operationsprojection.OperationsDefinition(strings.TrimSpace(request.Kind))
	if !found || definition.ExecutionScope != operationsmodel.OperationsExecutionWorkspace || definition.ResourceType != strings.TrimSpace(request.ResourceType) {
		return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindBadRequest, "backend.operations.definition_mismatch", nil, nil)
	}
	permission := operationsOwnerPermission(definition.Permissions, principal)
	receipt, decision, err := s.Submit(ctx, OperationsSubmitRequest{
		Kind: request.Kind, Permission: permission, ResourceType: request.ResourceType,
		ResourceID: request.ResourceID, Reason: request.Reason, Reference: request.Reference, Payload: request.Payload,
	}, request.Key, principal)
	if err != nil {
		return OperationsOwnerExecutionResult{}, err
	}
	return s.executeOwnerReceipt(ctx, receipt, decision, execute)
}

// ExecuteDirectAuthoringUpsert provides one durable idempotency and optimistic
// concurrency envelope without taking ownership of an owner's authorization,
// validation, normalization, or mutation logic.
func (s *OperationsApplicationService) ExecuteDirectAuthoringUpsert(
	ctx context.Context,
	request DirectAuthoringUpsertRequest,
	principal principalmodel.Principal,
	authorize func(context.Context) error,
	currentHash func(context.Context) (string, bool, error),
	execute func(context.Context) (any, error),
) (OperationsOwnerExecutionResult, error) {
	request.CapabilityKey = strings.TrimSpace(request.CapabilityKey)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	request.BuilderTaskID = strings.TrimSpace(request.BuilderTaskID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.ExpectedResourceHash = strings.TrimSpace(request.ExpectedResourceHash)
	if request.CapabilityKey == "" || request.ResourceID == "" || request.BuilderTaskID == "" {
		return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindBadRequest, "backend.authoring.request_identity_required", nil, nil)
	}
	if request.IdempotencyKey == "" {
		return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindBadRequest, "backend.idempotency.key_required", nil, nil)
	}
	if request.ExpectedResourceHash == "" {
		// First creation cannot present a resource hash: the resource does not
		// exist yet, so no route can serve one. An absent expected hash is the
		// create-only assertion "the resource does not exist"; the
		// execution-time hash check below still rejects it with
		// backend.authoring.resource_hash_conflict when the resource already
		// exists, so optimistic concurrency for updates is unchanged.
		request.ExpectedResourceHash = "empty"
	}
	if trustedTaskID := requestcontext.RuntimeAuthoringBuilderTaskID(ctx); trustedTaskID != "" && trustedTaskID != request.BuilderTaskID {
		return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindForbidden, "backend.authoring.builder_task_mismatch", nil, nil)
	}
	if authorize == nil || currentHash == nil || execute == nil {
		return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindUnavailable, "backend.authoring.owner_contract_unavailable", nil, nil)
	}
	if err := authorize(ctx); err != nil {
		return OperationsOwnerExecutionResult{}, err
	}
	if s.directAuthoringProjection == nil {
		return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindUnavailable, "backend.authoring.success_projection_unavailable", nil, nil)
	}
	kind := "runtime.authoring.upsert." + request.CapabilityKey
	receipt, decision, err := s.submit(ctx, OperationsSubmitRequest{
		Kind: kind, Permission: "owner_enforced", ResourceType: "authoring_resource", ResourceID: request.ResourceID,
		Reason:  "direct authoring upsert " + request.CapabilityKey,
		Payload: map[string]any{"builder_task_id": request.BuilderTaskID, "expected_resource_hash": request.ExpectedResourceHash, "payload": request.Payload},
	}, request.IdempotencyKey, principal.UserID, operationsmodel.OperationsScope{WorkspaceID: principal.WorkspaceID, ResourceType: "authoring_resource", ResourceID: request.CapabilityKey + ":" + request.ResourceID})
	if err != nil {
		return OperationsOwnerExecutionResult{}, err
	}
	return s.executeOwnerReceipt(ctx, receipt, decision, func(executionContext context.Context) (any, error) {
		actualHash, found, hashErr := currentHash(executionContext)
		if hashErr != nil {
			return nil, hashErr
		}
		expected := request.ExpectedResourceHash
		if (expected == "empty" && found) || (expected != "empty" && strings.TrimSpace(actualHash) != expected) {
			return nil, apperror.New(apperror.KindConflict, "backend.authoring.resource_hash_conflict", nil, map[string]string{"expected": expected, "actual": strings.TrimSpace(actualHash)})
		}
		resource, executeErr := execute(executionContext)
		if executeErr != nil {
			return nil, executeErr
		}
		resourceHash, _, hashErr := currentHash(executionContext)
		if hashErr != nil {
			return nil, hashErr
		}
		if strings.TrimSpace(resourceHash) == "" {
			return nil, apperror.New(apperror.KindUnavailable, "backend.authoring.resource_projection_unavailable", nil, nil)
		}
		projection, projectionErr := s.directAuthoringProjection(executionContext, request.CapabilityKey, principal)
		if projectionErr != nil {
			return nil, projectionErr
		}
		if strings.TrimSpace(projection.SnapshotHash) == "" {
			return nil, apperror.New(apperror.KindUnavailable, "backend.authoring.snapshot_projection_unavailable", nil, nil)
		}
		return OperationsDirectAuthoringSuccessResult{
			Resource: resource, ResourceHash: resourceHash, SnapshotHash: projection.SnapshotHash,
			AvailableSuccessors: projection.AvailableSuccessors,
		}, nil
	})
}

func (s *OperationsApplicationService) executeOwnerReceipt(ctx context.Context, receipt operationsmodel.OperationsReceipt, decision operationsmodel.OperationsSubmissionDecision, execute func(context.Context) (any, error)) (OperationsOwnerExecutionResult, error) {
	result := OperationsOwnerExecutionResult{Receipt: receipt, Replayed: decision == operationsmodel.OperationsSubmissionReplay}
	if receipt.Command.Status == operationsmodel.OperationsStatusSucceeded {
		if len(receipt.Result) != 0 {
			if err := json.Unmarshal(receipt.Result, &result.Value); err != nil {
				return OperationsOwnerExecutionResult{}, apperror.New(apperror.KindInternal, "backend.operations.receipt_result_invalid", err, nil)
			}
		}
		return result, nil
	}
	if receipt.Command.Status == operationsmodel.OperationsStatusFailed {
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
	value, ownerErr := execute(ctx)
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

func operationsOwnerPermission(permissions []string, principal principalmodel.Principal) string {
	if principal.HasExactPermission("workspace.admin") {
		return "workspace.admin"
	}
	for _, permission := range permissions {
		if principal.HasExactPermission(permission) {
			return permission
		}
	}
	if len(permissions) > 0 {
		return strings.TrimSpace(permissions[0])
	}
	return ""
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
