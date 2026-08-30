package capability

import (
	"strings"
	"testing"
)

func TestMetadataAuthoringCatalogIsSourceControlledAndReadOnly(t *testing.T) {
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, capability := range domain.Capabilities {
			if capability.Execution == nil || capability.Execution.ChangeControl != "source_controlled_json" {
				continue
			}
			if len(capability.Execution.WriteSet) != 0 || len(capability.Execution.SideEffects) != 0 || capability.Execution.Transaction != "read_only_candidate_validation" {
				t.Errorf("%s exposes mutation semantics: %#v", capability.Key, capability.Execution)
			}
			if capability.Lifecycle != "source_controlled_json" || len(capability.AuditEvents) != 0 || capability.SystemDraftResourceType != "" || capability.SystemDraftResourceTypeInputJSONPointer != "" {
				t.Errorf("%s exposes an online publication lifecycle: %#v", capability.Key, capability)
			}
			if capability.ResourceOperations != nil {
				t.Errorf("%s exposes online resource operations: %#v", capability.Key, capability.ResourceOperations)
			}
			for _, route := range capability.ConfigurationRoutes {
				if strings.Contains(route, "/change-plans") || strings.Contains(route, "/versions") || strings.Contains(route, "/rollback") {
					t.Errorf("%s exposes retired route %q", capability.Key, route)
				}
			}
			for _, parameter := range capability.Parameters {
				if strings.HasPrefix(parameter.Key, "expected_") && strings.HasSuffix(parameter.Key, "_hash") {
					t.Errorf("%s exposes optimistic-lock parameter %q", capability.Key, parameter.Key)
				}
			}
			for _, item := range capability.Errors {
				if strings.HasPrefix(item.Code, "backend.change_plan.") {
					t.Errorf("%s exposes retired ChangePlan error %q", capability.Key, item.Code)
				}
			}
			if capability.InputSchema != nil {
				if _, found := capability.InputSchema.Properties["expected_schema_hash"]; found {
					t.Errorf("%s exposes optimistic-lock input schema", capability.Key)
				}
			}
			for _, example := range capability.Examples {
				if _, found := example.Value["expected_schema_hash"]; found {
					t.Errorf("%s example %q exposes optimistic-lock input", capability.Key, example.Name)
				}
			}
		}
	}
}
