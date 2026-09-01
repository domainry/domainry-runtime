package projection

import (
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestDefaultObjectAuthorizationActionsOwnExactPermissions(t *testing.T) {
	definitions, err := DefaultActionsForObject(definitionmodel.ObjectSchema{Key: "sales.order", Name: "Sales order"}, "application:orders")
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 5 {
		t.Fatalf("definitions=%d", len(definitions))
	}
	registry := actioncontract.NewRegistry()
	for _, definition := range definitions {
		if definition.Permission == nil || definition.Permission.Key != definition.Key || definition.Permission.ResourceKey+"."+definition.Permission.ActionKey != definition.Key {
			t.Fatalf("definition is not exact: %+v", definition)
		}
		if definition.HTTP == nil || strings.Contains(definition.HTTP.DisplayRouteTemplate, "{objectKey}") || !strings.Contains(definition.HTTP.DisplayRouteTemplate, "/objects/sales.order/") {
			t.Fatalf("definition has no concrete object route: %+v", definition)
		}
		if err := registry.Register(definition); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	resolved, found := registry.ResolveNonHTTP("runtime_object_action", "sales.order.export")
	if !found || resolved.EffectClass != actioncontract.EffectRead {
		t.Fatalf("resolved=%+v found=%v", resolved, found)
	}
	if resolved, found := registry.ResolveHTTP("GET", "/objects/sales.order/records/export"); !found || resolved.Key != "sales.order.export" {
		t.Fatalf("concrete HTTP projection resolved=%+v found=%v", resolved, found)
	}
}

func TestDefaultActionsForObjectHonorsExplicitAndLifecycleCapabilities(t *testing.T) {
	explicit := definitionmodel.ObjectCapabilitySet{Read: true, Export: true}
	definitions, err := DefaultActionsForObject(definitionmodel.ObjectSchema{Key: "ledger", Capabilities: &explicit}, "application:ledger")
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 2 || definitions[0].Key != "ledger.read" || definitions[1].Key != "ledger.export" {
		t.Fatalf("explicit definitions=%+v", definitions)
	}
	all := definitionmodel.StandardObjectCapabilities()
	definitions, err = DefaultActionsForObject(definitionmodel.ObjectSchema{Key: "entry", Capabilities: &all, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}}, "application:ledger")
	if err != nil {
		t.Fatal(err)
	}
	for _, definition := range definitions {
		if definition.Key == "entry.update" || definition.Key == "entry.delete" {
			t.Fatalf("append-only object exposed %s", definition.Key)
		}
	}
}
