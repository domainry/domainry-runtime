package service

import (
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRuntimePermissionGrantPolicyClassifiesSecureDefaults(t *testing.T) {
	tests := []struct {
		name, sourceKind, owner string
		want                    actioncontract.PermissionGrantMode
	}{
		{name: "agent tool", sourceKind: "agent_tool", owner: "module:agent", want: actioncontract.PermissionGrantBundled},
		{name: "agent collaboration", sourceKind: "agent_collaboration", owner: "module:agent", want: actioncontract.PermissionGrantBundled},
		{name: "service protocol", sourceKind: "service_protocol", owner: "module:integration", want: actioncontract.PermissionGrantInternal},
		{name: "metadata http", sourceKind: "module_http", owner: "module:metadata", want: actioncontract.PermissionGrantInternal},
		{name: "runtime discovery", sourceKind: "builtin_http", owner: "runtime:builtin", want: actioncontract.PermissionGrantInternal},
		{name: "ordinary http", sourceKind: "module_http", owner: "module:reports", want: actioncontract.PermissionGrantInternal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition, err := actioncontract.NormalizeDefinition(actioncontract.ActionDefinition{
				Key: "catalog.read", Owner: test.owner, SourceKind: test.sourceKind,
				CapabilityKey: "catalog", CapabilityLabel: "Catalog", OperationKey: "read", OperationLabel: "Read", Label: "Read catalog",
				Exposures:     []actioncontract.Exposure{actioncontract.ExposureManagement},
				Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
				HTTP:          &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: "/catalog"},
				Permission: &actioncontract.PermissionDefinition{
					Key: "catalog.read", Owner: test.owner, ResourceKey: "catalog", OperationKey: "read", Label: "Read catalog", Category: "Catalog", LifecycleStatus: actioncontract.LifecycleActive,
				},
				EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "catalog_read", LifecycleStatus: actioncontract.LifecycleActive,
			})
			if err != nil {
				t.Fatal(err)
			}
			if definition.Permission.GrantMode != actioncontract.PermissionGrantInternal {
				t.Fatalf("normalization did not reproduce internal default: %s", definition.Permission.GrantMode)
			}
			got := applyRuntimePermissionGrantPolicy(definition)
			if got.Permission.GrantMode != test.want {
				t.Fatalf("grant mode=%s want=%s", got.Permission.GrantMode, test.want)
			}
		})
	}
}

func TestRuntimeOwnedRecordTimerPermissionsUseStableRuntimeOwner(t *testing.T) {
	registry, err := BuildAuthorizationRegistry(AuthorizationRegistryInput{
		ApplicationKey: "knowledge",
		Snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{
			{Key: "record_timer", Config: map[string]any{"runtime_owned": true, "system_kind": "record_timer"}},
			{Key: "customer"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	timer, ok := registry.Definition("record_timer.read")
	if !ok || timer.Permission == nil || timer.Owner != "runtime:record-timer" || timer.Permission.Owner != "runtime:record-timer" {
		t.Fatalf("record timer permission owner=%q permission=%#v", timer.Owner, timer.Permission)
	}
	customer, ok := registry.Definition("customer.read")
	if !ok || customer.Permission == nil || customer.Owner != "application:knowledge" || customer.Permission.Owner != "application:knowledge" {
		t.Fatalf("business object permission owner=%q permission=%#v", customer.Owner, customer.Permission)
	}
}
