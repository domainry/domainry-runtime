package capability

import "testing"

func TestRuntimeAuthoringCapabilitiesPublishOutputs(t *testing.T) {
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, definition := range domain.Capabilities {
			if definition.OutputSchema == nil {
				t.Errorf("%s has no output schema", definition.Key)
				continue
			}
			if definition.OutputSchema.Schema != "https://json-schema.org/draft/2020-12/schema" {
				t.Errorf("%s output schema is not draft 2020-12", definition.Key)
			} else if definition.OutputSchema.Type == "object" && (definition.OutputSchema.AdditionalProperties == nil || *definition.OutputSchema.AdditionalProperties) {
				t.Errorf("%s output object schema is not closed", definition.Key)
			} else if definition.OutputSchema.Type == "array" && definition.OutputSchema.Items == nil {
				t.Errorf("%s output array schema has no item schema", definition.Key)
			} else if definition.OutputSchema.Type != "object" && definition.OutputSchema.Type != "array" {
				t.Errorf("%s output schema has unsupported root type %q", definition.Key, definition.OutputSchema.Type)
			}
			if len(definition.OutputVariables) == 0 {
				t.Errorf("%s publishes no output variables", definition.Key)
			}
		}
	}
}
