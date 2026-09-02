package policy

import (
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

// AutomationAuthoringDomain publishes Automation-owned authoring contracts.
func AutomationAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	capabilities := []capabilitycontract.CapabilityAuthoringDefinition{{
		Key: "automation.rule", Status: "supported", Lifecycle: "versioned_metadata",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "key", Type: "rule_key", Required: true}, {Key: "name", Type: "string", Required: true}, {Key: "object_key", Type: "object_key", Required: true},
			{Key: "enabled", Type: "boolean", Required: true}, {Key: "priority", Type: "integer"}, {Key: "trigger", Type: "automation_trigger", Required: true},
			{Key: "conditions", Type: "automation_condition_group"}, {Key: "instructions", Type: "array", ItemSchema: "automation_instruction", Required: true},
			{Key: "execution", Type: "automation_execution_policy"}, {Key: "audit_event", Type: "event_key"}, {Key: "i18n", Type: "object"}, {Key: "layout", Type: "automation_layout"},
		}, Permissions: []string{
			"runtime.appschema.validate_application_definition",
			"runtime.automation.validate_automation_rule",
			"runtime.automation.simulate_rule_candidate",
		}, AuditEvents: []string{"automation_rule_saved"},
		ValidationEndpoint: "POST /automation-rules/validate", SimulationEndpoint: "POST /automation-rules/simulate", ConfigurationRoutes: append([]string{"POST /automation-rules/validate", "POST /automation-rules/simulate"}, appschemacontract.VersionedApplicationDefinitionRoutes("automation_rule")...), Errors: []capabilitycontract.CapabilityAuthoringError{{Code: "backend.automation.instruction_type_invalid", FieldPath: "instructions[].type", ParameterKeys: []string{"instruction", "type"}, MessageKey: "backend.automation.instruction_type_invalid"}},
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "discovery", Path: "runtime/domain/capability/contract/capability_automation_contract.go", Symbol: "RuntimeAutomationCapabilities"},
			{Kind: "model", Path: "runtime/domain/automation/model/automation_schema.go", Symbol: "AutomationRuleSchema"},
			{Kind: "validation", Path: "runtime/domain/automation/validation/automation_definition_validator.go", Symbol: "AutomationDefinitionValidator.Validate"},
		},
	}}
	automationCompleteRuleAuthoringContract(&capabilities[0])
	capabilities = append(capabilities, automationAuthoringComponentCapabilities()...)
	return capabilitycontract.CapabilityAuthoringDomain{Key: "automation", Capabilities: capabilities}
}

func automationAuthoringComponentCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	automationSource := capabilitycontract.CapabilityAuthoringSource{Kind: "domain", Path: "runtime/domain/automation/model/automation_schema.go", Symbol: "AutomationRuleSchema"}
	components := []capabilitycontract.CapabilityAuthoringDefinition{
		{
			Key: "automation.trigger", Status: "supported", Lifecycle: "record_lifecycle",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "phase", Type: "string", Required: true, Enum: capabilitycontract.RuntimeAutomationCapabilities().Phases},
				{Key: "operation", Type: "string", Required: true, Enum: capabilitycontract.RuntimeAutomationCapabilities().Operations},
				{Key: "changed_fields", Type: "array", ItemSchema: "field_key"}, {Key: "from_state", Type: "string"},
				{Key: "to_state", Type: "string"}, {Key: "source", Type: "string"},
			}, Requires: []string{"automation.rule"}, ValidationEndpoint: "POST /automation-rules/authoring-fragments/{capabilityKey}/validate", Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/automation/validation/automation_definition_validator.go", Symbol: "validateAutomationTriggerFilters"}},
		},
		{
			Key: "automation.condition_group", Status: "supported", Lifecycle: "record_lifecycle",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "mode", Type: "string", Default: "all", Enum: []string{"all", "any"}}, {Key: "clauses", Type: "array", ItemSchema: "automation_condition_clause"},
				{Key: "groups", Type: "array", ItemSchema: "automation_condition_group"},
			}, Requires: []string{"automation.rule"}, ValidationEndpoint: "POST /automation-rules/authoring-fragments/{capabilityKey}/validate", Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "validation", Path: "runtime/domain/automation/validation/automation_definition_validator.go", Symbol: "validateAutomationConditionGroup"}},
		},
		{
			Key: "automation.execution_policy", Status: "supported", Lifecycle: "record_lifecycle",
			Parameters: []capabilitycontract.CapabilityAuthoringParameter{
				{Key: "mode", Type: "string", Enum: capabilitycontract.RuntimeAutomationCapabilities().ExecutionModes},
				{Key: "run_as", Type: "string", Enum: capabilitycontract.RuntimeAutomationCapabilities().RunAsModes},
				{Key: "result_notification", Type: "string", Enum: capabilitycontract.RuntimeAutomationCapabilities().ResultNotificationModes},
				{Key: "timeout_seconds", Type: "integer", Minimum: automationAuthoringFloatPointer(0)}, {Key: "max_depth", Type: "integer", Minimum: automationAuthoringFloatPointer(0)},
				{Key: "idempotency_keys", Type: "array", ItemSchema: "field_key"},
			}, Requires: []string{"automation.rule"}, ValidationEndpoint: "POST /automation-rules/authoring-fragments/{capabilityKey}/validate", Sources: []capabilitycontract.CapabilityAuthoringSource{automationSource},
		},
	}
	for index := range components {
		automationCompleteComponentAuthoringContract(&components[index])
	}
	return append(components,
		automationAuthoringInstruction("derive_fields", []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "fields", Type: "object", Required: true},
		}),
		automationAuthoringInstruction("assert", []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "source", Type: "reference", Required: true}, {Key: "operator", Type: "string", Required: true, Enum: []string{"empty", "eq", "future_date", "gt", "gte", "in", "lt", "lte", "ne", "not_empty"}}, {Key: "value", Type: "any"}, {Key: "error_code", Type: "string"},
		}),
		automationAuthoringInstruction("invoke_business_action", []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "action_key", Type: "action_key", Required: true}, {Key: "object_key", Type: "object_key"},
			{Key: "record_id", Type: "record_id"}, {Key: "input", Type: "object"},
		}),
		automationAuthoringInstruction("start_workflow", []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "workflow_key", Type: "workflow_key", Required: true}, {Key: "payload", Type: "object"},
		}),
		automationAuthoringInstruction("emit_event", []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "event_type", Type: "event_key", Required: true}, {Key: "object_key", Type: "object_key"},
			{Key: "record_id", Type: "record_id"}, {Key: "message", Type: "string"}, {Key: "metadata", Type: "object"},
		}),
	)
}

func automationAuthoringInstruction(instructionType string, configParameters []capabilitycontract.CapabilityAuthoringParameter) capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "key", Type: "instruction_key", Required: true}, {Key: "type", Type: "string", Required: true, Enum: []string{instructionType}},
		{Key: "name", Type: "string"}, {Key: "i18n", Type: "object"}, {Key: "mode", Type: "string"}, {Key: "result_alias", Type: "string"}, {Key: "on_error", Type: "string", Enum: []string{"continue", "fail"}},
		{Key: "config", Type: "automation_instruction_config", Required: true},
	}
	capability := capabilitycontract.CapabilityAuthoringDefinition{
		Key: "automation.instruction." + instructionType, Status: "supported", Lifecycle: "record_lifecycle",
		Parameters: parameters, Requires: []string{"automation.rule"}, Permissions: []string{"runtime.automation.validate_automation_authoring_fragment"}, ValidationEndpoint: "POST /automation-rules/authoring-fragments/{capabilityKey}/validate", Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/automation/model/automation_schema.go", Symbol: "AutomationInstructionSchema"}, {Kind: "validation", Path: "runtime/domain/automation/validation/automation_authoring_fragment_validation.go", Symbol: "AutomationValidateAuthoringFragment"}, {Kind: "runtime", Path: "runtime/application/automation/automation_instruction_dispatch_application_service.go", Symbol: "AutomationInstructionDispatchApplicationService.Execute"}},
	}
	capability.InputSchema = automationInstructionInputSchema(instructionType, parameters, configParameters)
	capability.OutputSchema = automationFragmentValidationOutputSchema()
	capability.OutputVariables = automationFragmentValidationOutputVariables()
	capability.Execution = &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"automation.rule"}, Transaction: "read_only_validation", Idempotency: "naturally_idempotent", PermissionModel: "runtime.automation.validate_automation_authoring_fragment", SideEffectLevel: "none"}
	capability.Errors = automationInstructionErrors(instructionType)
	capability.Examples = automationInstructionExamples(instructionType)
	return capability
}

func automationAuthoringFloatPointer(value float64) *float64 { return &value }

func automationInstructionInputSchema(instructionType string, parameters, configParameters []capabilitycontract.CapabilityAuthoringParameter) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	properties := map[string]capabilitycontract.CapabilityAuthoringSchema{}
	required := []string{}
	for _, parameter := range parameters {
		property := automationAuthoringParameterSchema(parameter)
		if parameter.Key == "config" {
			property = automationInstructionConfigSchema(configParameters)
		}
		properties[parameter.Key] = property
		if parameter.Required {
			required = append(required, parameter.Key)
		}
	}
	properties["type"] = capabilitycontract.CapabilityAuthoringSchema{Type: "string", Const: instructionType, Enum: []any{instructionType}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Properties: properties, Required: required}
}

func automationInstructionConfigSchema(parameters []capabilitycontract.CapabilityAuthoringParameter) capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	properties := map[string]capabilitycontract.CapabilityAuthoringSchema{}
	required := []string{}
	for _, parameter := range parameters {
		properties[parameter.Key] = automationAuthoringParameterSchema(parameter)
		if parameter.Required {
			required = append(required, parameter.Key)
		}
	}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: properties, Required: required}
}

func automationAuthoringParameterSchema(parameter capabilitycontract.CapabilityAuthoringParameter) capabilitycontract.CapabilityAuthoringSchema {
	property := capabilitycontract.CapabilityAuthoringSchema{Minimum: parameter.Minimum, Maximum: parameter.Maximum, Default: parameter.Default}
	switch parameter.Type {
	case "string", "instruction_key", "reference", "action_key", "object_key", "record_id", "workflow_key", "event_key":
		property.Type = "string"
	case "integer":
		property.Type = "integer"
	case "boolean":
		property.Type = "boolean"
	case "object":
		property.Type = "object"
	}
	for _, value := range parameter.Enum {
		property.Enum = append(property.Enum, value)
	}
	return property
}

func automationValidationOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	open := true
	issue := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"section", "field_path", "error_code", "message_key", "capability_key", "contract_version"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"section": {Type: "string"}, "instruction_key": {Type: "string"}, "field_path": {Type: "string"}, "error_code": {Type: "string"}, "message_key": {Type: "string"},
		"capability_key": {Type: "string"}, "contract_version": {Type: "string"}, "params": {Type: "object", AdditionalProperties: &open},
	}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"valid", "rule"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"valid": {Type: "boolean"}, "rule": {Type: "object"}, "errors": {Type: "array", Items: &issue},
	}}
}

func automationInstructionErrors(instructionType string) []capabilitycontract.CapabilityAuthoringError {
	codes := map[string]string{
		"derive_fields": "backend.automation.derive_fields_required", "assert": "backend.automation.assert_source_required",
		"invoke_business_action": "backend.automation.business_action_key_required", "start_workflow": "backend.automation.workflow_key_required", "emit_event": "backend.automation.instruction_key_invalid",
	}
	code := codes[instructionType]
	return []capabilitycontract.CapabilityAuthoringError{{Code: code, FieldPath: "instructions[].config", MessageKey: code}, {Code: "backend.automation.instruction_type_invalid", FieldPath: "instructions[].type", MessageKey: "backend.automation.instruction_type_invalid"}}
}

func automationInstructionExamples(instructionType string) []capabilitycontract.CapabilityAuthoringExample {
	valid := map[string]map[string]any{
		"derive_fields":          {"key": "derive", "type": "derive_fields", "name": "Derive order state", "on_error": "fail", "config": map[string]any{"fields": map[string]any{"status": "ready", "automation_source": "runtime"}}},
		"assert":                 {"key": "guard", "type": "assert", "name": "Require ready order", "on_error": "fail", "config": map[string]any{"source": "$record.status", "operator": "eq", "value": "ready", "error_code": "order.not_ready"}},
		"invoke_business_action": {"key": "complete", "type": "invoke_business_action", "name": "Complete order", "on_error": "fail", "config": map[string]any{"action_key": "order.complete", "object_key": "order", "record_id": "$record.id", "input": map[string]any{"source": "automation"}}},
		"start_workflow":         {"key": "approval", "type": "start_workflow", "name": "Start approval", "on_error": "fail", "config": map[string]any{"workflow_key": "order.approval", "payload": map[string]any{"record_id": "$record.id"}}},
		"emit_event":             {"key": "emit", "type": "emit_event", "name": "Emit completion", "on_error": "continue", "config": map[string]any{"event_type": "order.completed", "object_key": "order", "record_id": "$record.id", "message": "Order completed", "metadata": map[string]any{"source": "automation"}}},
	}[instructionType]
	minimal := automationAuthoringCopyMap(valid)
	representative := automationAuthoringCopyMap(valid)
	delete(minimal, "name")
	delete(minimal, "on_error")
	minimalConfig, _ := minimal["config"].(map[string]any)
	switch instructionType {
	case "derive_fields":
		minimalConfig["fields"] = map[string]any{"status": "ready"}
	case "assert":
		delete(minimalConfig, "value")
		delete(minimalConfig, "error_code")
	case "invoke_business_action":
		delete(minimalConfig, "object_key")
		delete(minimalConfig, "record_id")
		delete(minimalConfig, "input")
	case "start_workflow":
		delete(minimalConfig, "payload")
	case "emit_event":
		delete(minimalConfig, "object_key")
		delete(minimalConfig, "record_id")
		delete(minimalConfig, "message")
		delete(minimalConfig, "metadata")
	}
	invalid := automationAuthoringCopyMap(valid)
	config, _ := invalid["config"].(map[string]any)
	errorCode := automationInstructionErrors(instructionType)[0].Code
	switch instructionType {
	case "derive_fields":
		delete(config, "fields")
	case "assert":
		delete(config, "source")
	case "invoke_business_action":
		delete(config, "action_key")
	case "start_workflow":
		delete(config, "workflow_key")
	case "emit_event":
		invalid["key"] = ""
	}
	return []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: minimal}, {Name: "representative", Value: representative}, {Name: "invalid_with_repair", Value: invalid, ExpectedErrorCodes: []string{errorCode}}}
}

func automationAuthoringCopyMap(value map[string]any) map[string]any {
	copy := make(map[string]any, len(value))
	for key, item := range value {
		if mapped, ok := item.(map[string]any); ok {
			nested := make(map[string]any, len(mapped))
			for nestedKey, nestedValue := range mapped {
				nested[nestedKey] = nestedValue
			}
			copy[key] = nested
		} else {
			copy[key] = item
		}
	}
	return copy
}
