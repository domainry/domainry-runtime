package contract

import (
	"sort"
	"testing"
)

// An author writes ux.config from the schema.object contract alone. When that
// contract is a closed object naming fewer keys than the binding it decodes
// into, a correct model is refused at plan time for a key the contract never
// mentioned, which is what this guards.
func TestIdentityProfileExtensionConfigPublishesTheWholeBinding(t *testing.T) {
	schema := IdentityProfileExtensionConfigSchema()
	if schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		t.Fatalf("ux.config must stay a closed object")
	}
	for _, key := range []string{"identity_relation_field", "cardinality", "business_identity", "default_visibility", "binding_lifecycle", "summary_fields", "profile_tabs", "required_permissions", "standalone_workspace", "provenance"} {
		if _, declared := schema.Properties[key]; !declared {
			t.Errorf("ux.config does not declare %q, so a model that sets it is refused with no contract to read", key)
		}
	}
	required := append([]string(nil), schema.Required...)
	sort.Strings(required)
	want := []string{"business_identity", "default_visibility", "identity_relation_field"}
	if len(required) != len(want) {
		t.Fatalf("required = %v, want %v", required, want)
	}
	for index := range want {
		if required[index] != want[index] {
			t.Fatalf("required = %v, want %v", required, want)
		}
	}
	businessIdentity, declared := schema.Properties["business_identity"]
	if !declared || len(businessIdentity.Required) != 1 || businessIdentity.Required[0] != "key" {
		t.Fatalf("business_identity must require key: %+v", businessIdentity)
	}
}
