package runtime

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type authorizationTestModuleAdapter struct {
	action actioncontract.ActionDefinition
}

func (authorizationTestModuleAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (adapter authorizationTestModuleAdapter) Owner() string   { return adapter.action.Owner }
func (authorizationTestModuleAdapter) Name() string            { return "authorization_test" }
func (adapter authorizationTestModuleAdapter) Routes() []modulehttp.Route {
	return []modulehttp.Route{{Action: adapter.action}}
}
func (authorizationTestModuleAdapter) Handler() http.Handler { return http.NotFoundHandler() }

type authorizationPermissionRegistryStub struct {
	requests      []identitysdk.PermissionReconcileRequest
	receiptMutate func(*identitysdk.PermissionReconcileReceipt)
	reconcile     func(identitysdk.PermissionReconcileRequest) error
	snapshots     map[string]identitysdk.PermissionSourceSnapshot
}

func (registry *authorizationPermissionRegistryStub) CurrentSourceSnapshot(_ context.Context, request identitysdk.PermissionSourceSnapshotRequest) (identitysdk.PermissionSourceSnapshot, error) {
	if snapshot, found := registry.snapshots[request.SourceOwner]; found {
		snapshot.Definitions = append([]identitysdk.PermissionDefinition(nil), snapshot.Definitions...)
		return snapshot, nil
	}
	return identitysdk.PermissionSourceSnapshot{WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner}, nil
}

func (registry *authorizationPermissionRegistryStub) Reconcile(_ context.Context, request identitysdk.PermissionReconcileRequest) (identitysdk.PermissionReconcileReceipt, error) {
	registry.requests = append(registry.requests, request)
	if registry.reconcile != nil {
		if err := registry.reconcile(request); err != nil {
			return identitysdk.PermissionReconcileReceipt{}, err
		}
	}
	if registry.snapshots == nil {
		registry.snapshots = map[string]identitysdk.PermissionSourceSnapshot{}
	}
	registry.snapshots[request.SourceOwner] = identitysdk.PermissionSourceSnapshot{
		WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner,
		SnapshotHash: request.SnapshotHash, Definitions: append([]identitysdk.PermissionDefinition(nil), request.Definitions...),
	}
	receipt := identitysdk.PermissionReconcileReceipt{
		WorkspaceID: request.Application.WorkspaceID, SourceOwner: request.SourceOwner,
		PreviousSnapshotHash: request.PreviousSnapshotHash, SnapshotHash: request.SnapshotHash,
		DefinitionCount: len(request.Definitions), Inserted: len(request.Definitions),
	}
	if registry.receiptMutate != nil {
		registry.receiptMutate(&receipt)
	}
	return receipt, nil
}

type authorizationIdentityBindingStub struct {
	runtimeIdentityBindingStub
	permissions identitysdk.PermissionRegistry
}

func (binding authorizationIdentityBindingStub) Permissions() identitysdk.PermissionRegistry {
	return binding.permissions
}

func TestRuntimeAuthorizationRegistryPublishesActionsWithoutObjectMetadataMirror(t *testing.T) {
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{
			{
				Key: "sales.customer", Name: "Customer",
				Fields: []definitionmodel.FieldSchema{{Key: "id"}, {Key: "created_at"}, {Key: "name"}},
			},
			{Key: "record_timer", Name: "Record timer", Config: map[string]any{"system_object": true}, Capabilities: &definitionmodel.ObjectCapabilitySet{Read: true}},
		},
		Actions: []definitionmodel.ActionSchema{{Key: "sales.customer.merge", ObjectKey: "sales.customer", Label: "Merge customer", Kind: definitionmodel.ActionKindRecordOperation, AuditEvent: "customer.merged"}},
		Workflows: []definitionmodel.WorkflowSchema{
			{Key: "order.approval", Name: "Order approval", Enabled: true},
			{Key: "order.disabled", Name: "Disabled order flow", Enabled: false},
		},
	}
	registry, err := runtimeAuthorizationActionRegistry(snapshot, "orders-runtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"sales.customer.create": true, "sales.customer.read": true, "sales.customer.update": true,
		"sales.customer.delete": true, "sales.customer.export": true, "sales.customer.merge": true,
		"record_timer.read": true,
	}
	for _, permission := range registry.PermissionDefinitions() {
		if !want[permission.Key] {
			continue
		}
		if permission.Key != permission.ResourceKey+"."+permission.OperationKey || permission.Owner != "application:orders-runtime" {
			t.Fatalf("permission is not exact/source-owned: %+v", permission)
		}
		delete(want, permission.Key)
	}
	if len(want) != 0 {
		t.Fatalf("missing object/action permissions: %#v", want)
	}
	workflowActionKey := workflowcontract.RunActionKey("order.approval")
	workflowAction, found := registry.Definition(workflowActionKey)
	if !found || workflowAction.Permission == nil || workflowAction.Permission.Key != workflowActionKey || workflowAction.Permission.Owner != "application:orders-runtime" {
		t.Fatalf("Workflow Action was not projected as a source-owned same-key Permission: %#v found=%v", workflowAction, found)
	}
	if _, found := registry.Definition(workflowcontract.RunActionKey("order.disabled")); found {
		t.Fatal("disabled Workflow unexpectedly produced an executable Action")
	}
	owned := runtimePermissionDefinitionsByOwner(registry)["application:orders-runtime"]
	found = false
	for _, permission := range owned {
		if permission.PermissionKey == workflowActionKey {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Workflow Permission missing from Identity owner snapshot: %#v", owned)
	}
}

func TestRuntimeAuthorizationReferencesUseCompleteGeneratedRegistry(t *testing.T) {
	moduleAction := testOwnedPermissionAction("module.orders.read", "module:test", "/module-orders-read")
	snapshot := appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "order", Name: "Order"}},
	}
	registry, err := runtimeAuthorizationActionRegistry(snapshot, "orders-runtime", []actioncontract.ActionDefinition{moduleAction})
	if err != nil {
		t.Fatal(err)
	}
	roles := []manifestmodel.RoleSchema{{Key: "operator", Permissions: []manifestmodel.RolePermission{{PermissionKey: "order.read", DataScope: identitysdk.DataScopeAll}, {PermissionKey: "module.orders.read", DataScope: identitysdk.DataScopeAll}, {PermissionKey: "runtime.appschema.metadata_object_record_count", DataScope: identitysdk.DataScopeAll}}}}
	if err := validateRuntimeAuthorizationReferences(snapshot, roles, registry); err != nil {
		t.Fatalf("generated object/module permissions rejected: %v", err)
	}
	roles[0].Permissions = append(roles[0].Permissions, manifestmodel.RolePermission{PermissionKey: "order.typo", DataScope: identitysdk.DataScopeAll})
	err = validateRuntimeAuthorizationReferences(snapshot, roles, registry)
	if err == nil || !strings.Contains(err.Error(), `roles[0].permissions[3].permission_key="order.typo"`) {
		t.Fatalf("orphan role permission error=%v", err)
	}
	var typed *AuthorizationReferenceError
	if !errors.As(err, &typed) || typed.KnownPermissionCount == 0 || len(typed.Diagnostics) != 1 {
		t.Fatalf("orphan permission did not expose structured diagnostics: %#v", err)
	}
	diagnostic := typed.Diagnostics[0]
	if diagnostic.Code != "runtime.authorization.permission_unknown" || diagnostic.Path != "roles[0].permissions[3].permission_key" || diagnostic.PermissionKey != "order.typo" || diagnostic.SourceKind != "role" {
		t.Fatalf("structured orphan permission diagnostic=%+v", diagnostic)
	}
	if envelope := typed.Diagnostic(); envelope["code"] != "runtime.authorization.references_invalid" {
		t.Fatalf("authorization diagnostic envelope=%v", envelope)
	}
}

func TestRuntimeAuthorizationRegistryUsesModuleActionAsSingleRouteAuthority(t *testing.T) {
	const (
		actionKey        = "notification.deliveries.list"
		staleEndpointKey = "runtime.notifications.list_deliveries"
	)
	action := actioncontract.ActionDefinition{
		Key: actionKey, Owner: "module:notification", SourceKind: "module_http",
		CapabilityKey: "notification.deliveries", CapabilityLabel: "Notification deliveries",
		OperationKey: "list", OperationLabel: "List deliveries", Label: "List notification deliveries",
		Exposures:     []actioncontract.Exposure{actioncontract.ExposureManagement, actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP:          &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: "/notification/deliveries"},
		Permission: &actioncontract.PermissionDefinition{
			Key: actionKey, Owner: "module:notification", ResourceKey: "notification.deliveries", OperationKey: "list",
			Label: "List notification deliveries", Category: "Notification deliveries", LifecycleStatus: actioncontract.LifecycleActive,
		},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow,
		IdempotencyDecision: "not_applicable", AuditClass: "notification_delivery_read",
		LifecycleStatus: actioncontract.LifecycleActive,
	}
	registry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "orders-runtime", []actioncontract.ActionDefinition{action})
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := registry.ResolveHTTP(http.MethodGet, "/notification/deliveries")
	if !ok || resolved.Key != actionKey || resolved.Permission == nil || resolved.Permission.Key != actionKey || resolved.Permission.Owner != "module:notification" {
		t.Fatalf("resolved module Action is not the source-owned exact contract: %+v, ok=%t", resolved, ok)
	}
	if _, exists := registry.Definition(staleEndpointKey); exists {
		t.Fatalf("stale Runtime endpoint Action %q survived module route ownership", staleEndpointKey)
	}
}

func TestRuntimeNotificationFacadeUsesRuntimeOwnedExactDeliveryPermission(t *testing.T) {
	registry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "orders-runtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	delivery, ok := registry.ResolveHTTP(http.MethodGet, "/notification/deliveries")
	if !ok || delivery.Key != "runtime.notifications.list_deliveries" || delivery.Owner != "runtime:notifications" {
		t.Fatalf("delivery Action=%+v ok=%t", delivery, ok)
	}
	if delivery.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || delivery.Permission == nil || delivery.Permission.Key != delivery.Key || delivery.Permission.Owner != delivery.Owner {
		t.Fatalf("delivery Action is not an exact same-key Runtime Permission: %+v", delivery)
	}
	for _, path := range []string{
		"/notification/inbox/{notificationID}/actions/{actionKey}/resolve",
		"/notification/inbox/{notificationID}/actions/{actionKey}/resolve",
	} {
		resolved, found := registry.ResolveHTTP(http.MethodGet, path)
		if !found || resolved.Owner != "runtime:notifications" || resolved.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || resolved.Permission != nil {
			t.Fatalf("resolved notification facade Action for %q=%+v found=%t", path, resolved, found)
		}
	}
}

func TestRuntimeWorkflowSurfaceSeparatesSelfRoutesFromExactManagementActions(t *testing.T) {
	registry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "orders-runtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	exactRoutes := []string{
		"GET /workflow/team-tasks",
		"POST /workflow/processes/{processID}/retry",
		"GET /workflow/operations/executions",
		"POST /workflow/operations/executions/process",
		"POST /workflow/operations/executions/{executionID}/retry",
		"POST /workflow/operations/executions/{executionID}/resolve",
		"GET /workflow/operations/processes",
		"GET /workflow/operations/processes/{processID}",
		"POST /workflow/operations/processes/{processID}/retry",
		"POST /workflow/operations/processes/{processID}/resolve",
		"POST /workflow/authoring-fragments/{capabilityKey}/validate",
		"POST /workflow/definitions/{workflowKey}/validate",
		"POST /workflow/definitions/{workflowKey}/simulate",
	}
	for _, endpoint := range exactRoutes {
		method, route, _ := strings.Cut(endpoint, " ")
		action, found := registry.ResolveHTTP(method, route)
		if !found || action.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || action.Permission == nil || action.Permission.Key != action.Key {
			t.Fatalf("workflow endpoint %q is not an exact same-key Action: %#v found=%v", endpoint, action, found)
		}
	}
	principalRoutes := []string{
		"GET /workflow/tasks",
		"GET /workflow/processes",
		"GET /workflow/processes/{processID}",
		"POST /workflow/processes/{processID}/withdraw",
		"POST /workflow/definitions/{workflowKey}/run",
		"POST /workflow/definitions/{workflowKey}/run",
	}
	for _, endpoint := range principalRoutes {
		method, route, _ := strings.Cut(endpoint, " ")
		action, found := registry.ResolveHTTP(method, route)
		if !found || action.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || action.Permission != nil {
			t.Fatalf("workflow endpoint %q is not principal/domain scoped: %#v found=%v", endpoint, action, found)
		}
	}
}

func TestRuntimeMetadataObjectRecordCountUsesSameKeyPermission(t *testing.T) {
	registry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "orders-runtime", nil)
	if err != nil {
		t.Fatal(err)
	}
	definition, ok := registry.ResolveHTTP(http.MethodGet, "/metadata/objects/{objectKey}/record-count")
	if !ok || definition.Key != "runtime.appschema.metadata_object_record_count" || definition.Owner != "runtime:appschema" {
		t.Fatalf("record-count Action=%+v ok=%t", definition, ok)
	}
	if definition.Authorization.Strategy != actioncontract.AuthorizationAuthenticated || definition.Permission == nil || definition.Permission.Key != definition.Key || definition.Permission.Owner != definition.Owner {
		t.Fatalf("record-count Action is not an exact same-key Runtime Permission: %+v", definition)
	}
}

func TestRuntimeAuthorizationRegistryUsesSchedulerManifestInsteadOfGeneratedHostKeys(t *testing.T) {
	actions, err := schedulersdk.SchedulerAuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	registry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "orders-runtime", actions)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range actions {
		resolved, found := registry.ResolveHTTP(source.HTTP.Method, source.HTTP.RouteTemplate)
		if !found || resolved.Key != source.Key || resolved.Owner != schedulersdk.SchedulerAuthorizationOwner || resolved.Permission == nil || resolved.Permission.Key != source.Key {
			t.Fatalf("Scheduler route %s %s resolved to %+v, found=%t", source.HTTP.Method, source.HTTP.RouteTemplate, resolved, found)
		}
		legacy := endpointmodel.EndpointContracts[source.HTTP.Method+" "+source.HTTP.RouteTemplate].ActionKey
		if legacy != source.Key {
			if _, found := registry.Definition(legacy); found {
				t.Fatalf("generated Scheduler host key %q survived source manifest projection", legacy)
			}
		}
	}
}

func TestRuntimeAuthorizationReconcileRejectsMismatchedSuccessReceipt(t *testing.T) {
	permissions := &authorizationPermissionRegistryStub{receiptMutate: func(receipt *identitysdk.PermissionReconcileReceipt) {
		receipt.SnapshotHash = "wrong-snapshot"
	}}
	binding := authorizationIdentityBindingStub{permissions: permissions}
	_, err := reconcileRuntimeIdentityAuthorization(t.Context(), binding, appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}},
	}, nil, nil, nil, "workspace-primary", "orders-runtime", nil)
	if err == nil {
		t.Fatal("Runtime accepted a reconcile receipt for a different snapshot")
	}
	var sdkError *identitysdk.Error
	if !errors.As(err, &sdkError) || sdkError.Code != "identity.permission_reconcile_receipt_invalid" {
		t.Fatalf("mismatched reconcile receipt error=%#v", err)
	}
}

func TestRuntimeAuthorizationReconcileCarriesPreviousHashAndRetiresRemovedOwner(t *testing.T) {
	action := actioncontract.ActionDefinition{
		Key: "test.module.read", Owner: "module:test", SourceKind: "module_http",
		CapabilityKey: "test.module", CapabilityLabel: "Test module", OperationKey: "read", OperationLabel: "Read", Label: "Read test module",
		Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic}, Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP:        &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: "/test-module"},
		Permission:  &actioncontract.PermissionDefinition{Key: "test.module.read", Owner: "module:test", ResourceKey: "test.module", OperationKey: "read", Label: "Read test module", Category: "Test", LifecycleStatus: actioncontract.LifecycleActive},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "test_module_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
	previousRegistry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "orders-runtime", []actioncontract.ActionDefinition{action})
	if err != nil {
		t.Fatal(err)
	}
	previousDefinitions := []identitysdk.PermissionDefinition{{
		PermissionKey: "test.module.read", ResourceKey: "test.module", OperationKey: "read", Label: "Read test module", Category: "Test", SourceKind: "module_http",
	}}
	wantPreviousHash, err := identitysdk.PermissionSnapshotHash("module:test", previousDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	permissions := &authorizationPermissionRegistryStub{snapshots: map[string]identitysdk.PermissionSourceSnapshot{
		"module:test": {WorkspaceID: "workspace-primary", SourceOwner: "module:test", SnapshotHash: wantPreviousHash, Definitions: previousDefinitions},
	}}
	binding := authorizationIdentityBindingStub{permissions: permissions}
	if _, err := reconcileRuntimeIdentityAuthorization(t.Context(), binding, appschemamodel.ApplicationSchemaSnapshot{}, nil, previousRegistry, nil, "workspace-primary", "orders-runtime", nil); err != nil {
		t.Fatal(err)
	}
	wantEmptyHash, err := identitysdk.PermissionSnapshotHash("module:test", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range permissions.requests {
		if request.SourceOwner != "module:test" {
			continue
		}
		if len(request.Definitions) != 0 || request.PreviousSnapshotHash != wantPreviousHash || request.SnapshotHash != wantEmptyHash {
			t.Fatalf("removed owner reconcile request=%+v", request)
		}
		return
	}
	t.Fatal("removed module owner was not reconciled with an empty snapshot")
}

func TestRuntimePermissionReconcileCompensatesEarlierOwnersOnBatchFailure(t *testing.T) {
	nextRegistry := actioncontract.NewRegistry()
	for _, definition := range []actioncontract.ActionDefinition{
		testOwnedPermissionAction("orders.customer.read", "application:orders-runtime", "/orders/customer"),
		testOwnedPermissionAction("test.module.read", "module:test", "/test-module"),
	} {
		if err := nextRegistry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	if err := nextRegistry.Freeze(); err != nil {
		t.Fatal(err)
	}
	previousDefinitions := []identitysdk.PermissionDefinition{{
		PermissionKey: "orders.customer.read", ResourceKey: "orders.customer", OperationKey: "read",
		Label: "Read previous customer snapshot", Category: "orders.customer", SourceKind: "test_adapter",
	}}
	previousHash, err := identitysdk.PermissionSnapshotHash("application:orders-runtime", previousDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	permissions := &authorizationPermissionRegistryStub{snapshots: map[string]identitysdk.PermissionSourceSnapshot{
		"application:orders-runtime": {
			WorkspaceID: "workspace-primary", SourceOwner: "application:orders-runtime",
			SnapshotHash: previousHash, Definitions: previousDefinitions,
		},
	}, reconcile: func(request identitysdk.PermissionReconcileRequest) error {
		if request.SourceOwner == "module:test" {
			return errors.New("module owner unavailable")
		}
		return nil
	}}
	application := identitysdk.ApplicationRef{WorkspaceID: "workspace-primary", ApplicationKey: "orders-runtime"}
	err = reconcileRuntimePermissionRegistries(t.Context(), permissions, application, nil, nextRegistry)
	if err == nil || !strings.Contains(err.Error(), "module owner unavailable") {
		t.Fatalf("reconcile error=%v", err)
	}
	if len(permissions.requests) != 3 {
		t.Fatalf("requests=%+v", permissions.requests)
	}
	forward, failed, compensation := permissions.requests[0], permissions.requests[1], permissions.requests[2]
	if forward.SourceOwner != "application:orders-runtime" || failed.SourceOwner != "module:test" {
		t.Fatalf("forward order=%+v", permissions.requests)
	}
	if compensation.SourceOwner != forward.SourceOwner || compensation.PreviousSnapshotHash != forward.SnapshotHash || len(compensation.Definitions) != 1 || compensation.Definitions[0].Label != previousDefinitions[0].Label {
		t.Fatalf("compensation=%+v forward=%+v", compensation, forward)
	}
}

type commandOnlyPermissionRegistry struct{}

func (commandOnlyPermissionRegistry) Reconcile(context.Context, identitysdk.PermissionReconcileRequest) (identitysdk.PermissionReconcileReceipt, error) {
	return identitysdk.PermissionReconcileReceipt{}, nil
}

func TestRuntimePermissionReconcileRequiresAuthoritativeSnapshotReader(t *testing.T) {
	registry := actioncontract.NewRegistry()
	if err := registry.Register(testOwnedPermissionAction("orders.customer.read", "application:orders-runtime", "/orders/customer")); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	err := reconcileRuntimePermissionRegistries(t.Context(), commandOnlyPermissionRegistry{}, identitysdk.ApplicationRef{
		WorkspaceID: "workspace-primary", ApplicationKey: "orders-runtime",
	}, nil, registry)
	if err == nil || !strings.Contains(err.Error(), "snapshot reader is unavailable") {
		t.Fatalf("missing snapshot reader error=%v", err)
	}
}

func testOwnedPermissionAction(key, owner, route string) actioncontract.ActionDefinition {
	resourceKey, operationKey := strings.TrimSuffix(key, ".read"), "read"
	return actioncontract.ActionDefinition{
		Key: key, Owner: owner, SourceKind: "test_adapter",
		CapabilityKey: resourceKey, CapabilityLabel: resourceKey, OperationKey: operationKey, OperationLabel: "Read", Label: "Read " + resourceKey,
		Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic}, Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP:        &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: route},
		Permission:  &actioncontract.PermissionDefinition{Key: key, Owner: owner, ResourceKey: resourceKey, OperationKey: "read", Label: "Read " + resourceKey, Category: resourceKey, LifecycleStatus: actioncontract.LifecycleActive},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "test_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

var _ modulehttp.Adapter = authorizationTestModuleAdapter{}
var _ identitysdk.Binding = authorizationIdentityBindingStub{}
