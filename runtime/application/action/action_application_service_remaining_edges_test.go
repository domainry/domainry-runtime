package action

import accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

import (
	"context"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestActionApplicationDependencyAndNilReceiverBoundaries(t *testing.T) {
	customFailureAudit := func(context.Context, definitionmodel.ActionSchema, actionmodel.ActionInvocation, actionmodel.ActionInvocationResult, error) []auditmodel.AuditEvent {
		return []auditmodel.AuditEvent{{ID: "custom-failure"}}
	}
	service := NewActionApplication(ActionApplicationDependencies{
		Audit: ActionAudit{BuildFailure: customFailureAudit},
	})
	if service.dependencies.Audit.BuildFailure == nil {
		t.Fatal("custom failure audit was replaced")
	}
	if err := service.bulk.dependencies.ValidateObject(t.Context(), actionTestPrincipal(), "order"); err != nil {
		t.Fatalf("nil object authorization should allow bulk preflight: %v", err)
	}

	var nilService *ActionApplicationService
	nilService.ReplaceDefinitions(nil)
	if definitions := nilService.Definitions(); definitions != nil {
		t.Fatalf("nil service definitions = %+v", definitions)
	}
	if failures := nilService.CatalogValidationErrors(); failures != nil {
		t.Fatalf("nil service validation errors = %+v", failures)
	}

	noCatalog := &ActionApplicationService{}
	noCatalog.ReplaceDefinitions(nil)
	if definitions := noCatalog.Definitions(); definitions != nil {
		t.Fatalf("nil catalog definitions = %+v", definitions)
	}

	system := NewSystemOperationCatalog()
	valid := NewActionApplication(ActionApplicationDependencies{
		Catalog:          NewActionCatalog(nil, system, frozenEmptyHandlerRegistry(t)),
		SystemOperations: NewSystemOperationExecutor(system),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}),
	})
	_ = valid.CatalogValidationErrors()
	valid.ReplaceDefinitions(nil)
	if definitions := valid.Definitions(); len(definitions) != 0 {
		t.Fatalf("empty definitions = %+v", definitions)
	}
}

func TestActionApplicationInvocationInputBoundaries(t *testing.T) {
	principal := actionTestPrincipal("order.update", "order.create")
	if _, err := (&ActionApplicationService{}).Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		Principal: principal,
	}); apperror.CodeOf(err) != "backend.action.key_required" {
		t.Fatalf("missing action key error = %v", err)
	}

	recordAction := definitionmodel.ActionSchema{
		Key: "order.update", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate, RequiresPermission: "order.update",
	}
	objectAction := definitionmodel.ActionSchema{
		Key: "order.create", ObjectKey: "order", Kind: definitionmodel.ActionKindObjectCreate, RequiresPermission: "order.create",
	}
	service := &ActionApplicationService{dependencies: ActionApplicationDependencies{
		Catalog: &ActionCatalog{entries: map[string]ActionCatalogEntry{
			recordAction.Key: {Definition: recordAction},
			objectAction.Key: {Definition: objectAction},
		}},
	}}
	if _, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: recordAction.Key,
		Principal: principal,
	}); apperror.CodeOf(err) != "backend.action.object_action_required" {
		t.Fatalf("default object key error = %v", err)
	}
	if _, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: objectAction.Key,
		ObjectKey: objectAction.ObjectKey,
		RecordID:  "order-1",
		Principal: principal,
	}); apperror.CodeOf(err) != "backend.action.record_action_required" {
		t.Fatalf("record supplied to object action error = %v", err)
	}
	if _, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: "missing.action",
		Principal: principal,
	}); apperror.CodeOf(err) != "backend.action.not_found" {
		t.Fatalf("missing catalog action error = %v", err)
	}

	resolutionFailure := apperror.New(apperror.KindInternal, "backend.action.owner_resolution_failed", nil, nil)
	service.dependencies.Catalog.entries["broken.action"] = ActionCatalogEntry{
		Definition:      definitionmodel.ActionSchema{Key: "broken.action"},
		ResolutionError: resolutionFailure,
	}
	if _, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: "broken.action",
		Principal: principal,
	}); err == nil {
		t.Fatal("owner resolution failure was ignored")
	}

	service.dependencies.Catalog.entries["mismatch.action"] = ActionCatalogEntry{
		Definition: definitionmodel.ActionSchema{Key: "mismatch.action", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate},
	}
	if _, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: "mismatch.action",
		ObjectKey: "invoice",
		RecordID:  "invoice-1",
		Principal: principal,
	}); apperror.CodeOf(err) != "backend.action.object_mismatch" {
		t.Fatalf("object mismatch error = %v", err)
	}
}

func TestActionApplicationPanicBeforeUnitOfWorkIsNormalized(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.update", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate, RequiresPermission: "order.update",
	}
	service := &ActionApplicationService{dependencies: ActionApplicationDependencies{
		Catalog: &ActionCatalog{entries: map[string]ActionCatalogEntry{
			action.Key: {Definition: action},
		}},
	}}
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: action.Key,
		ObjectKey: action.ObjectKey,
		RecordID:  "order-1",
		Principal: actionTestPrincipal("order.update"),
	})
	if apperror.CodeOf(err) != "backend.action.handler_panicked" || result.Status != "failed" {
		t.Fatalf("panic result = %+v, err = %v", result, err)
	}
}

func TestActionApplicationRejectsEmptyOwnerResult(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.update", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate, RequiresPermission: "order.update",
	}
	system := NewSystemOperationCatalog(SystemOperationDescriptor{
		Key: "record.update", Kind: definitionmodel.ActionKindRecordUpdate, WriteOperation: "update",
	})
	executor := NewSystemOperationExecutor(system, SystemOperationBinding{
		Key: "record.update",
		Handler: func(context.Context, actionmodel.ActionInvocation, definitionmodel.ActionSchema, map[string]any) (ActionExecutionResult, error) {
			return ActionExecutionResult{}, nil
		},
	})
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog:          NewActionCatalog([]definitionmodel.ActionSchema{action}, system, frozenEmptyHandlerRegistry(t)),
		SystemOperations: executor,
		UnitOfWork:       newActionTestUnitOfWork().manager,
	})
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: action.Key,
		ObjectKey: action.ObjectKey,
		RecordID:  "order-1",
		Principal: actionTestPrincipal("order.update"),
	})
	if apperror.CodeOf(err) != "backend.action.result_invalid" || result.Status != "failed" {
		t.Fatalf("empty owner result = %+v, err = %v", result, err)
	}
}

func TestActionApplicationPayloadIdempotencyAndRecordVersion(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.update", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate,
		RequiresPermission: "order.update", AuditEvent: "order.updated",
	}
	system := NewSystemOperationCatalog(SystemOperationDescriptor{
		Key: "record.update", Kind: definitionmodel.ActionKindRecordUpdate, WriteOperation: "update",
	})
	executor := NewSystemOperationExecutor(system, SystemOperationBinding{
		Key: "record.update",
		Handler: func(_ context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, _ map[string]any) (ActionExecutionResult, error) {
			if invocation.IdempotencyKey != "payload-request" {
				t.Fatalf("idempotency key = %q", invocation.IdempotencyKey)
			}
			result := actionmodel.ActionResult{
				ActionKey: action.Key,
				ObjectKey: action.ObjectKey,
				RecordID:  invocation.RecordID,
				Record:    recordmodel.Record{ID: invocation.RecordID, UpdatedAt: "version-2"},
			}
			return ActionExecutionResult{Record: &result}, nil
		},
	})
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog:          NewActionCatalog([]definitionmodel.ActionSchema{action}, system, frozenEmptyHandlerRegistry(t)),
		SystemOperations: executor,
		UnitOfWork:       newActionTestUnitOfWork().manager,
	})
	result, err := service.Invoke(t.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: action.Key,
		ObjectKey: action.ObjectKey,
		RecordID:  "order-1",
		Input:     map[string]any{"idempotency_key": " payload-request "},
		Principal: actionTestPrincipal("order.update"),
	})
	if err != nil || result.RecordVersions["order-1"] != "version-2" || result.AuditEvent != "order.updated" {
		t.Fatalf("versioned result = %+v, err = %v", result, err)
	}
}

func TestActionFailureAuditFallbacksAndPermissionCombinations(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "order.update", ObjectKey: "order"}
	result := actionmodel.ActionInvocationResult{InvocationID: "invocation-1"}
	notFound := apperror.New(apperror.KindNotFound, "backend.record.not_found", nil, nil)
	principal := actionTestPrincipal()
	principal = accessfixture.WithMutation(principal, func(role *accessfixture.Bundle) {
		role.DataPolicies = []accessfixture.DataPolicyFixture{
			{ObjectKey: "order", Read: false, AuditDenial: true},
			{ObjectKey: "order", Read: true, AuditDenial: false},
			{ObjectKey: "other", Read: true, AuditDenial: true},
		}
	})
	invocation := actionmodel.ActionInvocation{
		RecordID:  "order-1",
		Principal: principal,
		Source:    actionmodel.ActionSourceHTTP,
	}
	if events := buildActionFailureAudits(t.Context(), action, invocation, result, notFound); events != nil {
		t.Fatalf("non-audited permissions emitted events: %+v", events)
	}

	invocation.Principal = accessfixture.WithMutation(invocation.Principal, func(role *accessfixture.Bundle) {
		role.DataPolicies = append(role.DataPolicies, accessfixture.DataPolicyFixture{
			ObjectKey: "order", Read: true, AuditDenial: true,
		})
	})
	events := buildActionFailureAudits(t.Context(), action, invocation, result, notFound)
	if len(events) != 1 || events[0].ObjectKey != "order" || events[0].RecordID != "order-1" {
		t.Fatalf("fallback denial audit = %+v", events)
	}
}

func TestActionExecutionUnknownOwnerAndResultProjection(t *testing.T) {
	service := &ActionApplicationService{}
	_, err := service.execute(t.Context(), governedActionExecution{
		entry: ActionCatalogEntry{
			Definition: definitionmodel.ActionSchema{Key: "unknown.owner"},
			Owner:      ActionOwner("unknown"),
		},
	})
	if err == nil {
		t.Fatal("unknown action owner was executed")
	}

	record := actionmodel.ActionResult{ActionKey: "order.update", RecordID: "order-1"}
	invocation := actionmodel.ActionInvocation{IdempotencyKey: "request-1", Source: actionmodel.ActionSourceHTTP}
	projected := invocationResultFromRecord(invocation, definitionmodel.ActionSchema{AuditEvent: "order.updated"}, record)
	if projected.Record == nil || projected.Record.RecordID != "order-1" || projected.InvocationID != "request-1" {
		t.Fatalf("record projection = %+v", projected)
	}

	service.dependencies.NewInvocationID = func(context.Context) string { return "generated" }
	if invocationID := service.invocationID(t.Context(), actionmodel.ActionInvocation{RequestID: "request-id"}); invocationID != "request-id" {
		t.Fatalf("request invocation ID = %q", invocationID)
	}
}

func TestActionApplicationBulkValidationUsesObjectAuthorization(t *testing.T) {
	called := false
	service := NewActionApplication(ActionApplicationDependencies{
		Authorization: ActionAuthorization{ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			called = true
			return definitionmodel.ObjectSchema{Key: "order"}, nil
		}},
	})
	if err := service.bulk.dependencies.ValidateObject(t.Context(), actionTestPrincipal(), "order"); err != nil || !called {
		t.Fatalf("bulk object authorization called=%v err=%v", called, err)
	}
	if _, err := service.bulk.dependencies.Invoke(t.Context(), actionmodel.ActionInvocation{
		ActionKey: "missing",
		Principal: actionTestPrincipal(),
	}); apperror.CodeOf(err) != "backend.action.not_found" {
		t.Fatalf("bulk invoke closure error = %v", err)
	}
}

func TestActionCatalogValidationSkipsBusinessExecutorWithoutBusinessOwners(t *testing.T) {
	system := NewRuntimeSystemOperationCatalog()
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog: NewActionCatalog([]definitionmodel.ActionSchema{{
			Key: "order.create", ObjectKey: "order", Kind: definitionmodel.ActionKindObjectCreate,
		}}, system, runtimeext.NewBusinessHandlerRegistry()),
		SystemOperations: NewSystemOperationExecutor(system),
	})
	_ = service.CatalogValidationErrors()
}

func TestActionCatalogValidationIncludesBusinessExecutorForBusinessOwners(t *testing.T) {
	service := &ActionApplicationService{dependencies: ActionApplicationDependencies{
		Catalog: &ActionCatalog{entries: map[string]ActionCatalogEntry{
			"booking.reserve": {
				Definition: definitionmodel.ActionSchema{Key: "booking.reserve"},
				Owner:      ActionOwnerBusinessHandler,
			},
		}},
		SystemOperations: NewSystemOperationExecutor(NewSystemOperationCatalog()),
		BusinessHandlers: newActionTestBusinessHandlerExecutor(BusinessHandlerExecutionDependencies{}),
		UnitOfWork:       NewActionUnitOfWorkManager(nil),
	}}
	_ = service.CatalogValidationErrors()
}

func TestActionAuthorizationDeniesMissingPermission(t *testing.T) {
	err := (ActionAuthorization{}).Validate(actionTestPrincipal(), definitionmodel.ActionSchema{
		Key: "order.update", RequiresPermission: "order.update",
	})
	if apperror.CodeOf(err) != "backend.action.permission_denied" {
		t.Fatalf("authorization error = %v", err)
	}
}

func TestActionApplicationExecuteBulkDelegates(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.update", ObjectKey: "order", Kind: definitionmodel.ActionKindRecordUpdate, RequiresPermission: "order.update",
	}
	system := NewRuntimeSystemOperationCatalog()
	service := NewActionApplication(ActionApplicationDependencies{
		Catalog:          NewActionCatalog([]definitionmodel.ActionSchema{action}, system, frozenEmptyHandlerRegistry(t)),
		SystemOperations: NewSystemOperationExecutor(system),
		UnitOfWork:       newActionTestUnitOfWork().manager,
	})
	if _, err := service.ExecuteBulkAction(t.Context(), "order", action.Key, actionmodel.ActionBulkRequest{}, actionTestPrincipal("order.update")); apperror.CodeOf(err) != "backend.bulk_action.record_ids_required" {
		t.Fatalf("bulk delegation error = %v", err)
	}
}

func TestActionSuccessAuditPreservesPublishedEvent(t *testing.T) {
	event := buildActionSuccessAudit(
		t.Context(),
		definitionmodel.ActionSchema{Key: "order.update", ObjectKey: "order", AuditEvent: "order.updated"},
		actionmodel.ActionInvocation{RecordID: "order-1", Principal: actionTestPrincipal()},
		actionmodel.ActionInvocationResult{InvocationID: "invocation-1", Status: "success"},
	)
	if event.Event != "order.updated" {
		t.Fatalf("audit event = %+v", event)
	}
}
