// Package invocation owns the static target and input contract for every
// cross-resource Action or Workflow call. Callers add their own authorization
// checks, but may not redefine target modes, required/default semantics, or
// value compatibility.
package invocation

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	bindingcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/binding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
)

var canonicalExactDecimalLiteralPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

type WorkflowEntryMode string

const (
	WorkflowEntryManual           WorkflowEntryMode = "manual"
	WorkflowEntryAutomation       WorkflowEntryMode = "automation"
	WorkflowEntryAgent            WorkflowEntryMode = "agent"
	WorkflowEntryIntegrationEvent WorkflowEntryMode = "integration_event"
	WorkflowEntryScheduled        WorkflowEntryMode = "scheduled"
	WorkflowEntryRecordEvent      WorkflowEntryMode = "record_event"
	WorkflowEntryActionEvent      WorkflowEntryMode = "action_event"
)

type Issue struct {
	Code      string
	Field     string
	Expected  string
	Actual    string
	Reference string
}

type InputContract struct {
	Fields     []definitionmodel.ActionPayloadField
	Defaults   map[string]any
	Reserved   map[string]bindingcontract.ValueType
	Bindings   bindingcontract.Environment
	Input      map[string]any
	FieldOwner string
}

func ValidateAction(action definitionmodel.ActionSchema, expectedObject string, input map[string]any, bindings bindingcontract.Environment) []Issue {
	issues := ValidateActionTarget(action, expectedObject)
	if strings.TrimSpace(action.Key) == "" {
		return issues
	}
	issues = append(issues, ValidateInput(InputContract{Fields: action.PayloadFields, Defaults: action.Defaults, Bindings: bindings, Input: input, FieldOwner: action.Key})...)
	return issues
}

// ValidateActionTarget validates only target identity/object ownership. It is
// suitable for finite allowlists that do not carry an invocation payload.
func ValidateActionTarget(action definitionmodel.ActionSchema, expectedObject string) []Issue {
	issues := []Issue{}
	if strings.TrimSpace(action.Key) == "" {
		return []Issue{{Code: "invocation.action_not_found"}}
	}
	if expectedObject = strings.TrimSpace(expectedObject); expectedObject != "" && strings.TrimSpace(action.ObjectKey) != expectedObject {
		issues = append(issues, Issue{Code: "invocation.action_object_mismatch", Field: "object_key", Expected: action.ObjectKey, Actual: expectedObject})
	}
	return issues
}

func ValidateActionPermission(action definitionmodel.ActionSchema, principal principalmodel.Principal) []Issue {
	if !principal.Known {
		return []Issue{{Code: "invocation.action_permission_denied"}}
	}
	if !principal.HasExactPermission(strings.TrimSpace(action.Key)) {
		return []Issue{{Code: "invocation.action_permission_denied", Expected: strings.TrimSpace(action.Key), Actual: strings.TrimSpace(principal.RoleKey)}}
	}
	return nil
}

func ValidateWorkflow(workflow definitionmodel.WorkflowSchema, mode WorkflowEntryMode, input map[string]any, bindings bindingcontract.Environment) []Issue {
	issues := ValidateWorkflowTarget(workflow, mode)
	if strings.TrimSpace(workflow.Key) == "" {
		return issues
	}
	issues = append(issues, ValidateInput(InputContract{
		Fields: workflowInvocationFields(workflow.InputFields), Reserved: WorkflowReservedInputs(mode), Bindings: bindings, Input: input, FieldOwner: workflow.Key,
	})...)
	return issues
}

// ValidateWorkflowTarget validates only the callable target. It is used for
// finite allowlists whose actual payload is supplied later at execution time.
func ValidateWorkflowTarget(workflow definitionmodel.WorkflowSchema, mode WorkflowEntryMode) []Issue {
	issues := []Issue{}
	if strings.TrimSpace(workflow.Key) == "" {
		return []Issue{{Code: "invocation.workflow_not_found"}}
	}
	if !workflow.Enabled {
		issues = append(issues, Issue{Code: "invocation.workflow_disabled"})
	}
	triggerType := ""
	if workflow.TriggerContract != nil {
		triggerType = strings.TrimSpace(workflow.TriggerContract.Type)
	}
	if !workflowModeAllowsTrigger(mode, triggerType) {
		issues = append(issues, Issue{Code: "invocation.workflow_entry_mode_invalid", Field: "trigger_contract.type", Expected: expectedWorkflowTrigger(mode), Actual: triggerType})
	}
	return issues
}

func ValidateWorkflowPermission(workflow definitionmodel.WorkflowSchema, principal principalmodel.Principal) []Issue {
	required := workflowcontract.RunActionKey(workflow.Key)
	if !principal.Known || required == "" || !principal.HasExactPermission(required) {
		return []Issue{{Code: "invocation.workflow_permission_denied", Expected: required, Actual: strings.TrimSpace(principal.RoleKey)}}
	}
	return nil
}

func workflowInvocationFields(fields []definitionmodel.WorkflowInputField) []definitionmodel.ActionPayloadField {
	result := make([]definitionmodel.ActionPayloadField, 0, len(fields))
	for _, field := range fields {
		result = append(result, definitionmodel.ActionPayloadField{Key: field.Key, Name: field.Name, Type: field.Type, Options: append([]string(nil), field.Options...), Required: field.Required, DefaultValue: field.DefaultValue})
	}
	return result
}

func ValidateInput(contract InputContract) []Issue {
	issues := []Issue{}
	fields := map[string]definitionmodel.ActionPayloadField{}
	for _, field := range contract.Fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			continue
		}
		fields[key] = field
		if field.Required && !inputProvides(contract.Input, key) && !inputProvides(contract.Defaults, key) && field.DefaultValue == nil {
			issues = append(issues, Issue{Code: "invocation.input_required", Field: key, Expected: string(bindingcontract.NormalizeType(field.Type))})
		}
	}
	keys := make([]string, 0, len(contract.Input))
	for key := range contract.Input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := contract.Input[key]
		field, declared := fields[strings.TrimSpace(key)]
		if !declared {
			if expected, reserved := contract.Reserved[strings.TrimSpace(key)]; reserved {
				actual, known := bindingcontract.ValueTypeOf(value, contract.Bindings)
				if !known {
					issues = append(issues, Issue{Code: "invocation.input_reference_unknown", Field: key, Reference: strings.TrimSpace(fmt.Sprint(value)), Expected: string(expected)})
				} else if !inputValueCompatible(value, actual, expected) {
					issues = append(issues, Issue{Code: "invocation.input_type_mismatch", Field: key, Expected: string(expected), Actual: string(actual)})
				}
				continue
			}
			issues = append(issues, Issue{Code: "invocation.input_unknown", Field: key})
			continue
		}
		issues = append(issues, validateInputValue(field, value, contract.Bindings)...)
	}
	issues = append(issues, ValidateDefaults(contract.Fields, contract.Defaults)...)
	return issues
}

// ValidateDefaults validates definition-owned defaults without pretending an
// invocation payload exists. This is used when publishing Action metadata.
func ValidateDefaults(fields []definitionmodel.ActionPayloadField, defaults map[string]any) []Issue {
	issues := []Issue{}
	declared := map[string]definitionmodel.ActionPayloadField{}
	for _, field := range fields {
		declared[strings.TrimSpace(field.Key)] = field
	}
	for key, value := range defaults {
		field, exists := declared[strings.TrimSpace(key)]
		if !exists {
			issues = append(issues, Issue{Code: "invocation.default_unknown", Field: key})
			continue
		}
		for _, issue := range validateInputValue(field, value, nil) {
			issue.Code = "invocation.default_" + strings.TrimPrefix(issue.Code, "invocation.input_")
			issues = append(issues, issue)
		}
	}
	for _, field := range fields {
		if field.DefaultValue == nil {
			continue
		}
		for _, issue := range validateInputValue(field, field.DefaultValue, nil) {
			issue.Code = "invocation.default_" + strings.TrimPrefix(issue.Code, "invocation.input_")
			issues = append(issues, issue)
		}
	}
	return issues
}

func validateInputValue(field definitionmodel.ActionPayloadField, value any, bindings bindingcontract.Environment) []Issue {
	actual, known := bindingcontract.ValueTypeOf(value, bindings)
	if text, ok := value.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "$") && !known {
		return []Issue{{Code: "invocation.input_reference_unknown", Field: field.Key, Reference: strings.TrimSpace(text), Expected: field.Type}}
	}
	expected := bindingcontract.NormalizeType(field.Type)
	if known && !exactDecimalLiteralCompatible(field.Type, value, actual) && !inputValueCompatible(value, actual, expected) {
		return []Issue{{Code: "invocation.input_type_mismatch", Field: field.Key, Expected: string(expected), Actual: string(actual)}}
	}
	if len(field.Options) > 0 && known && actual == bindingcontract.TypeText {
		allowed := map[string]bool{}
		for _, option := range field.Options {
			allowed[strings.TrimSpace(option)] = true
		}
		if reference := exactInputReference(value); reference != "" {
			fact := bindings[reference]
			for _, candidate := range fact.Values {
				if !allowed[candidate] {
					return []Issue{{Code: "invocation.input_option_invalid", Field: field.Key, Expected: strings.Join(field.Options, ","), Actual: strings.Join(fact.Values, ",")}}
				}
			}
			return nil
		}
		stored := strings.TrimSpace(fmt.Sprint(value))
		if !allowed[stored] {
			return []Issue{{Code: "invocation.input_option_invalid", Field: field.Key, Expected: strings.Join(field.Options, ","), Actual: stored}}
		}
	}
	return nil
}

// Exact decimal values are transported as canonical strings end-to-end. This
// keeps Action defaults and invocations lossless and avoids a float64 round
// trip while retaining number compatibility for typed producer references.
func exactDecimalLiteralCompatible(fieldType string, value any, actual bindingcontract.ValueType) bool {
	switch strings.ToLower(strings.TrimSpace(fieldType)) {
	case "currency", "decimal", "percent":
		text, literal := value.(string)
		return literal && actual == bindingcontract.TypeText && exactInputReference(text) == "" && canonicalExactDecimalLiteralPattern.MatchString(text)
	default:
		return false
	}
}

func inputValueCompatible(value any, actual, expected bindingcontract.ValueType) bool {
	if bindingcontract.Compatible(actual, expected) {
		return true
	}
	text, literal := value.(string)
	if !literal || exactInputReference(text) != "" {
		return false
	}
	switch expected {
	case bindingcontract.TypeUser, bindingcontract.TypeRelation, bindingcontract.TypeDate, bindingcontract.TypeDateTime:
		return true
	default:
		return false
	}
}

func exactInputReference(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	references := bindingcontract.ReferencesInString(text)
	if len(references) == 1 && strings.TrimSpace(text) == references[0] {
		return references[0]
	}
	return ""
}

func workflowModeAllowsTrigger(mode WorkflowEntryMode, triggerType string) bool {
	switch mode {
	case WorkflowEntryManual, WorkflowEntryAutomation, WorkflowEntryAgent:
		return triggerType == "manual"
	case WorkflowEntryIntegrationEvent:
		return triggerType == "integration_event"
	case WorkflowEntryScheduled:
		return triggerType == "scheduled"
	case WorkflowEntryRecordEvent:
		return triggerType == "record_created" || triggerType == "record_updated" || triggerType == "field_changed"
	case WorkflowEntryActionEvent:
		return triggerType == "action_completed"
	default:
		return false
	}
}

func expectedWorkflowTrigger(mode WorkflowEntryMode) string {
	switch mode {
	case WorkflowEntryManual, WorkflowEntryAutomation, WorkflowEntryAgent:
		return "manual"
	case WorkflowEntryIntegrationEvent:
		return "integration_event"
	case WorkflowEntryScheduled:
		return "scheduled"
	case WorkflowEntryRecordEvent:
		return "record_created|record_updated|field_changed"
	case WorkflowEntryActionEvent:
		return "action_completed"
	default:
		return "none"
	}
}

// WorkflowReservedInputs returns the Runtime-owned input contract for a
// concrete workflow entry path. Authoring and invocation validation share this
// table so a Runtime-provided value cannot be legal in one layer and invisible
// in the other.
func WorkflowReservedInputs(mode WorkflowEntryMode) map[string]bindingcontract.ValueType {
	reserved := map[string]bindingcontract.ValueType{
		"object_key": bindingcontract.TypeText, "record_id": bindingcontract.TypeRelation, "request_id": bindingcontract.TypeText,
		"initiating_user_id": bindingcontract.TypeUser, "initiating_role_key": bindingcontract.TypeText,
	}
	switch mode {
	case WorkflowEntryScheduled:
		reserved["scheduled_at"] = bindingcontract.TypeDateTime
	case WorkflowEntryAgent:
		reserved["agent_handoff_idempotency_key"] = bindingcontract.TypeText
		reserved["agent_interactive_run_id"] = bindingcontract.TypeText
	case WorkflowEntryIntegrationEvent:
		reserved["integration_event_id"] = bindingcontract.TypeText
		reserved["integration_provider"] = bindingcontract.TypeText
		reserved["integration_event_type"] = bindingcontract.TypeText
		reserved["integration_external_id"] = bindingcontract.TypeText
		reserved["integration_mapping_key"] = bindingcontract.TypeText
	}
	return reserved
}

// WorkflowReservedInputsForTrigger returns the Runtime-owned bindings that
// exist when a workflow runs through its declared trigger. Manual workflows
// use the common manual contract; Agent-only handoff values remain scoped to
// the Agent invocation path and therefore are not published here.
func WorkflowReservedInputsForTrigger(triggerType string) map[string]bindingcontract.ValueType {
	mode := WorkflowEntryManual
	switch strings.TrimSpace(triggerType) {
	case "integration_event":
		mode = WorkflowEntryIntegrationEvent
	case "scheduled":
		mode = WorkflowEntryScheduled
	case "record_created", "record_updated", "field_changed":
		mode = WorkflowEntryRecordEvent
	case "action_completed":
		mode = WorkflowEntryActionEvent
	}
	return WorkflowReservedInputs(mode)
}

func inputProvides(values map[string]any, key string) bool {
	if values == nil {
		return false
	}
	value, exists := values[key]
	return exists && value != nil && strings.TrimSpace(fmt.Sprint(value)) != ""
}
