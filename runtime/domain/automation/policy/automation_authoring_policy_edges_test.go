package policy

import (
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func TestAutomationAuthoringParameterSchemaNumericAndBooleanTypes(t *testing.T) {
	for _, test := range []struct{ parameterType, want string }{{"integer", "integer"}, {"boolean", "boolean"}} {
		property := automationAuthoringParameterSchema(capabilitycontract.CapabilityAuthoringParameter{Type: test.parameterType})
		if property.Type != test.want {
			t.Fatalf("type %s mapped to %q", test.parameterType, property.Type)
		}
	}
}
