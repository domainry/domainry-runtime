package action

import (
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type ActionBulkDependencies struct {
	Actions        func(context.Context) []definitionmodel.ActionSchema
	Allowed        func(principalmodel.Principal, definitionmodel.ActionSchema) bool
	ValidateObject func(context.Context, principalmodel.Principal, string) error
	Invoke         func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
	AuditBulk      func(context.Context, actionmodel.ActionBulkResult, []string, principalmodel.Principal)
	Execution      *actionruntime.ActionExecutionRuntime
}

// ActionBulkApplicationService coordinates bulk action invocation and audit order.
type ActionBulkApplicationService struct {
	dependencies ActionBulkDependencies
}

func NewActionBulkApplicationService(dependencies ActionBulkDependencies) *ActionBulkApplicationService {
	return &ActionBulkApplicationService{dependencies: dependencies}
}

func (s *ActionBulkApplicationService) ActionsForObject(ctx context.Context, objectKey string, principal principalmodel.Principal) ([]definitionmodel.ActionSchema, error) {
	if err := actionAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	objectKey = strings.TrimSpace(objectKey)
	if s.dependencies.ValidateObject != nil {
		if err := s.dependencies.ValidateObject(ctx, principal, objectKey); err != nil {
			return nil, err
		}
	}
	actions := make([]definitionmodel.ActionSchema, 0)
	if s.dependencies.Actions == nil {
		return actions, nil
	}
	for _, definition := range s.dependencies.Actions(ctx) {
		if strings.TrimSpace(definition.ObjectKey) == objectKey && s.dependencies.Allowed != nil && s.dependencies.Allowed(principal, definition) {
			actions = append(actions, definition)
		}
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i].Key < actions[j].Key })
	return actions, nil
}

func (s *ActionBulkApplicationService) ExecuteBulkAction(ctx context.Context, objectKey, actionKey string, request actionmodel.ActionBulkRequest, principal principalmodel.Principal) (actionmodel.ActionBulkResult, error) {
	if err := actionAuthorizeCommand(principal); err != nil {
		return actionmodel.ActionBulkResult{}, err
	}
	objectKey, actionKey = strings.TrimSpace(objectKey), strings.TrimSpace(actionKey)
	if s.dependencies.ValidateObject != nil {
		if err := s.dependencies.ValidateObject(ctx, principal, objectKey); err != nil {
			return actionmodel.ActionBulkResult{}, err
		}
	}
	definition, exists := s.actionByKey(ctx, actionKey)
	if !exists {
		return actionmodel.ActionBulkResult{}, actionBulkApplicationError(apperror.KindNotFound, "backend.action.not_found")
	}
	if strings.TrimSpace(definition.ObjectKey) != objectKey {
		return actionmodel.ActionBulkResult{}, actionBulkApplicationError(apperror.KindBadRequest, "backend.action.object_mismatch")
	}
	if s.dependencies.Allowed != nil && !s.dependencies.Allowed(principal, definition) {
		return actionmodel.ActionBulkResult{}, actionBulkApplicationError(apperror.KindForbidden, "backend.action.permission_denied")
	}
	recordIDs := actionpolicy.ActionUniqueNonEmptyStrings(request.RecordIDs)
	if len(recordIDs) == 0 {
		return actionmodel.ActionBulkResult{}, actionBulkApplicationError(apperror.KindBadRequest, "backend.bulk_action.record_ids_required")
	}
	if len(recordIDs) > 200 {
		return actionmodel.ActionBulkResult{}, actionBulkApplicationError(apperror.KindBadRequest, "backend.bulk_action.too_many_records")
	}
	if s.dependencies.Invoke == nil {
		return actionmodel.ActionBulkResult{}, actionBulkApplicationError(apperror.KindInternal, "backend.internal")
	}
	if strings.TrimSpace(request.IdempotencyKey) == "" {
		return actionmodel.ActionBulkResult{}, actionBulkApplicationError(apperror.KindBadRequest, "backend.idempotency.key_required")
	}
	if s.dependencies.Execution == nil {
		return actionmodel.ActionBulkResult{}, actionBulkApplicationError(apperror.KindInternal, "backend.idempotency.receipt_unavailable")
	}
	cached, claim, replay, err := s.dependencies.Execution.BeginBulk(ctx, objectKey, actionKey, request.IdempotencyKey, request, principal)
	if err != nil {
		return actionmodel.ActionBulkResult{}, err
	}
	if replay {
		return cached, nil
	}

	result := actionmodel.ActionBulkResult{
		ActionKey: actionKey, ObjectKey: objectKey, Total: len(recordIDs),
		Message: "backend.bulk_action.executed", Items: make([]actionmodel.ActionBulkItemResult, 0, len(recordIDs)),
	}
	commandKey := strings.TrimSpace(request.IdempotencyKey)
	for _, recordID := range recordIDs {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		data := actionpolicy.ActionCloneData(request.Data)
		if expected, ok := request.ExpectedVersions[recordID]; ok {
			data["expected_version"] = expected
		}
		invocation, err := s.dependencies.Invoke(ctx, actionmodel.ActionInvocation{
			ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, Input: data, Principal: principal, Source: actionmodel.ActionSourceBulk,
			IdempotencyKey: commandKey + ":" + recordID,
		})
		if err != nil || invocation.Record == nil {
			if err == nil {
				err = actionBulkApplicationError(apperror.KindInternal, "backend.internal")
			}
			result.Failed++
			result.Items = append(result.Items, actionmodel.ActionBulkItemResult{RecordID: recordID, Code: actionBulkErrorCode(err), Error: err.Error()})
			continue
		}
		actionResult := *invocation.Record
		result.Succeeded++
		result.Items = append(result.Items, actionmodel.ActionBulkItemResult{RecordID: recordID, Success: true, Result: &actionResult})
	}
	if s.dependencies.AuditBulk != nil {
		s.dependencies.AuditBulk(ctx, result, recordIDs, principal)
	}
	if err := s.dependencies.Execution.Complete(ctx, claim, result); err != nil {
		return actionmodel.ActionBulkResult{}, err
	}
	return result, nil
}

func (s *ActionBulkApplicationService) actionByKey(ctx context.Context, key string) (definitionmodel.ActionSchema, bool) {
	if s.dependencies.Actions == nil {
		return definitionmodel.ActionSchema{}, false
	}
	for _, definition := range s.dependencies.Actions(ctx) {
		if strings.TrimSpace(definition.Key) == key {
			return definition, true
		}
	}
	return definitionmodel.ActionSchema{}, false
}

func actionBulkApplicationError(kind apperror.ErrorKind, code string) error {
	return &apperror.AppError{Kind: kind, Code: code}
}

func actionBulkErrorCode(err error) string {
	return apperror.CodeOf(err)
}
