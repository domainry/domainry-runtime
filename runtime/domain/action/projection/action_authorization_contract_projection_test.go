package projection

import (
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestAuthorizationActionDefinitionOwnsDedicatedPermissionWithoutInventingHTTP(t *testing.T) {
	schema := definitionmodel.ActionSchema{
		Key: "order.refund", ObjectKey: "order", Label: "Refund order", Kind: definitionmodel.ActionKindRecordOperation,
		AuditEvent: "order.refunded",
		AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{
			definitionmodel.ActionAssuranceMakerChecker, definitionmodel.ActionAssuranceWorkflowApproval,
		}},
	}
	definition, err := AuthorizationActionDefinition(schema, AuthorizationContractContext{
		Owner: "application:orders", CapabilityKey: "orders.actions", CapabilityLabel: "Order actions",
		PermissionCategory: "Order management", Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.HTTP != nil || len(definition.NonHTTP) != 1 || definition.NonHTTP[0].InvocationKey != schema.Key {
		t.Fatalf("bindings=%#v HTTP=%#v", definition.NonHTTP, definition.HTTP)
	}
	if definition.Permission == nil || definition.Permission.Key != schema.Key {
		t.Fatalf("permission=%#v", definition.Permission)
	}
	if len(definition.ApprovalPolicies) != 2 || definition.IdempotencyDecision != "caller_key_required" || definition.RiskLevel != actioncontract.RiskCritical || definition.AuditClass != "business_action" || definition.AuditEvent != schema.AuditEvent {
		t.Fatalf("governance=%#v", definition)
	}
	registry := actioncontract.NewRegistry()
	if err := registry.Register(definition); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	if resolved, ok := registry.ResolveNonHTTP("runtime_action", schema.Key); !ok || resolved.Key != schema.Key {
		t.Fatalf("resolved=%#v ok=%v", resolved, ok)
	}
}

func TestAuthorizationActionDefinitionUsesSameKeyPermission(t *testing.T) {
	schema := definitionmodel.ActionSchema{
		Key: "order.confirm", ObjectKey: "order", Label: "Confirm order", Kind: definitionmodel.ActionKindRecordUpdate,
		AuditEvent: "order.updated", OptimisticConcurrency: true,
	}
	definition, err := AuthorizationActionDefinition(schema, AuthorizationContractContext{
		Owner: "application:orders", CapabilityKey: "orders.actions", CapabilityLabel: "Order actions",
		PermissionCategory: "Order management", Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic},
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.Permission == nil || definition.Permission.Key != schema.Key {
		t.Fatalf("definition=%#v", definition)
	}
	if definition.IdempotencyDecision != "caller_key_required" || definition.RiskLevel != actioncontract.RiskMedium {
		t.Fatalf("governance=%#v", definition)
	}
}

func TestAuthorizationActionDefinitionKeepsDeprecatedActionPermissionActive(t *testing.T) {
	schema := definitionmodel.ActionSchema{
		Key: "order.refund", ObjectKey: "order", Label: "Refund order", Kind: definitionmodel.ActionKindRecordOperation,
		AuditEvent: "order.refunded",
	}
	definition, err := AuthorizationActionDefinition(schema, AuthorizationContractContext{
		Owner: "application:orders", CapabilityKey: "orders.actions", CapabilityLabel: "Order actions",
		PermissionCategory: "Order management", Exposures: []actioncontract.Exposure{actioncontract.ExposurePublic},
		LifecycleStatus: actioncontract.LifecycleDeprecated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if definition.LifecycleStatus != actioncontract.LifecycleDeprecated || definition.Permission.LifecycleStatus != actioncontract.LifecycleActive {
		t.Fatalf("definition=%#v", definition)
	}
}
