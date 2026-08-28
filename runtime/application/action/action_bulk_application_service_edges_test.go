package action

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type actionBulkExecutionStore struct {
	beginErr    error
	completeErr error
}

func (s *actionBulkExecutionStore) TryBeginExecution(_ context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	if s.beginErr != nil {
		return actionmodel.ActionExecutionClaimResult{}, s.beginErr
	}
	execution := request.Execution
	execution.ID, execution.LeaseOwner, execution.FencingToken = "execution-1", request.LeaseOwner, 1
	return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: execution}, nil
}

func (*actionBulkExecutionStore) HeartbeatExecution(context.Context, string, string, int64, time.Time, time.Time) error {
	return nil
}

func (s *actionBulkExecutionStore) CompleteExecution(_ context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return completion.Execution, s.completeErr
}

func (s *actionBulkExecutionStore) CommitExecution(_ context.Context, _ []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return completion.Execution, s.completeErr
}

func TestActionBulkAvailableActionBoundaryFailures(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	service := NewActionBulkApplicationService(ActionBulkDependencies{})
	if _, err := service.ActionsForObject(t.Context(), "order", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unauthorized actions error=%v", err)
	}
	want := errors.New("object validation failed")
	service.dependencies.ValidateObject = func(context.Context, principalmodel.Principal, string) error { return want }
	if _, err := service.ActionsForObject(t.Context(), "order", principal); !errors.Is(err, want) {
		t.Fatalf("object validation error=%v", err)
	}
	service.dependencies.ValidateObject = nil
	if actions, err := service.ActionsForObject(t.Context(), "order", principal); err != nil || actions == nil || len(actions) != 0 {
		t.Fatalf("nil catalog actions=%v error=%v", actions, err)
	}
	service.dependencies.Actions = func(context.Context) []definitionmodel.ActionSchema {
		return []definitionmodel.ActionSchema{{Key: "other", ObjectKey: "other"}, {Key: "denied", ObjectKey: "order"}}
	}
	if actions, err := service.ActionsForObject(t.Context(), "order", principal); err != nil || len(actions) != 0 {
		t.Fatalf("nil Allowed must expose no actions: %v error=%v", actions, err)
	}
	service.dependencies.ValidateObject = func(context.Context, principalmodel.Principal, string) error { return nil }
	service.dependencies.Allowed = func(_ principalmodel.Principal, action definitionmodel.ActionSchema) bool {
		return action.Key != "denied"
	}
	if actions, err := service.ActionsForObject(t.Context(), "order", principal); err != nil || len(actions) != 0 {
		t.Fatalf("denied catalog actions=%v err=%v", actions, err)
	}
	service.dependencies.Actions = func(context.Context) []definitionmodel.ActionSchema {
		return []definitionmodel.ActionSchema{{Key: "allowed", ObjectKey: "order"}}
	}
	if actions, err := service.ActionsForObject(t.Context(), "order", principal); err != nil || len(actions) != 1 {
		t.Fatalf("allowed catalog actions=%v err=%v", actions, err)
	}
}

func TestActionBulkExecutionInputAndDependencyFailures(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	actionDefinition := definitionmodel.ActionSchema{Key: "order.approve", ObjectKey: "order"}
	base := func() ActionBulkDependencies {
		return ActionBulkDependencies{
			Actions: func(context.Context) []definitionmodel.ActionSchema {
				return []definitionmodel.ActionSchema{actionDefinition}
			},
			Invoke: func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
				result := actionmodel.ActionResult{ActionKey: invocation.ActionKey, ObjectKey: invocation.ObjectKey, RecordID: invocation.RecordID}
				return actionmodel.ActionInvocationResult{Record: &result}, nil
			},
			Execution: actionruntime.NewActionExecutionRuntime(&actionBulkExecutionStore{}),
		}
	}
	request := actionmodel.ActionBulkRequest{RecordIDs: []string{"one"}, IdempotencyKey: "bulk-1"}
	assertCode := func(name, code string, dependencies ActionBulkDependencies, objectKey, actionKey string, current actionmodel.ActionBulkRequest, currentPrincipal principalmodel.Principal) {
		t.Helper()
		_, err := NewActionBulkApplicationService(dependencies).ExecuteBulkAction(t.Context(), objectKey, actionKey, current, currentPrincipal)
		if apperror.CodeOf(err) != code {
			t.Fatalf("%s error=%v code=%s", name, err, apperror.CodeOf(err))
		}
	}
	assertCode("authorization", "backend.workspace_scope_required", base(), "order", "order.approve", request, principalmodel.Principal{})
	dependencies := base()
	want := errors.New("object validation failed")
	dependencies.ValidateObject = func(context.Context, principalmodel.Principal, string) error { return want }
	if _, err := NewActionBulkApplicationService(dependencies).ExecuteBulkAction(t.Context(), "order", "order.approve", request, principal); !errors.Is(err, want) {
		t.Fatalf("validate object error=%v", err)
	}
	dependencies = base()
	dependencies.Actions = nil
	assertCode("missing catalog", "backend.action.not_found", dependencies, "order", "order.approve", request, principal)
	assertCode("missing action", "backend.action.not_found", base(), "order", "missing", request, principal)
	assertCode("object mismatch", "backend.action.object_mismatch", base(), "other", "order.approve", request, principal)
	dependencies = base()
	dependencies.Allowed = func(principalmodel.Principal, definitionmodel.ActionSchema) bool { return false }
	assertCode("permission", "backend.action.permission_denied", dependencies, "order", "order.approve", request, principal)
	dependencies = base()
	dependencies.ValidateObject = func(context.Context, principalmodel.Principal, string) error { return nil }
	dependencies.Allowed = func(principalmodel.Principal, definitionmodel.ActionSchema) bool { return true }
	if result, err := NewActionBulkApplicationService(dependencies).ExecuteBulkAction(t.Context(), "order", "order.approve", request, principal); err != nil || result.Succeeded != 1 {
		t.Fatalf("allowed execution result=%+v err=%v", result, err)
	}
	empty := request
	empty.RecordIDs = []string{"", " "}
	assertCode("empty records", "backend.bulk_action.record_ids_required", base(), "order", "order.approve", empty, principal)
	tooMany := request
	tooMany.RecordIDs = make([]string, 201)
	for index := range tooMany.RecordIDs {
		tooMany.RecordIDs[index] = fmt.Sprintf("record-%03d", index)
	}
	assertCode("record limit", "backend.bulk_action.too_many_records", base(), "order", "order.approve", tooMany, principal)
	dependencies = base()
	dependencies.Invoke = nil
	assertCode("invoke dependency", "backend.internal", dependencies, "order", "order.approve", request, principal)
	missingKey := request
	missingKey.IdempotencyKey = " "
	assertCode("idempotency key", "backend.idempotency.key_required", base(), "order", "order.approve", missingKey, principal)
	dependencies = base()
	dependencies.Execution = nil
	assertCode("execution dependency", "backend.idempotency.receipt_unavailable", dependencies, "order", "order.approve", request, principal)
	dependencies = base()
	dependencies.Execution = actionruntime.NewActionExecutionRuntime(&actionBulkExecutionStore{beginErr: want})
	if _, err := NewActionBulkApplicationService(dependencies).ExecuteBulkAction(t.Context(), "order", "order.approve", request, principal); !errors.Is(err, want) {
		t.Fatalf("execution begin error=%v", err)
	}
}

func TestActionBulkExecutionItemAndCompletionFailures(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	want := errors.New("invoke failed")
	store := &actionBulkExecutionStore{}
	dependencies := ActionBulkDependencies{
		Actions: func(context.Context) []definitionmodel.ActionSchema {
			return []definitionmodel.ActionSchema{{Key: "order.approve", ObjectKey: "order"}}
		},
		Invoke: func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			if invocation.RecordID == "error" {
				return actionmodel.ActionInvocationResult{}, want
			}
			return actionmodel.ActionInvocationResult{}, nil
		},
		Execution: actionruntime.NewActionExecutionRuntime(store),
	}
	request := actionmodel.ActionBulkRequest{RecordIDs: []string{"error", "nil-result"}, IdempotencyKey: "bulk-items"}
	result, err := NewActionBulkApplicationService(dependencies).ExecuteBulkAction(t.Context(), "order", "order.approve", request, principal)
	if err != nil || result.Failed != 2 || result.Succeeded != 0 || len(result.Items) != 2 || result.Items[0].Code != "backend.internal" || result.Items[1].Code != "backend.internal" {
		t.Fatalf("failed items result=%+v error=%v", result, err)
	}
	store.completeErr = errors.New("complete failed")
	if _, err := NewActionBulkApplicationService(dependencies).ExecuteBulkAction(t.Context(), "order", "order.approve", actionmodel.ActionBulkRequest{RecordIDs: []string{"nil-result"}, IdempotencyKey: "bulk-complete"}, principal); !errors.Is(err, store.completeErr) {
		t.Fatalf("completion error=%v", err)
	}
	service := NewActionBulkApplicationService(ActionBulkDependencies{})
	if _, found := service.actionByKey(t.Context(), "missing"); found {
		t.Fatal("nil action catalog returned a definition")
	}
	service.dependencies.Actions = func(context.Context) []definitionmodel.ActionSchema { return nil }
	if _, found := service.actionByKey(t.Context(), "missing"); found {
		t.Fatal("missing action returned a definition")
	}
	if actionBulkErrorCode(errors.New("plain")) != "backend.internal" {
		t.Fatal("plain bulk error code mismatch")
	}
}
