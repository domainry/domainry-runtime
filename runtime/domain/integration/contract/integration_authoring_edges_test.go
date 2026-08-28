package integrationcontract

import (
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationAuthoringRemainingOptionalAndSchemaEdges(t *testing.T) {
	if _, ok := SpecializeIntegrationConnectionAuthoringCapability("unsupported", integrationmodel.ConnectorSchema{}, "provider"); ok {
		t.Fatal("unsupported specialization key was accepted")
	}
	field := definitionmodel.FieldSchema{Key: "mode", Type: "text"}
	field.Validation.Options = []string{"safe"}
	if property := integrationFieldValueSchema(field); len(property.Enum) != 1 || property.Enum[0] != "safe" {
		t.Fatalf("property=%#v", property)
	}
	schema := integrationClosedObjectSchema([]capabilitycontract.CapabilityAuthoringParameter{{Key: "optional"}})
	if len(schema.Required) != 0 {
		t.Fatalf("schema=%#v", schema)
	}
	provider := &integrationmodel.ConnectorProviderSchema{
		ConfigFields: []definitionmodel.FieldSchema{{Key: "optional"}},
		SecretFields: []definitionmodel.FieldSchema{{Key: "token"}},
	}
	examples := integrationConnectionExamples("connector", "provider", provider)
	if len(examples) != 3 {
		t.Fatalf("examples=%#v", examples)
	}
	defaultValue := "configured"
	if got := integrationExampleFieldValue(definitionmodel.FieldSchema{Key: "status", Default: defaultValue}); got != defaultValue {
		t.Fatalf("default=%#v", got)
	}
	if integrationOptionalInt(1) == nil {
		t.Fatal("positive optional integer was discarded")
	}

	connector := integrationmodel.ConnectorSchema{Key: "connector"}
	operation := &integrationmodel.ConnectorOperationSchema{Key: "read", Output: []definitionmodel.FieldSchema{{Key: "optional"}}}
	definition := SpecializeIntegrationBindingValidationAuthoringCapability(connector, provider, operation)
	if definition.Key == "" || len(definition.Examples) != 3 {
		t.Fatalf("binding authoring definition=%#v", definition)
	}
}
