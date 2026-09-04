package openapi

import (
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	"sort"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func openAPISchemas(snapshot appschemamodel.ApplicationSchemaSnapshot) map[string]any {
	schemas := map[string]any{
		"Error": map[string]any{
			"type":       "object",
			"properties": map[string]any{"error": map[string]any{"type": "string"}, "request_id": map[string]any{"type": "string"}},
		},
		"SchemaSnapshot": openAPIObject(nil),
		"Record": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":         map[string]any{"type": "string"},
				"object_key": map[string]any{"type": "string"},
				"data":       openAPIObject(nil),
				"created_at": map[string]any{"type": "string", "format": "date-time"},
				"updated_at": map[string]any{"type": "string", "format": "date-time"},
				"localization": map[string]any{"type": "object", "properties": map[string]any{
					"requested_locale": map[string]any{"type": "string"},
					"fallback_locale":  map[string]any{"type": "string"},
					"field_sources":    map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				}},
			},
		},
		"PageResult": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items":     openAPIArray(openAPIRef("Record")),
				"total":     map[string]any{"type": "integer"},
				"page":      map[string]any{"type": "integer"},
				"page_size": map[string]any{"type": "integer"},
				"has_next":  map[string]any{"type": "boolean"},
			},
		},
		"ReferenceOption": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"id", "name"},
			"properties": map[string]any{
				"id": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
			},
		},
		"ReferenceOptionPage": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"items", "has_more"},
			"properties": map[string]any{
				"items": openAPIArray(openAPIRef("ReferenceOption")), "has_more": map[string]any{"type": "boolean"},
			},
		},
		"Action":              openAPIObject(nil),
		"ActionRequest":       map[string]any{"type": "object", "properties": map[string]any{"data": openAPIObject(nil)}},
		"ActionResult":        openAPIObject(nil),
		"ObjectActionRequest": map[string]any{"type": "object", "properties": map[string]any{"data": openAPIObject(nil)}},
		"ObjectActionResult":  openAPIObject(nil),
		"BulkActionRequest": map[string]any{
			"type":       "object",
			"required":   []string{"record_ids"},
			"properties": map[string]any{"record_ids": openAPIArray(map[string]any{"type": "string"}), "data": openAPIObject(nil), "expected_versions": openAPIObject(map[string]any{"type": "integer"})},
		},
		"AutomationRule":                openAPIObject(nil),
		"AutomationExecutionCatalog":    openAPIObject(nil),
		"AutomationSimulationRequest":   openAPIObject(nil),
		"AutomationSimulationResult":    openAPIObject(nil),
		"AgentTaskDefinition":           openAPIAgentTaskDefinitionSchema(),
		"AgentEntrypointAssignment":     openAPIAgentEntrypointAssignmentSchema(),
		"GlobalAgentContextContract":    openAPIGlobalAgentContextSchema(),
		"AgentRoutingContract":          openAPIAgentRoutingSchema(),
		"WorkflowAgentTaskNodeContract": openAPIWorkflowAgentTaskNodeSchema(),
		"InteractiveAgentHandoff":       openAPIInteractiveAgentHandoffSchema(),
	}
	for _, object := range snapshot.Objects {
		name := openAPIObjectSchemaName(object.Key)
		dataSchema := openAPIObjectDataSchema(object)
		requestProperties := map[string]any{"data": dataSchema}
		if translations := openAPIRecordTranslationsSchema(object); translations != nil {
			requestProperties["translations"] = translations
		}
		schemas[name+"Data"] = dataSchema
		schemas[name+"Record"] = map[string]any{
			"allOf": []map[string]any{
				openAPIRef("Record"),
				{"type": "object", "properties": map[string]any{"data": openAPIRef(name + "Data")}},
			},
		}
		schemas[name+"CreateRequest"] = map[string]any{"type": "object", "required": []string{"data"}, "properties": requestProperties}
		schemas[name+"UpdateRequest"] = map[string]any{"type": "object", "properties": requestProperties}
	}
	return schemas
}

func openAPIRecordTranslationsSchema(object definitionmodel.ObjectSchema) map[string]any {
	properties := map[string]any{}
	for _, field := range object.Fields {
		localized, _ := field.Config["localized"].(bool)
		if localized {
			properties[field.Key] = map[string]any{"type": "string"}
		}
	}
	if len(properties) == 0 {
		return nil
	}
	return map[string]any{
		"type":        "object",
		"description": "BCP 47 locale to localized business-record field values.",
		"additionalProperties": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           properties,
		},
	}
}

func openAPIAgentTaskDefinitionSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"contract_version", "key", "version", "agent_key", "instruction", "input_schema", "output_schema", "allowed_outcomes", "side_effect_mode", "enabled"},
		"properties": map[string]any{
			"contract_version": map[string]any{"type": "string", "enum": []string{agentsdk.AgentTaskContractVersion}},
			"key":              map[string]any{"type": "string"}, "version": map[string]any{"type": "string"}, "agent_key": map[string]any{"type": "string"},
			"name": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}, "instruction": map[string]any{"type": "string"},
			"input_schema": openAPIObject(nil), "output_schema": openAPIObject(nil),
			"allowed_objects": openAPIArray(map[string]any{"type": "string"}), "allowed_actions": openAPIArray(map[string]any{"type": "string"}),
			"allowed_outcomes": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": agentsdk.AgentTaskOutcomes}},
			"side_effect_mode": map[string]any{"type": "string", "enum": []string{agentsdk.AgentTaskSideEffectAnalysisOnly, agentsdk.AgentTaskSideEffectProposalOnly, agentsdk.AgentTaskSideEffectActionAllowed}},
			"execution_limits": openAPIObject(nil), "enabled": map[string]any{"type": "boolean"},
		},
	}
}

func openAPIGlobalAgentContextSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"contract_version", "max_selected_records", "max_context_bytes"},
		"properties": map[string]any{
			"contract_version":     map[string]any{"type": "string", "enum": []string{agentsdk.GlobalAgentContextContractVersion}},
			"allowed_hint_fields":  map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{"route_key", "object_key", "record_id", "selected_record_ids", "locale", "timezone"}}},
			"max_selected_records": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			"max_context_bytes":    map[string]any{"type": "integer", "minimum": 1024, "maximum": 1048576},
		},
	}
}

func openAPIAgentRoutingSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"contract_version", "allowed_route_types"},
		"properties": map[string]any{
			"contract_version":    map[string]any{"type": "string", "enum": []string{agentsdk.AgentRoutingContractVersion}},
			"allowed_route_types": map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow, agentsdk.AgentRouteProposal}}},
			"allow_recursive":     map[string]any{"type": "boolean", "enum": []bool{false}},
		},
	}
}

func openAPIAgentEntrypointAssignmentSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"contract_version", "key", "agent_key", "required_permissions", "route_patterns", "context_contract", "routing_contract", "enabled"},
		"properties": map[string]any{
			"contract_version": map[string]any{"type": "string", "enum": []string{agentsdk.AgentEntrypointContractVersion}},
			"key":              map[string]any{"type": "string"}, "agent_key": map[string]any{"type": "string"},
			"required_permissions": openAPIArray(map[string]any{"type": "string"}),
			"route_patterns":       openAPIArray(map[string]any{"type": "string"}), "allowed_task_keys": openAPIArray(map[string]any{"type": "string"}),
			"allowed_workflow_keys": openAPIArray(map[string]any{"type": "string"}), "context_contract": openAPIRef("GlobalAgentContextContract"),
			"routing_contract": openAPIRef("AgentRoutingContract"), "enabled": map[string]any{"type": "boolean"},
		},
	}
}

func openAPIWorkflowAgentTaskNodeSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"task_key", "task_version", "identity", "input", "output_variable", "execution_mode"},
		"properties": map[string]any{
			"task_key": map[string]any{"type": "string"}, "task_version": map[string]any{"type": "string"},
			"identity": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"mode"}, "properties": map[string]any{"mode": map[string]any{"type": "string", "enum": []string{agentsdk.AgentTaskIdentityInherit, agentsdk.AgentTaskIdentityService}}, "principal_key": map[string]any{"type": "string"}}},
			"input":    openAPIObject(nil), "output_variable": map[string]any{"type": "string"}, "execution_mode": map[string]any{"type": "string", "enum": []string{"async"}},
			"timeout_seconds": map[string]any{"type": "integer", "minimum": 1}, "retry": openAPIObject(nil), "on_error": map[string]any{"type": "string", "enum": []string{"", "fail", "error_branch"}},
			"allowed_objects": openAPIArray(map[string]any{"type": "string"}), "allowed_actions": openAPIArray(map[string]any{"type": "string"}),
			"allowed_outcomes": map[string]any{"type": "array", "uniqueItems": true, "items": map[string]any{"type": "string", "enum": agentsdk.AgentTaskOutcomes}},
		},
	}
}

func openAPIInteractiveAgentHandoffSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"contract_version", "route_type", "target_key", "input", "idempotency_key"},
		"properties": map[string]any{
			"contract_version": map[string]any{"type": "string", "enum": []string{agentsdk.InteractiveHandoffContractVersion}},
			"route_type":       map[string]any{"type": "string", "enum": []string{agentsdk.AgentRouteTask, agentsdk.AgentRouteWorkflow}},
			"target_key":       map[string]any{"type": "string"}, "input": openAPIObject(nil), "idempotency_key": map[string]any{"type": "string"},
			"process_id": map[string]any{"type": "string", "readOnly": true}, "task_run_id": map[string]any{"type": "string", "readOnly": true},
		},
	}
}

func openAPIObjectDataSchema(object definitionmodel.ObjectSchema) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == "" || strings.TrimSpace(field.DisabledAt) != "" {
			continue
		}
		properties[field.Key] = openAPIFieldSchema(field)
		if field.Required {
			required = append(required, field.Key)
		}
	}
	sort.Strings(required)
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func openAPIFieldSchema(field definitionmodel.FieldSchema) map[string]any {
	schema := map[string]any{}
	switch strings.ToLower(strings.TrimSpace(field.Type)) {
	case "currency", "decimal":
		schema["type"] = "string"
		schema["format"] = "decimal"
		schema["pattern"] = `^-?[0-9]+(?:\.[0-9]+)?$`
		schema["x-lossless-decimal"] = true
	case "number", "float":
		schema["type"] = "number"
	case "integer", "int":
		schema["type"] = "integer"
	case "boolean", "bool":
		schema["type"] = "boolean"
	case "date", "datetime", "timestamp":
		schema["type"] = "string"
		schema["format"] = "date-time"
	case "json", "object", "rich_text", "address":
		schema["type"] = "object"
	case "array", "multi_select":
		schema["type"] = "array"
		schema["items"] = map[string]any{"type": "string"}
	default:
		schema["type"] = "string"
	}
	openAPISetConstraint(schema, "minLength", field.Validation.MinLength, field.Validation.MinLength > 0)
	openAPISetConstraint(schema, "maxLength", field.Validation.MaxLength, field.Validation.MaxLength > 0)
	openAPISetFloatConstraint(schema, "minimum", field.Validation.Min)
	openAPISetFloatConstraint(schema, "maximum", field.Validation.Max)
	openAPISetConstraint(schema, "pattern", field.Validation.Pattern, field.Validation.Pattern != "")
	openAPISetConstraint(schema, "enum", field.Validation.Options, len(field.Validation.Options) > 0)
	openAPISetConstraint(schema, "title", field.Name, field.Name != "")
	return schema
}

func openAPISetConstraint(schema map[string]any, key string, value any, include bool) {
	if !include {
		return
	}
	schema[key] = value
}

func openAPISetFloatConstraint(schema map[string]any, key string, value *float64) {
	if value == nil {
		return
	}
	schema[key] = *value
}
