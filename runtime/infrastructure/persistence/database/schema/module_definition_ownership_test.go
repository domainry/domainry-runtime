package schema

import "testing"

func TestRuntimeApplicationSchemaDoesNotDeclareExtractedModuleDefinitionTables(t *testing.T) {
	for _, table := range metadataDefinitionTables() {
		switch table {
		case "_scheduler_definitions", "_identity_profile_binding_definitions",
			"_metadata_object_definitions", "_metadata_field_definitions", "_metadata_validation_definitions", "_metadata_action_definitions", "_metadata_dictionary_definitions",
			"_report_definitions", "_report_operation_state_examples",
			"_report_sensitive_field_policies", "_report_export_controls",
			"_agent_skill_definitions", "_agent_definitions", "_agent_task_definitions",
			"_agent_entrypoint_definitions", "_agent_service_principal_definitions":
			t.Fatalf("Runtime still declares extracted module table %q", table)
		}
	}
}
