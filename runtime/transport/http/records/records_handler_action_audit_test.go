package records

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type recordsAuditRepository struct {
	events       []auditmodel.AuditEvent
	options      []auditmodel.AuditOption
	eventQuery   auditmodel.AuditEventQuery
	optionQuery  auditmodel.AuditOptionQuery
	err          error
	errors       []error
	eventBatches [][]auditmodel.AuditEvent
	calls        int
}

func (*recordsAuditRepository) InsertAuditEvent(context.Context, string, auditmodel.AuditEvent) error {
	return nil
}
func (r *recordsAuditRepository) ListAuditEvents(_ context.Context, _ string, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	r.eventQuery = query
	index := r.calls
	r.calls++
	if index < len(r.errors) && r.errors[index] != nil {
		return nil, r.errors[index]
	}
	if index < len(r.eventBatches) {
		return r.eventBatches[index], nil
	}
	return r.events, r.err
}
func (*recordsAuditRepository) ListAuditEventsForSystem(context.Context, principalmodel.SystemScope, auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	return nil, nil
}
func (r *recordsAuditRepository) ListAuditOptions(_ context.Context, _ string, query auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	r.optionQuery = query
	return r.options, r.err
}

type recordsSchemaProvider struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

type recordsActionExecutionStore struct{}

type recordsActionExecutionTransaction struct{}

func (recordsActionExecutionStore) TryBeginExecution(_ context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	execution := request.Execution
	execution.ID, execution.LeaseOwner, execution.FencingToken = "execution-1", request.LeaseOwner, 1
	return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: execution}, nil
}

func (recordsActionExecutionStore) HeartbeatExecution(context.Context, string, string, int64, time.Time, time.Time) error {
	return nil
}

func (recordsActionExecutionStore) CompleteExecution(_ context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return completion.Execution, nil
}

func (recordsActionExecutionStore) CommitExecution(_ context.Context, _ []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return completion.Execution, nil
}

func (recordsActionExecutionStore) BeginExecutionTransaction(context.Context) (actioncontract.ActionExecutionTransaction, error) {
	return recordsActionExecutionTransaction{}, nil
}

func (recordsActionExecutionTransaction) Context(ctx context.Context) context.Context { return ctx }

func (recordsActionExecutionTransaction) Commit(_ context.Context, _ []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	return completion.Execution, nil
}

func (recordsActionExecutionTransaction) RollBack(context.Context) error { return nil }

func (p recordsSchemaProvider) SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return p.snapshot
}

func recordsHTTPPrincipal() principalmodel.Principal {
	permissions := []string{
		"customer.create", "customer.read", "customer.update", "customer.delete", "customer.import", "customer.export",
		"order.create", "order.read", "order.update", "order.delete", "order.import", "order.export",
		"customer.approve", "customer.approve_object",
	}
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}, RequestID: "request"}, accessfixture.Bundle{
		Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, "all"),
	})
}

func recordsActionService(actions []definitionmodel.ActionSchema, execute actionapplication.SystemOperationHandler, authorizationError error) *actionapplication.ActionApplicationService {
	system := actionapplication.NewSystemOperationCatalog(actionapplication.SystemOperationDescriptor{
		Key: "test.operation", Matches: func(definitionmodel.ActionSchema) bool { return true }, WriteOperation: "update",
	})
	executor := actionapplication.NewSystemOperationExecutor(system, actionapplication.SystemOperationBinding{Key: "test.operation", Handler: execute})
	registry := runtimeext.NewBusinessHandlerRegistry()
	registry.Freeze()
	return actionapplication.NewActionApplication(actionapplication.ActionApplicationDependencies{
		Catalog: actionapplication.NewActionCatalog(actions, system, registry), SystemOperations: executor,
		Authorization: actionapplication.ActionAuthorization{ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return definitionmodel.ObjectSchema{Key: "customer"}, authorizationError
		}},
		UnitOfWork: actionapplication.NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(recordsActionExecutionStore{})),
	})
}

func recordsHandlerForTest(principal principalmodel.Principal) (*RecordsHandler, *error) {
	serviceErr := new(error)
	handler := NewRecordsHandler(RecordsDependencies{
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			*serviceErr = errors.New(code)
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			*serviceErr = err
			w.WriteHeader(599)
		},
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, target any) bool {
			if err := json.NewDecoder(r.Body).Decode(target); err != nil {
				*serviceErr = err
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
	})
	return handler, serviceErr
}

func recordsRequest(method, target, body string, pathValues map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	for key, value := range pathValues {
		r.SetPathValue(key, value)
	}
	return r
}

func TestRecordsActionHandlersProjectInputsAndReplayHeaders(t *testing.T) {
	principal := recordsHTTPPrincipal()
	var bulkInvocation, recordInvocation, objectInvocation actionmodel.ActionInvocation
	actions := recordsActionService([]definitionmodel.ActionSchema{
		{Key: "customer.approve", ObjectKey: "customer", Kind: "record_update"},
		{Key: "customer.approve_object", ObjectKey: "customer", Kind: "object_operation"},
	}, func(_ context.Context, invocation actionmodel.ActionInvocation, action definitionmodel.ActionSchema, _ map[string]any) (actionapplication.ActionExecutionResult, error) {
		if invocation.Source == actionmodel.ActionSourceBulk {
			bulkInvocation = invocation
		}
		if invocation.RecordID != "" {
			if invocation.Source != actionmodel.ActionSourceBulk {
				recordInvocation = invocation
			}
			result := actionmodel.ActionResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, RecordID: invocation.RecordID, Message: "backend.action.idempotent_replay"}
			return actionapplication.ActionExecutionResult{Record: &result}, nil
		}
		objectInvocation = invocation
		result := actionmodel.ActionObjectResult{ActionKey: action.Key, ObjectKey: action.ObjectKey, Status: "success", Message: "backend.action.idempotent_replay"}
		return actionapplication.ActionExecutionResult{Object: &result}, nil
	}, nil)
	handler, serviceErr := recordsHandlerForTest(principal)
	handler.actions = actions

	for name, call := range map[string]func(*httptest.ResponseRecorder){
		"list": func(w *httptest.ResponseRecorder) {
			handler.listActions(w, recordsRequest("GET", "/records/customer/actions", "", map[string]string{"objectKey": " customer "}))
		},
		"bulk": func(w *httptest.ResponseRecorder) {
			r := recordsRequest("POST", "/bulk", `{"record_ids":["one"]}`, map[string]string{"objectKey": " customer ", "actionKey": " customer.approve "})
			r.Header.Set("Idempotency-Key", "bulk-key")
			handler.executeBulkAction(w, r)
		},
		"object": func(w *httptest.ResponseRecorder) {
			r := recordsRequest("POST", "/object", `{"data":{"approved":true}}`, map[string]string{"objectKey": " customer ", "actionKey": " customer.approve_object "})
			r.Header.Set("Idempotency-Key", "object-key")
			handler.executeObjectAction(w, r)
		},
		"record": func(w *httptest.ResponseRecorder) {
			r := recordsRequest("POST", "/record", `{"data":{"approved":true}}`, map[string]string{"objectKey": " customer ", "recordID": " one ", "actionKey": " customer.approve "})
			r.Header.Set("Idempotency-Key", "record-key")
			handler.executeAction(w, r)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			call(w)
			if w.Code != http.StatusOK || *serviceErr != nil {
				t.Fatalf("status=%d error=%v body=%s", w.Code, *serviceErr, w.Body.String())
			}
		})
	}
	if bulkInvocation.Source != actionmodel.ActionSourceBulk || objectInvocation.IdempotencyKey != "object-key" || recordInvocation.IdempotencyKey != "record-key" {
		t.Fatalf("bulk=%#v object=%#v record=%#v", bulkInvocation, objectInvocation, recordInvocation)
	}
	if _, exists := recordInvocation.Input["idempotency_key"]; exists {
		t.Fatalf("system idempotency leaked into business data: %#v", recordInvocation.Input)
	}
	if recordInvocation.RecordID != "one" || objectInvocation.ObjectKey != "customer" {
		t.Fatalf("record=%#v object=%#v", recordInvocation, objectInvocation)
	}
}

func TestRecordsActionHandlersRejectDecodeAndIdempotencyMismatch(t *testing.T) {
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.actions = recordsActionService(nil, nil, nil)

	w := httptest.NewRecorder()
	handler.executeBulkAction(w, recordsRequest("POST", "/bulk", `{`, map[string]string{}))
	if w.Code != http.StatusBadRequest || *serviceErr == nil {
		t.Fatalf("decode status=%d err=%v", w.Code, *serviceErr)
	}
	*serviceErr = nil
	w = httptest.NewRecorder()
	r := recordsRequest("POST", "/object", `{"idempotency_key":"payload"}`, map[string]string{"objectKey": "customer", "actionKey": "customer.approve"})
	r.Header.Set("Idempotency-Key", "header")
	handler.executeObjectAction(w, r)
	if w.Code != 599 || *serviceErr == nil {
		t.Fatalf("mismatch status=%d err=%v", w.Code, *serviceErr)
	}
}

func TestRecordsActionAndPermissionHandlersForwardApplicationErrors(t *testing.T) {
	want := errors.New("action unavailable")
	principal := recordsHTTPPrincipal()
	handler, serviceErr := recordsHandlerForTest(principal)
	handler.actions = recordsActionService([]definitionmodel.ActionSchema{{Key: "customer.approve", ObjectKey: "customer", Kind: "record_update"}}, nil, want)
	calls := []func(http.ResponseWriter, *http.Request){handler.listActions, handler.executeBulkAction, handler.executeObjectAction, handler.executeAction}
	for _, call := range calls {
		*serviceErr = nil
		w := httptest.NewRecorder()
		r := recordsRequest("POST", "/action", "", map[string]string{"objectKey": "customer", "recordID": "one", "actionKey": "customer.approve"})
		call(w, r)
		if w.Code != 599 || *serviceErr == nil {
			t.Fatalf("status=%d err=%v", w.Code, *serviceErr)
		}
	}

	unknownHandler, unknownErr := recordsHandlerForTest(principalmodel.Principal{})
	unknownHandler.permissions = appschemaapplication.NewApplicationSchemaQueryApplicationService(recordsSchemaProvider{}, nil)
	w := httptest.NewRecorder()
	unknownHandler.effectivePermissions(w, recordsRequest("GET", "/permissions", "", nil))
	if w.Code != 599 || *unknownErr == nil {
		t.Fatalf("permissions status=%d err=%v", w.Code, *unknownErr)
	}
}

func TestEffectivePermissionsAppliesRecordRLSOnceForEveryRecordAction(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "coach-a"}}, accessfixture.Bundle{
		Key:         "operator",
		Permissions: []string{"customer.read", "customer.complete", "customer.cancel", "customer.create"},
		DataPolicies: []accessfixture.DataPolicyFixture{{
			ObjectKey: "customer", Scope: "owner", Read: true, Write: true,
		}},
	},
	)
	actions := []definitionmodel.ActionSchema{
		{Key: "customer.complete", ObjectKey: "customer", Label: "Complete", Kind: "record_operation", AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"otp"}}},
		{Key: "customer.cancel", ObjectKey: "customer", Label: "Cancel", Kind: "record_operation"},
		{Key: "customer.create", ObjectKey: "customer", Label: "Create", Kind: "object_create"},
	}
	for _, test := range []struct {
		name       string
		owner      string
		wantRecord bool
		wantReason string
	}{
		{name: "owner", owner: "coach-a", wantRecord: true, wantReason: "identity_policy_allowed"},
		{name: "other owner", owner: "coach-b", wantRecord: false, wantReason: "data_scope_denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &recordsHTTPRepository{record: recordmodel.Record{ID: "customer-1", OwnerUserID: test.owner}, found: true}
			handler, serviceErr := recordsHandlerForTest(principal)
			handler.UseQueries(recordsHTTPApplication(repository))
			handler.permissions = appschemaapplication.NewApplicationSchemaQueryApplicationService(recordsSchemaProvider{snapshot: appschemamodel.ApplicationSchemaSnapshot{
				Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}}}}, Actions: actions,
			}}, nil)
			w := httptest.NewRecorder()
			handler.effectivePermissions(w, recordsRequest("GET", "/records/permissions/effective?object_key=customer&record_id=customer-1", "", nil))
			if w.Code != http.StatusOK || *serviceErr != nil {
				t.Fatalf("status=%d err=%v body=%s", w.Code, *serviceErr, w.Body.String())
			}
			var snapshot recordcontract.RecordFeaturePermissionSnapshot
			if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Actions) != 3 || repository.getCalls != 0 || repository.listCalls != 2 {
				t.Fatalf("actions=%+v unscoped_gets=%d scoped_queries=%d", snapshot.Actions, repository.getCalls, repository.listCalls)
			}
			for _, permission := range snapshot.Actions {
				if permission.Kind == "object_create" {
					if !permission.Allowed || permission.Reason != "identity_policy_allowed" {
						t.Fatalf("object action was affected by record RLS: %+v", permission)
					}
					continue
				}
				if permission.Allowed != test.wantRecord || permission.Reason != test.wantReason {
					t.Fatalf("record action=%+v want allowed=%v reason=%s", permission, test.wantRecord, test.wantReason)
				}
				if permission.Key == "customer.complete" && len(permission.AssuranceRequired) != 1 {
					t.Fatalf("assurance projection=%+v", permission)
				}
			}
		})
	}
}

func TestEffectivePermissionsRequiresCompleteRecordContext(t *testing.T) {
	handler, serviceErr := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.permissions = appschemaapplication.NewApplicationSchemaQueryApplicationService(recordsSchemaProvider{}, nil)
	w := httptest.NewRecorder()
	handler.effectivePermissions(w, recordsRequest("GET", "/records/permissions/effective?object_key=customer", "", nil))
	if w.Code != http.StatusBadRequest || *serviceErr == nil || (*serviceErr).Error() != "backend.permissions.record_context_required" {
		t.Fatalf("status=%d err=%v", w.Code, *serviceErr)
	}
}

func TestEffectivePermissionsRecordDecisionEdgePaths(t *testing.T) {
	permissionService := func(principal principalmodel.Principal, actions []definitionmodel.ActionSchema) *appschemaapplication.ApplicationSchemaQueryApplicationService {
		return appschemaapplication.NewApplicationSchemaQueryApplicationService(recordsSchemaProvider{snapshot: appschemamodel.ApplicationSchemaSnapshot{
			Objects: []definitionmodel.ObjectSchema{
				{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}}},
				{Key: "order"},
			},
			Actions: actions,
		}}, nil)
	}
	recordAction := definitionmodel.ActionSchema{
		Key: "customer.approve", ObjectKey: "customer", Kind: "record_operation",
	}
	principalWithPermissions := func(permissions ...string) principalmodel.Principal {
		return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}, RequestID: "request"}, accessfixture.Bundle{Key: "limited", Permissions: permissions})
	}

	t.Run("record decision is unnecessary", func(t *testing.T) {
		principal := principalWithPermissions("customer.create")
		accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
			role.DataPolicies = []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all", Read: true, Write: true}}
		})
		handler, serviceErr := recordsHandlerForTest(principal)
		handler.permissions = permissionService(principal, []definitionmodel.ActionSchema{
			{Key: "order.approve", ObjectKey: "order", Kind: "record_operation"},
			{Key: "customer.denied", ObjectKey: "customer", Kind: "record_operation"},
			{Key: "customer.create", ObjectKey: "customer", Kind: "object_create"},
		})
		w := httptest.NewRecorder()
		handler.effectivePermissions(w, recordsRequest("GET", "/records/permissions/effective?object_key=customer&record_id=one", "", nil))
		if w.Code != http.StatusOK || *serviceErr != nil {
			t.Fatalf("status=%d err=%v body=%s", w.Code, *serviceErr, w.Body.String())
		}
	})

	t.Run("record service is required", func(t *testing.T) {
		principal := principalWithPermissions("customer.approve")
		accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
			role.DataPolicies = []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all", Read: true, Write: true}}
		})
		handler, serviceErr := recordsHandlerForTest(principal)
		handler.permissions = permissionService(principal, []definitionmodel.ActionSchema{recordAction})
		w := httptest.NewRecorder()
		handler.effectivePermissions(w, recordsRequest("GET", "/records/permissions/effective?object_key=customer&record_id=one", "", nil))
		if w.Code != 599 || apperror.CodeOf(*serviceErr) != "backend.permissions.record_service_unavailable" {
			t.Fatalf("status=%d err=%v", w.Code, *serviceErr)
		}
	})

	t.Run("record lookup error is forwarded", func(t *testing.T) {
		principal := principalWithPermissions("customer.approve", "order.approve")
		accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
			role.DataPolicies = []accessfixture.DataPolicyFixture{{ObjectKey: "customer", Scope: "all", Read: true, Write: true}}
		})
		handler, serviceErr := recordsHandlerForTest(principal)
		handler.permissions = permissionService(principal, []definitionmodel.ActionSchema{
			recordAction,
			{Key: "customer.denied", ObjectKey: "customer", Kind: "record_operation"},
			{Key: "order.approve", ObjectKey: "order", Kind: "record_operation"},
		})
		handler.UseQueries(recordsHTTPApplication(&recordsHTTPRepository{err: errors.New("lookup unavailable")}))
		w := httptest.NewRecorder()
		handler.effectivePermissions(w, recordsRequest("GET", "/records/permissions/effective?object_key=customer&record_id=one", "", nil))
		if w.Code != 599 || apperror.CodeOf(*serviceErr) != "backend.permissions.record_lookup_failed" {
			t.Fatalf("status=%d err=%v", w.Code, *serviceErr)
		}
	})

	t.Run("denial only mutates matching allowed record actions", func(t *testing.T) {
		principal := principalWithPermissions("customer.approve", "order.approve", "customer.create")
		accessfixture.Mutate(&principal, func(role *accessfixture.Bundle) {
			role.DataPolicies = []accessfixture.DataPolicyFixture{
				{ObjectKey: "customer", Scope: "owner", Read: true, Write: true},
				{ObjectKey: "order", Scope: "all", Read: true, Write: true},
			}
		})
		handler, serviceErr := recordsHandlerForTest(principal)
		handler.permissions = permissionService(principal, []definitionmodel.ActionSchema{
			recordAction,
			{Key: "customer.denied", ObjectKey: "customer", Kind: "record_operation"},
			{Key: "order.approve", ObjectKey: "order", Kind: "record_operation"},
			{Key: "customer.create", ObjectKey: "customer", Kind: "object_create"},
		})
		handler.UseQueries(recordsHTTPApplication(&recordsHTTPRepository{
			record: recordmodel.Record{ID: "one", OwnerUserID: "someone-else"},
			found:  true,
		}))
		w := httptest.NewRecorder()
		handler.effectivePermissions(w, recordsRequest("GET", "/records/permissions/effective?object_key=customer&record_id=one", "", nil))
		if w.Code != http.StatusOK || *serviceErr != nil {
			t.Fatalf("status=%d err=%v body=%s", w.Code, *serviceErr, w.Body.String())
		}
		var snapshot recordcontract.RecordFeaturePermissionSnapshot
		if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		decisions := map[string]recordcontract.RecordFeatureActionPermission{}
		for _, action := range snapshot.Actions {
			decisions[action.Key] = action
		}
		if decisions["customer.approve"].Allowed || decisions["customer.approve"].Reason != "data_scope_denied" {
			t.Fatalf("matching action=%+v", decisions["customer.approve"])
		}
		if decisions["customer.denied"].Allowed {
			t.Fatalf("pre-denied action=%+v", decisions["customer.denied"])
		}
		if !decisions["order.approve"].Allowed || !decisions["customer.create"].Allowed {
			t.Fatalf("unrelated actions changed: %+v", decisions)
		}
	})
}

func TestRuntimeOpsEffectivePermissionsKeepOnlyExactGrants(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"runtime.operations.list_operations", "integration.events.replay"}})
	result := runtimeOpsExactFeaturePermissions(recordcontract.RecordFeaturePermissionSnapshot{Functions: []recordcontract.RecordFeatureFunctionPermission{
		{Key: "runtime.appschema.validate_application_definition", Decision: recordcontract.RecordFeaturePermissionDecision{Allowed: true, Reason: "allowed"}},
		{Key: "integration.invocations.list", Decision: recordcontract.RecordFeaturePermissionDecision{Allowed: true, Reason: "inherited"}},
		{Key: "runtime.operations.list_operations", Decision: recordcontract.RecordFeaturePermissionDecision{Allowed: true, Reason: "allowed"}},
		{Key: "integration.events.replay", Decision: recordcontract.RecordFeaturePermissionDecision{Allowed: true, Reason: "allowed"}},
	}}, principal)
	if len(result.Functions) != 2 || result.Functions[0].Key != "runtime.operations.list_operations" || result.Functions[1].Key != "integration.events.replay" {
		t.Fatalf("Runtime Ops exact permissions = %+v", result.Functions)
	}
	for _, permission := range result.Functions {
		if permission.Decision.Reason != "allowed" {
			t.Fatalf("exact permission reason = %q", permission.Decision.Reason)
		}
	}
}
