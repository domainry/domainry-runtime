package action

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestActionApplicationAuthorizesWorkspaceBeforeExecutor(t *testing.T) {
	calls := 0
	action := definitionmodel.ActionSchema{Key: "order.approve", ObjectKey: "order", Kind: "record_update"}
	system := NewSystemOperationCatalog(SystemOperationDescriptor{Key: "record.update", Matches: func(definitionmodel.ActionSchema) bool { return true }, WriteOperation: "update"})
	executor := NewSystemOperationExecutor(system, SystemOperationBinding{Key: "record.update", Handler: func(context.Context, actionmodel.ActionInvocation, definitionmodel.ActionSchema, map[string]any) (ActionExecutionResult, error) {
		calls++
		return ActionExecutionResult{}, nil
	}})
	registry := runtimeext.NewBusinessHandlerRegistry()
	registry.Freeze()
	service := NewActionApplication(ActionApplicationDependencies{Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: executor})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
	checks := []func() error{
		func() error { _, err := service.ActionsForObject(t.Context(), "order", principal); return err },
		func() error {
			_, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: "order-1", Principal: principal})
			return err
		},
	}
	for index, check := range checks {
		if code := apperror.CodeOf(check()); code != "backend.workspace_scope_required" {
			t.Fatalf("check %d code=%q", index, code)
		}
	}
	if calls != 0 {
		t.Fatalf("executor called before workspace authorization: %d", calls)
	}
}
