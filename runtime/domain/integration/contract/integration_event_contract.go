package integrationcontract

import (
	"fmt"
	"sort"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	bindingcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/binding"
)

type IntegrationEventContractIssue struct {
	Code     string
	Field    string
	Path     string
	Expected string
	Actual   string
}

func IntegrationEventBindings(mapping integrationmodel.IntegrationEventMappingSchema) (bindingcontract.Environment, []IntegrationEventContractIssue) {
	issues := []IntegrationEventContractIssue{}
	bindings := bindingcontract.Environment{}
	declared := map[string]integrationmodel.IntegrationEventFieldSchema{}
	for index, field := range mapping.EventFields {
		fieldPath := strings.TrimSpace(field.Path)
		path := fmt.Sprintf("event_fields[%d]", index)
		if !integrationEventPathValid(fieldPath) {
			issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_field_path_invalid", Field: path + ".path", Actual: field.Path})
			continue
		}
		if _, exists := declared[fieldPath]; exists {
			issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_field_path_duplicate", Field: path + ".path", Path: fieldPath})
			continue
		}
		valueType := bindingcontract.NormalizeType(field.Type)
		if valueType == bindingcontract.TypeUnknown {
			issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_field_type_invalid", Field: path + ".type", Path: fieldPath, Actual: field.Type})
			continue
		}
		values := normalizedIntegrationEventOptions(field.Options)
		if len(values) != len(field.Options) {
			issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_field_options_invalid", Field: path + ".options", Path: fieldPath})
			continue
		}
		if len(values) > 0 && valueType != bindingcontract.TypeText {
			issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_field_options_type_invalid", Field: path + ".options", Path: fieldPath, Expected: string(bindingcontract.TypeText), Actual: string(valueType)})
			continue
		}
		declared[fieldPath] = field
		reference := "$event." + fieldPath
		bindings.Add(bindingcontract.Fact{Reference: reference, Type: valueType, Producer: "integration_event_contract", Values: values})
	}

	requirePath := func(field, path string, expected bindingcontract.ValueType) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		declaration, exists := declared[path]
		if !exists {
			issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_path_undeclared", Field: field, Path: path})
			return
		}
		actual := bindingcontract.NormalizeType(declaration.Type)
		if expected != bindingcontract.TypeUnknown && !bindingcontract.Compatible(actual, expected) {
			issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_path_type_invalid", Field: field, Path: path, Expected: string(expected), Actual: string(actual)})
		}
	}
	for inputKey, path := range mapping.ActionInput {
		requirePath("action_input."+inputKey, path, bindingcontract.TypeUnknown)
	}
	for inputKey, path := range mapping.WorkflowInput {
		requirePath("workflow_input."+inputKey, path, bindingcontract.TypeUnknown)
	}
	requirePath("record_id_path", mapping.RecordIDPath, bindingcontract.TypeRelation)
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].Field != issues[j].Field {
			return issues[i].Field < issues[j].Field
		}
		return issues[i].Code < issues[j].Code
	})
	return bindings, issues
}

func IntegrationValidateEventPayload(mapping integrationmodel.IntegrationEventMappingSchema, payload map[string]any) []IntegrationEventContractIssue {
	_, issues := IntegrationEventBindings(mapping)
	if len(issues) > 0 {
		return issues
	}
	for index, field := range mapping.EventFields {
		value, present := integrationEventPayloadPathValue(payload, field.Path)
		path := fmt.Sprintf("event_fields[%d]", index)
		if !present {
			if field.Required {
				issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_field_required", Field: path, Path: field.Path})
			}
			continue
		}
		actualType, typed := IntegrationProtocolValueType(value)
		if !IntegrationProtocolValueMatchesType(value, field.Type) && (!typed || !IntegrationProtocolTypesCompatible(actualType, field.Type)) {
			issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_field_value_type_invalid", Field: path, Path: field.Path, Expected: field.Type, Actual: fmt.Sprintf("%T", value)})
			continue
		}
		if len(field.Options) > 0 {
			actual := strings.TrimSpace(fmt.Sprint(value))
			matched := false
			for _, option := range field.Options {
				matched = matched || actual == strings.TrimSpace(option)
			}
			if !matched {
				issues = append(issues, IntegrationEventContractIssue{Code: "integration.event_field_value_option_invalid", Field: path, Path: field.Path, Expected: strings.Join(field.Options, ","), Actual: actual})
			}
		}
	}
	return issues
}

func integrationEventPathValid(path string) bool {
	if path == "" {
		return false
	}
	for _, segment := range strings.Split(path, ".") {
		if strings.TrimSpace(segment) == "" || strings.TrimSpace(segment) != segment {
			return false
		}
	}
	return true
}

func normalizedIntegrationEventOptions(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func integrationEventPayloadPathValue(payload map[string]any, path string) (any, bool) {
	var current any = payload
	for _, segment := range strings.Split(strings.TrimSpace(path), ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok || current == nil {
			return nil, false
		}
	}
	return current, true
}
