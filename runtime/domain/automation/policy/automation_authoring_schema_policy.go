package policy

import (
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

func automationCompleteRuleAuthoringContract(capability *capabilitycontract.CapabilityAuthoringDefinition) {
	capability.SystemDraftResourceType = "automation_rule"
	capability.InputSchema = automationRuleInputSchema()
	capability.OutputSchema = automationFragmentValidationOutputSchema()
	capability.OutputVariables = automationFragmentValidationOutputVariables()
	capability.Execution = appschemacontract.VersionedApplicationDefinitionExecution("automation.rule")
	capability.ReferenceContracts = []capabilitycontract.CapabilityAuthoringReference{
		{Kind: "object_key", InputJSONPointer: "/object_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/object_key"},
		{Kind: "field_key", InputJSONPointer: "/trigger/changed_fields/*", ScopeFrom: "/object_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/field_key"},
		{Kind: "action_key", InputJSONPointer: "/instructions/*/config/action_key", ScopeFrom: "/object_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/action_key"},
		{Kind: "workflow_key", InputJSONPointer: "/instructions/*/config/workflow_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/workflow_key"},
	}
	capability.Errors = append(capability.Errors,
		capabilitycontract.CapabilityAuthoringError{Code: "backend.automation.identity_required", FieldPath: "key", MessageKey: "backend.automation.identity_required"},
		capabilitycontract.CapabilityAuthoringError{Code: "backend.automation.object_not_found", FieldPath: "object_key", MessageKey: "backend.automation.object_not_found"},
	)
	capability.Examples = automationRuleExamples()
}

func automationCompleteComponentAuthoringContract(capability *capabilitycontract.CapabilityAuthoringDefinition) {
	schemas := map[string]*capabilitycontract.CapabilityAuthoringSchema{
		"automation.trigger":          automationTriggerSchema(true),
		"automation.condition_group":  automationConditionGroupRootSchema(),
		"automation.execution_policy": automationExecutionPolicySchema(),
	}
	capability.InputSchema = schemas[capability.Key]
	capability.OutputSchema = automationValidationOutputSchema()
	capability.OutputVariables = automationValidationOutputVariables()
	capability.Permissions = []string{"runtime.automation.validate_automation_authoring_fragment"}
	capability.Execution = &capabilitycontract.CapabilityAuthoringExecution{
		ReadSet: []string{"automation.rule"}, Transaction: "read_only_validation", Idempotency: "naturally_idempotent",
		SideEffectLevel: "none", PermissionModel: "runtime.automation.validate_automation_authoring_fragment",
	}
	capability.Sources = append(capability.Sources, capabilitycontract.CapabilityAuthoringSource{Kind: "validation", Path: "runtime/domain/automation/validation/automation_authoring_fragment_validation.go", Symbol: "AutomationValidateAuthoringFragment"})
	capability.Errors = append(capability.Errors, automationComponentError(capability.Key))
	capability.Examples = automationComponentExamples(capability.Key)
}

func automationValidationOutputVariables() []capabilitycontract.CapabilityAuthoringOutput {
	return []capabilitycontract.CapabilityAuthoringOutput{{Name: "valid", JSONPointer: "/valid", Type: "boolean", VisibleTo: "subsequent_capability_calls"}, {Name: "normalized_rule", JSONPointer: "/rule", Type: "automation_rule", VisibleTo: "subsequent_capability_calls"}}
}

func automationFragmentValidationOutputVariables() []capabilitycontract.CapabilityAuthoringOutput {
	return []capabilitycontract.CapabilityAuthoringOutput{
		{Name: "valid", JSONPointer: "/valid", Type: "boolean", VisibleTo: "subsequent_capability_calls"},
		{Name: "validated_fragment", JSONPointer: "/fragment", Type: "automation_fragment", VisibleTo: "subsequent_capability_calls"},
	}
}

func automationFragmentValidationOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	open := true
	issue := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"section", "field_path", "error_code", "message_key", "capability_key", "contract_version"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"section": {Type: "string"}, "instruction_key": {Type: "string"}, "field_path": {Type: "string"}, "error_code": {Type: "string"}, "message_key": {Type: "string"},
		"capability_key": {Type: "string"}, "contract_version": {Type: "string"}, "params": {Type: "object", AdditionalProperties: &open},
	}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"valid", "capability_key", "fragment"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"valid": {Type: "boolean"}, "capability_key": {Type: "string"}, "fragment": {Type: "object", AdditionalProperties: &open}, "errors": {Type: "array", Items: &issue},
	}}
}

func automationRuleInputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	instructionItem := capabilitycontract.CapabilityAuthoringSchema{OneOf: []capabilitycontract.CapabilityAuthoringSchema{
		{Ref: "#/$defs/instruction_assert"}, {Ref: "#/$defs/instruction_derive_fields"}, {Ref: "#/$defs/instruction_emit_event"},
		{Ref: "#/$defs/instruction_invoke_business_action"}, {Ref: "#/$defs/instruction_start_workflow"},
	}}
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Required: []string{"key", "name", "object_key", "enabled", "trigger", "instructions"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"key": {Type: "string"}, "name": {Type: "string"}, "i18n": automationOpenObjectSchema(), "object_key": {Type: "string"},
			"enabled": {Type: "boolean"}, "priority": {Type: "integer"}, "trigger": {Ref: "#/$defs/trigger"},
			"conditions": {Ref: "#/$defs/condition_group"}, "instructions": {Type: "array", Items: &instructionItem},
			"execution": {Ref: "#/$defs/execution_policy"}, "audit_event": {Type: "string"}, "layout": automationLayoutSchema(),
		},
		Definitions: automationRuleSchemaDefinitions(),
	}
}

func automationRuleSchemaDefinitions() map[string]capabilitycontract.CapabilityAuthoringSchema {
	definitions := automationConditionDefinitions()
	definitions["trigger"] = *automationTriggerSchema(false)
	definitions["execution_policy"] = *automationExecutionPolicySchema()
	definitions["instruction_assert"] = *automationInstructionInputSchema("assert", automationInstructionBaseParameters("assert"), []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "source", Type: "reference", Required: true}, {Key: "operator", Type: "string", Required: true, Enum: []string{"empty", "eq", "future_date", "gt", "gte", "in", "lt", "lte", "ne", "not_empty"}}, {Key: "value", Type: "any"}, {Key: "error_code", Type: "string"},
	})
	definitions["instruction_derive_fields"] = *automationInstructionInputSchema("derive_fields", automationInstructionBaseParameters("derive_fields"), []capabilitycontract.CapabilityAuthoringParameter{{Key: "fields", Type: "object", Required: true}})
	definitions["instruction_invoke_business_action"] = *automationInstructionInputSchema("invoke_business_action", automationInstructionBaseParameters("invoke_business_action"), []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "action_key", Type: "action_key", Required: true}, {Key: "object_key", Type: "object_key"}, {Key: "record_id", Type: "record_id"}, {Key: "input", Type: "object"},
	})
	definitions["instruction_start_workflow"] = *automationInstructionInputSchema("start_workflow", automationInstructionBaseParameters("start_workflow"), []capabilitycontract.CapabilityAuthoringParameter{{Key: "workflow_key", Type: "workflow_key", Required: true}, {Key: "payload", Type: "object"}})
	definitions["instruction_emit_event"] = *automationInstructionInputSchema("emit_event", automationInstructionBaseParameters("emit_event"), []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "event_type", Type: "event_key", Required: true}, {Key: "object_key", Type: "object_key"}, {Key: "record_id", Type: "record_id"}, {Key: "message", Type: "string"}, {Key: "metadata", Type: "object"},
	})
	return definitions
}

func automationInstructionBaseParameters(instructionType string) []capabilitycontract.CapabilityAuthoringParameter {
	return []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "key", Type: "instruction_key", Required: true}, {Key: "type", Type: "string", Required: true, Enum: []string{instructionType}},
		{Key: "name", Type: "string"}, {Key: "i18n", Type: "object"}, {Key: "mode", Type: "string"}, {Key: "result_alias", Type: "string"},
		{Key: "on_error", Type: "string", Enum: []string{"continue", "fail"}}, {Key: "config", Type: "automation_instruction_config", Required: true},
	}
}

func automationTriggerSchema(root bool) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	stringItem := capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
	catalog := capabilitycontract.RuntimeAutomationCapabilities()
	schema := &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"phase", "operation"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"phase": {Type: "string", Enum: automationStringEnums(catalog.Phases)}, "operation": {Type: "string", Enum: automationStringEnums(catalog.Operations)},
		"changed_fields": {Type: "array", Items: &stringItem}, "from_state": {Type: "string"}, "to_state": {Type: "string"}, "source": {Type: "string"},
	}}
	if root {
		schema.Schema = "https://json-schema.org/draft/2020-12/schema"
	}
	return schema
}

func automationConditionGroupRootSchema() *capabilitycontract.CapabilityAuthoringSchema {
	definitions := automationConditionDefinitions()
	root := definitions["condition_group"]
	root.Schema = "https://json-schema.org/draft/2020-12/schema"
	root.Definitions = definitions
	return &root
}

func automationConditionDefinitions() map[string]capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	clause := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"reference", "operator"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"reference": {Type: "string"}, "operator": {Type: "string", Enum: automationStringEnums([]string{"empty", "eq", "future_date", "gt", "gte", "in", "lt", "lte", "ne", "not_empty"})}, "value": {},
	}}
	clauseItem := capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/condition_clause"}
	groupItem := capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/condition_group"}
	group := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"mode": {Type: "string", Default: "all", Enum: []any{"all", "any"}}, "clauses": {Type: "array", Items: &clauseItem}, "groups": {Type: "array", Items: &groupItem},
	}}
	return map[string]capabilitycontract.CapabilityAuthoringSchema{"condition_clause": clause, "condition_group": group}
}

func automationExecutionPolicySchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	stringItem := capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
	catalog := capabilitycontract.RuntimeAutomationCapabilities()
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"mode":                {Type: "string", Enum: automationStringEnums(catalog.ExecutionModes)},
		"run_as":              {Type: "string", Enum: automationStringEnums(catalog.RunAsModes)},
		"result_notification": {Type: "string", Enum: automationStringEnums(catalog.ResultNotificationModes)},
		"timeout_seconds":     {Type: "integer", Minimum: automationAuthoringFloatPointer(0)}, "max_depth": {Type: "integer", Minimum: automationAuthoringFloatPointer(0)},
		"idempotency_keys": {Type: "array", Items: &stringItem},
	}}
}

func automationLayoutSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"version"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"version": {Type: "integer"}, "nodes": automationOpenObjectSchema(), "viewport": automationOpenObjectSchema(),
	}}
}

func automationOpenObjectSchema() capabilitycontract.CapabilityAuthoringSchema {
	open := true
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
}

func automationStringEnums(values []string) []any {
	items := make([]any, len(values))
	for index := range values {
		items[index] = values[index]
	}
	return items
}

func automationComponentError(capabilityKey string) capabilitycontract.CapabilityAuthoringError {
	codes := map[string]string{
		"automation.trigger": "backend.automation.operation_invalid", "automation.condition_group": "backend.automation.condition_mode_invalid",
		"automation.execution_policy": "backend.automation.run_as_invalid",
	}
	fields := map[string]string{"automation.trigger": "trigger.operation", "automation.condition_group": "conditions.mode", "automation.execution_policy": "execution.run_as"}
	code := codes[capabilityKey]
	return capabilitycontract.CapabilityAuthoringError{Code: code, FieldPath: fields[capabilityKey], MessageKey: code}
}

func automationRuleExamples() []capabilitycontract.CapabilityAuthoringExample {
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"key": "order.prepare", "name": "Prepare order", "object_key": "order", "enabled": true, "trigger": map[string]any{"phase": "before", "operation": "create"}, "instructions": []any{map[string]any{"key": "derive", "type": "derive_fields", "config": map[string]any{"fields": map[string]any{"status": "ready"}}}}}},
		{Name: "representative", Value: map[string]any{"key": "order.complete", "name": "Complete order", "object_key": "order", "enabled": true, "priority": 10, "trigger": map[string]any{"phase": "after", "operation": "update", "changed_fields": []any{"status"}}, "conditions": map[string]any{"mode": "all", "clauses": []any{map[string]any{"reference": "$record.status", "operator": "eq", "value": "ready"}}}, "instructions": []any{map[string]any{"key": "complete", "type": "invoke_business_action", "config": map[string]any{"action_key": "order.complete"}}, map[string]any{"key": "emit", "type": "emit_event", "config": map[string]any{"event_type": "order.completed"}}}, "execution": map[string]any{"run_as": "initiator", "timeout_seconds": 30, "max_depth": 4, "idempotency_keys": []any{"status"}}, "audit_event": "automation.order.completed"}},
		{Name: "invalid_with_repair", Value: map[string]any{"key": "", "name": "Invalid", "object_key": "order", "enabled": true, "trigger": map[string]any{"phase": "before", "operation": "create"}, "instructions": []any{}}, ExpectedErrorCodes: []string{"backend.automation.identity_required"}},
	}
}

func automationComponentExamples(capabilityKey string) []capabilitycontract.CapabilityAuthoringExample {
	values := map[string][]map[string]any{
		"automation.trigger":          {{"phase": "after", "operation": "create"}, {"phase": "before", "operation": "update", "changed_fields": []any{"status"}, "source": "api"}, {"phase": "after", "operation": "merge"}},
		"automation.condition_group":  {{}, {"mode": "any", "clauses": []any{map[string]any{"reference": "$record.status", "operator": "eq", "value": "ready"}}, "groups": []any{map[string]any{"mode": "all", "clauses": []any{map[string]any{"reference": "$record.status", "operator": "not_empty"}}}}}, {"mode": "none"}},
		"automation.execution_policy": {{}, {"mode": "async", "run_as": "initiator", "result_notification": "failures", "timeout_seconds": 30, "max_depth": 4, "idempotency_keys": []any{"status"}}, {"run_as": "system"}},
	}
	examples := values[capabilityKey]
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: examples[0]}, {Name: "representative", Value: examples[1]},
		{Name: "invalid_with_repair", Value: examples[2], ExpectedErrorCodes: []string{automationComponentError(capabilityKey).Code}},
	}
}
