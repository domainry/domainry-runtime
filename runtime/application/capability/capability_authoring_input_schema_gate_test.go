package capability

import (
	"testing"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func TestRuntimeAuthoringCapabilitiesPublishClosedInputSchemas(t *testing.T) {
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, definition := range domain.Capabilities {
			if definition.InputSchema == nil {
				t.Errorf("%s has no input schema", definition.Key)
				continue
			}
			if definition.InputSchema.Schema != "https://json-schema.org/draft/2020-12/schema" || definition.InputSchema.Type != "object" || definition.InputSchema.AdditionalProperties == nil || *definition.InputSchema.AdditionalProperties {
				t.Errorf("%s input schema is not a closed draft 2020-12 object", definition.Key)
			}
			assertAuthoringInputSchemaHasExplicitContainerStrategy(t, definition.Key, "$", definition.InputSchema)
		}
	}
}

func assertAuthoringInputSchemaHasExplicitContainerStrategy(t *testing.T, capabilityKey, path string, schema *capabilitycontract.CapabilityAuthoringSchema) {
	t.Helper()
	if schema == nil {
		return
	}
	// Property-bearing business objects must declare whether unknown fields are
	// accepted. A property-less object is an intentional free-form JSON map.
	if schema.Type == "object" && len(schema.Properties) > 0 && schema.AdditionalProperties == nil {
		t.Errorf("%s input schema object %s has no additionalProperties strategy", capabilityKey, path)
	}
	if schema.Type == "array" && schema.Items == nil {
		t.Errorf("%s input schema array %s has no item schema", capabilityKey, path)
	}
	for key, property := range schema.Properties {
		value := property
		assertAuthoringInputSchemaHasExplicitContainerStrategy(t, capabilityKey, path+"/"+key, &value)
	}
	for key, definition := range schema.Definitions {
		value := definition
		assertAuthoringInputSchemaHasExplicitContainerStrategy(t, capabilityKey, path+"/$defs/"+key, &value)
	}
	for index := range schema.OneOf {
		assertAuthoringInputSchemaHasExplicitContainerStrategy(t, capabilityKey, path+"/oneOf", &schema.OneOf[index])
	}
	for _, nested := range []struct {
		keyword string
		schema  *capabilitycontract.CapabilityAuthoringSchema
	}{{"if", schema.If}, {"then", schema.Then}, {"else", schema.Else}, {"contains", schema.Contains}} {
		assertAuthoringInputSchemaHasExplicitContainerStrategy(t, capabilityKey, path+"/"+nested.keyword, nested.schema)
	}
	assertAuthoringInputSchemaHasExplicitContainerStrategy(t, capabilityKey, path+"/items", schema.Items)
}
