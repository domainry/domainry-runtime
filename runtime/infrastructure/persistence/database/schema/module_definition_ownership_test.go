package schema

import "testing"

func TestRuntimeApplicationSchemaDoesNotDeclareExtractedModuleDefinitionTables(t *testing.T) {
	for _, table := range metadataDefinitionTables() {
		switch table {
		case "scheduler_definitions", "identity_profile_binding_definitions",
			"object_definitions", "field_definitions", "validation_definitions", "action_definitions", "dictionary_definitions",
			"report_definitions", "operation_state_example_definitions",
			"sensitive_field_policy_definitions", "report_export_control_definitions",
			"skill_definitions", "agent_definitions", "agent_task_definitions",
			"agent_entrypoint_definitions", "agent_service_principal_definitions":
			t.Fatalf("Runtime still declares extracted module table %q", table)
		}
	}
}
