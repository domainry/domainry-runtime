package service

import (
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
)

func TestRuntimePermissionGrantPolicyOverridesNormalizedDirectDefaults(t *testing.T) {
	tests := []struct {
		name, sourceKind, owner string
		want                    actioncontract.PermissionGrantMode
	}{
		{name: "agent tool", sourceKind: "agent_tool", owner: "module:agent", want: actioncontract.PermissionGrantBundled},
		{name: "agent collaboration", sourceKind: "agent_collaboration", owner: "module:agent", want: actioncontract.PermissionGrantBundled},
		{name: "service protocol", sourceKind: "service_protocol", owner: "module:integration", want: actioncontract.PermissionGrantInternal},
		{name: "metadata http", sourceKind: "module_http", owner: "module:metadata", want: actioncontract.PermissionGrantInternal},
		{name: "runtime discovery", sourceKind: "builtin_http", owner: "runtime:builtin", want: actioncontract.PermissionGrantInternal},
		{name: "ordinary http", sourceKind: "module_http", owner: "module:reports", want: actioncontract.PermissionGrantDirect},
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
			if definition.Permission.GrantMode != actioncontract.PermissionGrantDirect {
				t.Fatalf("normalization did not reproduce direct default: %s", definition.Permission.GrantMode)
			}
			got := applyRuntimePermissionGrantPolicy(definition)
			if got.Permission.GrantMode != test.want {
				t.Fatalf("grant mode=%s want=%s", got.Permission.GrantMode, test.want)
			}
		})
	}
}
