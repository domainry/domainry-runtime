package action

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestActionApplicationUsesCatalogAndOneInvoke(t *testing.T) {
	called := map[string]bool{}
	system := NewSystemOperationCatalog(
		SystemOperationDescriptor{Key: "record.update", Matches: func(action definitionmodel.ActionSchema) bool { return action.Kind == "record_update" }, WriteOperation: "update"},
		SystemOperationDescriptor{Key: "record.create", Matches: func(action definitionmodel.ActionSchema) bool { return action.Kind == "object_create" }, WriteOperation: "create"},
	)
	executor := NewSystemOperationExecutor(system,
		SystemOperationBinding{Key: "record.update", Handler: func(_ context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, _ map[string]any) (ActionExecutionResult, error) {
			called["record"] = true
			result := actionmodel.ActionResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID}
			return ActionExecutionResult{Record: &result}, nil
		}},
		SystemOperationBinding{Key: "record.create", Handler: func(_ context.Context, _ actionmodel.ActionInvocation, action definitionmodel.ActionSchema, _ map[string]any) (ActionExecutionResult, error) {
			called["object"] = true
			result := actionmodel.ActionObjectResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, Status: "success"}
			return ActionExecutionResult{Object: &result}, nil
		}},
	)
	registry := runtimeext.NewProjectExtensionRegistry()
	registry.Freeze()
	actions := []definitionmodel.ActionSchema{
		{Key: "order.approve", ObjectKey: "order", Kind: "record_update"},
		{Key: "order.create", ObjectKey: "order", Kind: "object_create"},
	}
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog(actions, system, registry), SystemOperations: executor,
		UnitOfWork: newActionTestUnitOfWork().manager,
		Authorization: ActionAuthorization{ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "order"}, nil
		}},
		Audit: ActionAudit{BuildSuccess: func(context.Context, definitionmodel.ActionSchema, actionmodel.ActionInvocation, actionmodel.ActionInvocationResult) auditmodel.AuditEvent {
			called["audit"] = true
			return auditmodel.AuditEvent{ID: "action-audit", Event: "action.executed", WorkspaceID: "workspace-a", CreatedAt: "2026-07-22T00:00:00Z"}
		}},
	})
	principal := actionTestPrincipal("order.approve", "order.create")
	if listed, err := service.ActionsForObject(t.Context(), "order", principal); err != nil || len(listed) != 2 {
		t.Fatalf("actions=%#v error=%v", listed, err)
	}
	if result, err := service.Invoke(t.Context(), actionmodel.ActionSourceWorkflow, actionmodel.ActionInvocation{ActionKey: "order.approve", ObjectKey: "order", RecordID: "order-1", IdempotencyKey: "approve-1", Principal: principal, Source: actionmodel.ActionSourceAgent}); err != nil || result.Record == nil || result.Source != actionmodel.ActionSourceWorkflow {
		t.Fatalf("record result=%#v error=%v", result, err)
	}
	if result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: "order.create", ObjectKey: "order", IdempotencyKey: "create-1", Principal: principal}); err != nil || result.Object == nil {
		t.Fatalf("object result=%#v error=%v", result, err)
	}
	if _, err := service.Invoke(t.Context(), "", actionmodel.ActionInvocation{ActionKey: "order.create", ObjectKey: "order", Principal: principal}); apperror.CodeOf(err) != "backend.action.source_invalid" {
		t.Fatalf("empty source error=%v", err)
	}
	for _, name := range []string{"record", "object", "audit"} {
		if !called[name] {
			t.Fatalf("path %q was not called", name)
		}
	}
}

func TestActionApplicationPropagatesExecutorErrors(t *testing.T) {
	failure := errors.New("action execution failed")
	action := definitionmodel.ActionSchema{Key: "order.approve", ObjectKey: "order", Kind: "record_update"}
	system := NewSystemOperationCatalog(SystemOperationDescriptor{Key: "record.update", Matches: func(definitionmodel.ActionSchema) bool { return true }, WriteOperation: "update"})
	executor := NewSystemOperationExecutor(system, SystemOperationBinding{Key: "record.update", Handler: func(context.Context, actionmodel.ActionInvocation, definitionmodel.ActionSchema, map[string]any) (ActionExecutionResult, error) {
		return ActionExecutionResult{}, failure
	}})
	registry := runtimeext.NewProjectExtensionRegistry()
	registry.Freeze()
	service := NewActionApplication(ActionApplicationDependencies{Catalog: NewActionCatalog([]definitionmodel.ActionSchema{action}, system, registry), SystemOperations: executor, UnitOfWork: newActionTestUnitOfWork().manager})
	_, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: "order-1", IdempotencyKey: "approve-1", Principal: actionTestPrincipal("order.approve")})
	if !errors.Is(err, failure) {
		t.Fatalf("error=%v", err)
	}
}

func actionTestPrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Key: "operator", Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScopeAll)})
}
