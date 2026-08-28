package capability

import "testing"

func TestRuntimeAuthoringCapabilitiesPublishOnlyCanonicalExamples(t *testing.T) {
	allowed := map[string]bool{"minimal_valid": true, "representative": true, "invalid_with_repair": true}
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, definition := range domain.Capabilities {
			// Seed input is closed only after object selection; its specialized
			// contract and examples are enforced by the BusinessSeed owner tests.
			if definition.Key == "seed.record" {
				continue
			}
			seen := map[string]bool{}
			invalidHasError := false
			for _, example := range definition.Examples {
				if !allowed[example.Name] || seen[example.Name] {
					t.Errorf("%s publishes unsupported or duplicate example %q", definition.Key, example.Name)
				}
				seen[example.Name] = true
				if example.Name == "invalid_with_repair" && len(example.ExpectedErrorCodes) > 0 {
					invalidHasError = true
				}
			}
			if !seen["minimal_valid"] || !seen["representative"] {
				t.Errorf("%s must publish minimal_valid and representative examples", definition.Key)
			}
			if len(definition.Errors) > 0 && !invalidHasError {
				t.Errorf("%s declares errors without invalid_with_repair evidence", definition.Key)
			}
		}
	}
}
