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
)

type authorizationTestModuleSurface struct {
	action actioncontract.ActionDefinition
}

func (authorizationTestModuleSurface) ContractVersion() string { return modulehttp.ContractVersion }
func (surface authorizationTestModuleSurface) Owner() string   { return surface.action.Owner }
func (authorizationTestModuleSurface) Name() string            { return "authorization_test" }
func (surface authorizationTestModuleSurface) Routes() []modulehttp.Route {
	return []modulehttp.Route{{Action: surface.action}}
}
func (authorizationTestModuleSurface) Handler() http.Handler { return http.NotFoundHandler() }

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
		if permission.Key != permission.ResourceKey+"."+permission.ActionKey || permission.Owner != "application:orders-runtime" {
			t.Fatalf("permission is not exact/source-owned: %+v", permission)
		}
		delete(want, permission.Key)
	}
	if len(want) != 0 {
		t.Fatalf("missing object/action permissions: %#v", want)
	}
}

func TestRuntimeAuthorizationRegistryUsesModuleActionAsSingleRouteAuthority(t *testing.T) {
	const (
		actionKey        = "notification.deliveries.list"
		staleEndpointKey = "runtime.notifications.list_deliveries"
	)
	action := actioncontract.ActionDefinition{
		Key: actionKey, Owner: "module:notification", SourceKind: "module_surface",
		CapabilityKey: "notification.deliveries", CapabilityLabel: "Notification deliveries",
		OperationKey: "list", OperationLabel: "List deliveries", Label: "List notification deliveries",
		Exposures:     []actioncontract.Exposure{actioncontract.ExposureTenantAdmin, actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationExactRolePermission},
		HTTP:          &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: "/notifications/deliveries"},
		Permission: &actioncontract.PermissionDefinition{
			Key: actionKey, Owner: "module:notification", ResourceKey: "notification.deliveries", ActionKey: "list",
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
	resolved, ok := registry.ResolveHTTP(http.MethodGet, "/notifications/deliveries")
	if !ok || resolved.Key != actionKey || resolved.Permission == nil || resolved.Permission.Key != actionKey || resolved.Permission.Owner != "module:notification" {
		t.Fatalf("resolved module Action is not the source-owned exact contract: %+v, ok=%t", resolved, ok)
	}
	if _, exists := registry.Definition(staleEndpointKey); exists {
		t.Fatalf("stale Runtime endpoint Action %q survived module route ownership", staleEndpointKey)
	}
}

func TestRuntimeAuthorizationReconcileRejectsMismatchedSuccessReceipt(t *testing.T) {
	permissions := &authorizationPermissionRegistryStub{receiptMutate: func(receipt *identitysdk.PermissionReconcileReceipt) {
		receipt.SnapshotHash = "wrong-snapshot"
	}}
	binding := authorizationIdentityBindingStub{permissions: permissions}
	_, err := reconcileRuntimeIdentityAuthorization(t.Context(), binding, appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}},
	}, nil, nil, "workspace-primary", "orders-runtime", nil)
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
		Key: "test.module.read", Owner: "module:test", SourceKind: "module_surface",
		CapabilityKey: "test.module", CapabilityLabel: "Test module", OperationKey: "read", OperationLabel: "Read", Label: "Read test module",
		Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic}, Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationExactRolePermission},
		HTTP:        &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: "/test-module"},
		Permission:  &actioncontract.PermissionDefinition{Key: "test.module.read", Owner: "module:test", ResourceKey: "test.module", ActionKey: "read", Label: "Read test module", Category: "Test", LifecycleStatus: actioncontract.LifecycleActive},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "test_module_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
	previousRegistry, err := runtimeAuthorizationActionRegistry(appschemamodel.ApplicationSchemaSnapshot{}, "orders-runtime", []actioncontract.ActionDefinition{action})
	if err != nil {
		t.Fatal(err)
	}
	previousDefinitions := []identitysdk.PermissionDefinition{{
		PermissionKey: "test.module.read", ResourceKey: "test.module", ActionKey: "read", Label: "Read test module", Category: "Test", SourceKind: "module_surface",
	}}
	wantPreviousHash, err := identitysdk.PermissionSnapshotHash("module:test", previousDefinitions)
	if err != nil {
		t.Fatal(err)
	}
	permissions := &authorizationPermissionRegistryStub{snapshots: map[string]identitysdk.PermissionSourceSnapshot{
		"module:test": {WorkspaceID: "workspace-primary", SourceOwner: "module:test", SnapshotHash: wantPreviousHash, Definitions: previousDefinitions},
	}}
	binding := authorizationIdentityBindingStub{permissions: permissions}
	if _, err := reconcileRuntimeIdentityAuthorization(t.Context(), binding, appschemamodel.ApplicationSchemaSnapshot{}, nil, previousRegistry, "workspace-primary", "orders-runtime", nil); err != nil {
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
		PermissionKey: "orders.customer.read", ResourceKey: "orders.customer", ActionKey: "read",
		Label: "Read previous customer snapshot", Category: "orders.customer", SourceKind: "test_surface",
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
		Key: key, Owner: owner, SourceKind: "test_surface",
		CapabilityKey: resourceKey, CapabilityLabel: resourceKey, OperationKey: operationKey, OperationLabel: "Read", Label: "Read " + resourceKey,
		Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic}, Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationExactRolePermission},
		HTTP:        &actioncontract.HTTPBinding{Method: http.MethodGet, RouteTemplate: route},
		Permission:  &actioncontract.PermissionDefinition{Key: key, Owner: owner, ResourceKey: resourceKey, ActionKey: "read", Label: "Read " + resourceKey, Category: resourceKey, LifecycleStatus: actioncontract.LifecycleActive},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "test_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

var _ modulehttp.Surface = authorizationTestModuleSurface{}
var _ identitysdk.Binding = authorizationIdentityBindingStub{}
