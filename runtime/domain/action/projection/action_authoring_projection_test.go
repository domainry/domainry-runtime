package projection

import "testing"

func TestActionAuthoringPublishesMetadataWithoutStepDSL(t *testing.T) {
	definition := ActionDefinitionAuthoringCapability()
	if definition.SimulationEndpoint != "" {
		t.Fatalf("Action definition still publishes Runtime simulation: %q", definition.SimulationEndpoint)
	}
	if len(ActionAuthoringDomain().Capabilities) != 1 {
		t.Fatalf("Action domain still publishes leaf Step capabilities: %#v", ActionAuthoringDomain().Capabilities)
	}
	payload := definition.InputSchema.Properties["payload"]
	for _, forbidden := range []string{"audit_event", "config", "steps", "step_type", "compensation_policy"} {
		if _, exists := payload.Properties[forbidden]; exists {
			t.Fatalf("Action authoring payload still publishes %q", forbidden)
		}
	}
	for _, redundant := range []string{"object_key", "name", "source_kind", "source_id"} {
		if _, exists := definition.InputSchema.Properties[redundant]; exists {
			t.Fatalf("Action authoring request still publishes redundant envelope field %q", redundant)
		}
	}
	for _, reference := range definition.ReferenceContracts {
		if reference.Kind == "capability_key" {
			t.Fatalf("Action authoring still references Step capability: %#v", reference)
		}
	}
}
