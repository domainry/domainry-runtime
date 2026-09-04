package validation

import (
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	bindingcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/binding"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/invocation"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

	"context"
	"encoding/json"
	"fmt"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"math"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
)

// AutomationDefinitionCatalog is the read-only metadata contract needed to validate an
// automation definition. Integration remains the owner of connection storage;
// automation only consumes the catalog through ListConnections.
type AutomationDefinitionCatalog struct {
	Objects     []definitionmodel.ObjectSchema
	Actions     []definitionmodel.ActionSchema
	Workflows   []definitionmodel.WorkflowSchema
	Connectors  []connectormodel.ConnectorSchema
	Connections []integrationsdk.Connection
}

type AutomationDefinitionValidator struct {
	Catalog AutomationDefinitionCatalog
}

func (v AutomationDefinitionValidator) Validate(ctx context.Context, rule automationmodel.AutomationRuleSchema) error {
	if strings.TrimSpace(rule.Key) == "" || strings.TrimSpace(rule.Name) == "" {
		return definitionError(apperror.KindBadRequest, "backend.automation.identity_required", nil)
	}
	catalog := v.Catalog
	objects := make(map[string]definitionmodel.ObjectSchema, len(catalog.Objects))
	for _, object := range catalog.Objects {
		objects[strings.TrimSpace(object.Key)] = object
	}
	object, objectExists := objects[strings.TrimSpace(rule.ObjectKey)]
	if !objectExists {
		return definitionError(apperror.KindBadRequest, "backend.automation.object_not_found", nil, "object", rule.ObjectKey)
	}
	if err := AutomationValidateTriggerShape(rule.Trigger); err != nil {
		return err
	}
	if err := AutomationValidateExecutionPolicyShape(rule.Execution); err != nil {
		return err
	}
	if err := AutomationValidateTriggerFilters(rule.Trigger, object); err != nil {
		return err
	}
	if err := AutomationValidateConditionGroup(rule.Conditions, "conditions", 0); err != nil {
		return err
	}
	if err := AutomationValidateConditionReferences(rule.Conditions, object); err != nil {
		return err
	}
	objectFields := map[string]bool{}
	for _, field := range object.Fields {
		objectFields[strings.TrimSpace(field.Key)] = true
	}
	for _, fieldKey := range rule.Execution.IdempotencyKeys {
		if !objectFields[strings.TrimSpace(fieldKey)] {
			return definitionError(apperror.KindBadRequest, "backend.automation.idempotency_field_not_found", nil, "field", fieldKey)
		}
	}
	seen := map[string]bool{}
	aliases := map[string]bool{}
	availableOutputs := map[string]map[string]string{}
	for _, action := range rule.Instructions {
		if strings.TrimSpace(action.Key) == "" || seen[action.Key] {
			return definitionError(apperror.KindBadRequest, "backend.automation.instruction_key_invalid", nil, "instruction", action.Key)
		}
		seen[action.Key] = true
		if err := AutomationValidateInstructionShape(rule.Trigger.Phase, action); err != nil {
			return err
		}
		alias := strings.TrimSpace(action.ResultAlias)
		if alias == "" {
			alias = strings.TrimSpace(action.Key)
		}
		if aliases[alias] {
			return definitionError(apperror.KindBadRequest, "backend.automation.result_alias_duplicate", nil, "alias", alias)
		}
		switch action.Type {
		case "invoke_business_action":
			actionKey := strings.TrimSpace(fmt.Sprint(action.Config["action_key"]))
			target, found := findAction(catalog.Actions, actionKey)
			if !found {
				return definitionError(apperror.KindBadRequest, "backend.automation.business_action_not_found", nil, "action", actionKey)
			}
			objectKey := strings.TrimSpace(fmt.Sprint(action.Config["object_key"]))
			if objectKey == "" || objectKey == "<nil>" {
				objectKey = rule.ObjectKey
			}
			input, _ := action.Config["input"].(map[string]any)
			if issues := invocationcontract.ValidateAction(target, objectKey, input, automationBindingEnvironment(object, availableOutputs)); len(issues) > 0 {
				return automationInvocationError("business_action", action.Key, issues[0])
			}
		case "start_workflow":
			workflowKey := strings.TrimSpace(fmt.Sprint(action.Config["workflow_key"]))
			target, found := findWorkflow(catalog.Workflows, workflowKey)
			if !found {
				return definitionError(apperror.KindBadRequest, "backend.automation.target_workflow_not_found", nil, "workflow", workflowKey)
			}
			payload, _ := action.Config["payload"].(map[string]any)
			if payload == nil {
				payload, _ = action.Config["input"].(map[string]any)
			}
			if issues := invocationcontract.ValidateWorkflow(target, invocationcontract.WorkflowEntryAutomation, payload, automationBindingEnvironment(object, availableOutputs)); len(issues) > 0 {
				return automationInvocationError("workflow", action.Key, issues[0])
			}
		}
		if err := AutomationValidateInstructionReferences(action, availableOutputs); err != nil {
			return err
		}
		if rule.Trigger.Phase == "after" {
			aliases[alias] = true
			availableOutputs[alias] = automationInstructionOutputTypes(action, catalog.Actions)
		}
	}
	return nil
}

func AutomationValidateConditionGroup(group automationmodel.AutomationConditionGroup, fieldPath string, depth int) error {
	if depth > 20 {
		return definitionError(apperror.KindBadRequest, "backend.automation.condition_depth_exceeded", nil, "field", fieldPath, "maximum", "20")
	}
	mode := valueOrDefault(strings.ToLower(strings.TrimSpace(group.Mode)), "all")
	if mode != "all" && mode != "any" {
		return definitionError(apperror.KindBadRequest, "backend.automation.condition_mode_invalid", nil, "field", fieldPath+".mode", "mode", group.Mode, "allowed", "all,any")
	}
	operators := map[string]bool{"empty": true, "eq": true, "future_date": true, "gt": true, "gte": true, "in": true, "lt": true, "lte": true, "ne": true, "not_empty": true}
	for index, clause := range group.Clauses {
		clausePath := fmt.Sprintf("%s.clauses[%d]", fieldPath, index)
		if strings.TrimSpace(clause.Reference) == "" {
			return definitionError(apperror.KindBadRequest, "backend.automation.condition_reference_required", nil, "field", clausePath+".reference")
		}
		if !operators[strings.TrimSpace(clause.Operator)] {
			return definitionError(apperror.KindBadRequest, "backend.automation.condition_operator_invalid", nil, "field", clausePath+".operator", "operator", clause.Operator)
		}
	}
	for index, nested := range group.Groups {
		if err := AutomationValidateConditionGroup(nested, fmt.Sprintf("%s.groups[%d]", fieldPath, index), depth+1); err != nil {
			return err
		}
	}
	return nil
}

func AutomationValidateConditionReferences(group automationmodel.AutomationConditionGroup, object definitionmodel.ObjectSchema) error {
	for _, clause := range group.Clauses {
		sourceType, known := AutomationMappingValueType(clause.Reference, object, nil)
		if !known {
			return definitionError(apperror.KindBadRequest, "backend.automation.condition_reference_unknown", nil, "reference", clause.Reference)
		}
		if clause.Operator == "empty" || clause.Operator == "not_empty" || clause.Value == nil {
			continue
		}
		targetType, targetKnown := AutomationMappingValueType(clause.Value, object, nil)
		if targetKnown && !AutomationProtocolTypesCompatible(targetType, sourceType) {
			return definitionError(apperror.KindBadRequest, "backend.automation.condition_type_mismatch", nil, "reference", clause.Reference, "expected", sourceType, "actual", targetType)
		}
	}
	for _, nested := range group.Groups {
		if err := AutomationValidateConditionReferences(nested, object); err != nil {
			return err
		}
	}
	return nil
}

func AutomationValidateTriggerFilters(trigger automationmodel.AutomationTriggerSchema, object definitionmodel.ObjectSchema) error {
	if err := AutomationValidateTriggerFilterShape(trigger); err != nil {
		return err
	}
	fields := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	for _, fieldKey := range trigger.ChangedFields {
		if _, exists := fields[strings.TrimSpace(fieldKey)]; !exists {
			return definitionError(apperror.KindBadRequest, "backend.automation.changed_field_not_found", nil, "field", fieldKey)
		}
	}
	if trigger.Operation != "transition" {
		return nil
	}
	var stateField definitionmodel.FieldSchema
	for _, key := range []string{"status", "state", "stage"} {
		if field, exists := fields[key]; exists && len(field.Validation.Options) > 0 {
			stateField = field
			break
		}
	}
	if stateField.Key == "" {
		for _, field := range object.Fields {
			if field.Type == "select" && len(field.Validation.Options) > 0 {
				stateField = field
				break
			}
		}
	}
	options := map[string]bool{}
	for _, option := range stateField.Validation.Options {
		options[strings.TrimSpace(option)] = true
	}
	for boundary, value := range map[string]string{"from_state": trigger.FromState, "to_state": trigger.ToState} {
		value = strings.TrimSpace(value)
		if value != "" && !options[value] {
			return definitionError(apperror.KindBadRequest, "backend.automation.transition_state_invalid", nil, "field", boundary, "state", value)
		}
	}
	return nil
}

func AutomationConnectorOperation(connector connectormodel.ConnectorSchema, operationKey string) *connectormodel.ConnectorOperationSchema {
	for index := range connector.Operations {
		if strings.TrimSpace(connector.Operations[index].Key) == strings.TrimSpace(operationKey) {
			return &connector.Operations[index]
		}
	}
	return nil
}

func AutomationValidateOperationInput(operation connectormodel.ConnectorOperationSchema, input map[string]any, object definitionmodel.ObjectSchema, outputs map[string]map[string]string) error {
	fields := map[string]definitionmodel.FieldSchema{}
	for _, field := range operation.Input {
		fields[strings.TrimSpace(field.Key)] = field
		if field.Required {
			value, ok := input[field.Key]
			if !ok || recordcontract.RecordIsEmptyValue(value) {
				return definitionError(apperror.KindBadRequest, "backend.automation.operation_input_required", nil, "operation", operation.Key, "field", field.Key)
			}
		}
	}
	for key := range input {
		if _, ok := fields[strings.TrimSpace(key)]; !ok {
			return definitionError(apperror.KindBadRequest, "backend.automation.operation_input_unknown", nil, "operation", operation.Key, "field", key)
		}
	}
	for key, value := range input {
		field := fields[strings.TrimSpace(key)]
		sourceType, known := AutomationMappingValueType(value, object, outputs)
		if reference, ok := value.(string); ok && strings.HasPrefix(reference, "$") && !known {
			return definitionError(apperror.KindBadRequest, "backend.automation.input_reference_unknown", nil, "operation", operation.Key, "field", key, "reference", reference)
		}
		if known && !AutomationProtocolTypesCompatible(sourceType, field.Type) {
			return definitionError(apperror.KindBadRequest, "backend.automation.operation_input_type_mismatch", nil, "operation", operation.Key, "field", key, "expected", field.Type, "actual", sourceType)
		}
	}
	return nil
}

func AutomationMappingValueType(value any, object definitionmodel.ObjectSchema, outputs map[string]map[string]string) (string, bool) {
	if reference, ok := value.(string); ok && strings.HasPrefix(reference, "$") {
		parts := strings.Split(strings.TrimPrefix(reference, "$"), ".")
		if len(parts) == 2 && (parts[0] == "input" || parts[0] == "payload" || parts[0] == "candidate" || parts[0] == "record" || parts[0] == "before") {
			if parts[0] == "record" && parts[1] == "id" {
				return "relation", true
			}
			for _, field := range object.Fields {
				if strings.TrimSpace(field.Key) == parts[1] {
					return strings.TrimSpace(field.Type), true
				}
			}
		}
		if len(parts) >= 3 && (parts[0] == "actions" || parts[0] == "steps") && outputs[parts[1]] != nil {
			fieldType, exists := outputs[parts[1]][strings.Join(parts[2:], ".")]
			return fieldType, exists
		}
		if reference == "$event.timestamp" {
			return "datetime", true
		}
		if reference == "$event.id" {
			return "text", true
		}
		if reference == "$actor.user_id" {
			return "user", true
		}
		if reference == "$actor.role" || reference == "$event.phase" || reference == "$event.operation" || reference == "$event.object_key" {
			return "text", true
		}
		return "", false
	}
	switch value.(type) {
	case bool:
		return "boolean", true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer", true
	case float32:
		value := float64(value.(float32))
		if !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value {
			return "integer", true
		}
		return "decimal", true
	case float64:
		value := value.(float64)
		if !math.IsNaN(value) && !math.IsInf(value, 0) && math.Trunc(value) == value {
			return "integer", true
		}
		return "decimal", true
	case map[string]any, []any:
		return "json", true
	case string:
		return "text", true
	case nil:
		return "", false
	default:
		return "json", true
	}
}

func AutomationProtocolTypesCompatible(sourceType, targetType string) bool {
	source := strings.ToLower(strings.TrimSpace(sourceType))
	target := strings.ToLower(strings.TrimSpace(targetType))
	if target == "json" || source == target {
		return true
	}
	if target == "decimal" && (source == "integer" || source == "number") {
		return true
	}
	if target == "number" && (source == "integer" || source == "decimal" || source == "currency") {
		return true
	}
	textTypes := map[string]bool{"text": true, "long_text": true, "string": true, "email": true, "phone": true, "url": true, "select": true, "relation": true, "user": true, "file": true}
	return textTypes[target] && textTypes[source]
}

func AutomationValidateInstructionReferences(action automationmodel.AutomationInstructionSchema, outputs map[string]map[string]string) error {
	var walk func(any) error
	walk = func(value any) error {
		switch typed := value.(type) {
		case string:
			if !strings.HasPrefix(typed, "$actions.") && !strings.HasPrefix(typed, "$steps.") {
				return nil
			}
			trimmed := strings.TrimPrefix(strings.TrimPrefix(typed, "$actions."), "$steps.")
			parts := strings.Split(trimmed, ".")
			if len(parts) < 2 || outputs[parts[0]] == nil {
				return definitionError(apperror.KindBadRequest, "backend.automation.output_reference_unknown", nil, "reference", typed, "instruction", action.Key)
			}
			if _, fieldExists := outputs[parts[0]][strings.Join(parts[1:], ".")]; !fieldExists {
				return definitionError(apperror.KindBadRequest, "backend.automation.output_reference_unknown", nil, "reference", typed, "instruction", action.Key)
			}
		case map[string]any:
			for _, item := range typed {
				if err := walk(item); err != nil {
					return err
				}
			}
		case []any:
			for _, item := range typed {
				if err := walk(item); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(action.Input); err != nil {
		return err
	}
	return walk(action.Config)
}

func automationBindingEnvironment(object definitionmodel.ObjectSchema, outputs map[string]map[string]string) bindingcontract.Environment {
	environment := bindingcontract.NewEnvironment(
		bindingcontract.Fact{Reference: "$event.id", Type: bindingcontract.TypeText, Producer: "automation.event"},
		bindingcontract.Fact{Reference: "$event.timestamp", Type: bindingcontract.TypeDateTime, Producer: "automation.event"},
		bindingcontract.Fact{Reference: "$event.operation", Type: bindingcontract.TypeText, Producer: "automation.event"},
		bindingcontract.Fact{Reference: "$event.object_key", Type: bindingcontract.TypeText, Producer: "automation.event"},
		bindingcontract.Fact{Reference: "$actor.user_id", Type: bindingcontract.TypeUser, Producer: "automation.actor"},
		bindingcontract.Fact{Reference: "$actor.role", Type: bindingcontract.TypeText, Producer: "automation.actor"},
		bindingcontract.Fact{Reference: "$record.id", Type: bindingcontract.TypeRelation, Producer: "automation.record"},
	)
	for _, field := range object.Fields {
		for _, prefix := range []string{"$input.", "$payload.", "$candidate.", "$record.", "$before."} {
			environment.Add(bindingcontract.Fact{Reference: prefix + field.Key, Type: bindingcontract.NormalizeType(field.Type), Producer: "automation.record"})
		}
	}
	for alias, fields := range outputs {
		for field, typeName := range fields {
			for _, prefix := range []string{"$actions.", "$steps."} {
				environment.Add(bindingcontract.Fact{Reference: prefix + alias + "." + field, Type: bindingcontract.NormalizeType(typeName), Producer: "automation.instruction:" + alias})
			}
		}
	}
	return environment
}

func automationInstructionOutputTypes(instruction automationmodel.AutomationInstructionSchema, actions []definitionmodel.ActionSchema) map[string]string {
	result := map[string]string{}
	switch instruction.Type {
	case "invoke_business_action":
		result["record_id"], result["object_key"] = "relation", "text"
		if action, found := findAction(actions, strings.TrimSpace(fmt.Sprint(instruction.Config["action_key"]))); found {
			for _, field := range action.OutputFields {
				result["data."+field.Key] = field.Type
			}
		}
	case "start_workflow":
		result["workflow_key"], result["execution_id"], result["status"] = "text", "text", "text"
	case "emit_event":
		result["event_type"], result["object_key"], result["record_id"], result["metadata"] = "text", "text", "relation", "json"
	case "assert":
		result["matched"] = "boolean"
	case "derive_fields":
		for key, value := range objectMapFromAny(instruction.Config["fields"]) {
			valueType, known := AutomationMappingValueType(value, definitionmodel.ObjectSchema{}, nil)
			if known {
				result[key] = valueType
			}
		}
	}
	return result
}

func automationInvocationError(kind, instruction string, issue invocationcontract.Issue) error {
	code := "backend.automation." + kind + "_" + strings.TrimPrefix(issue.Code, "invocation.")
	return definitionError(apperror.KindBadRequest, code, nil, "instruction", instruction, "field", issue.Field, "expected", issue.Expected, "actual", issue.Actual, "reference", issue.Reference)
}

func findAction(actions []definitionmodel.ActionSchema, key string) (definitionmodel.ActionSchema, bool) {
	for _, action := range actions {
		if strings.TrimSpace(action.Key) == strings.TrimSpace(key) {
			return action, true
		}
	}
	return definitionmodel.ActionSchema{}, false
}

func findWorkflow(workflows []definitionmodel.WorkflowSchema, key string) (definitionmodel.WorkflowSchema, bool) {
	for _, workflow := range workflows {
		if strings.TrimSpace(workflow.Key) == strings.TrimSpace(key) {
			return workflow, true
		}
	}
	return definitionmodel.WorkflowSchema{}, false
}

func objectMapFromAny(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func hasAction(actions []definitionmodel.ActionSchema, key string) bool {
	for _, action := range actions {
		if action.Key == key {
			return true
		}
	}
	return false
}

func hasWorkflow(workflows []definitionmodel.WorkflowSchema, key string) bool {
	for _, workflow := range workflows {
		if workflow.Key == key {
			return true
		}
	}
	return false
}

func intFromAny(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func definitionError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}
