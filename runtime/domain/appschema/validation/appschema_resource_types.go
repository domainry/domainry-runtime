package validation

func ApplicationSchemaBusinessResourceTypes() []string {
	return []string{
		"action", "agent", "automation_rule", "component", "connector", "dictionary", "entrypoint", "field",
		"identity_profile_binding", "integration_event_mapping", "object", "operation_state_example", "preference", "report", "report_export_control", "rule_set",
		"scheduler", "sensitive_field_policy", "skill", "surface", "validation", "view",
	}
}
