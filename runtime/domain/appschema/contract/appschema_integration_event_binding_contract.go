package contract

import (
	"fmt"
	"sort"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	bindingcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/binding"
)

type IntegrationEventContractIssue struct {
	Code     string
	Field    string
	Path     string
	Expected string
	Actual   string
}

// IntegrationEventBindings validates the Runtime targets of an Integration
// event mapping and projects its declared event fields into the shared binding
// environment. Validation of actual inbound payloads belongs to Integration.
func IntegrationEventBindings(mapping appschemamodel.IntegrationEventMappingSchema) (bindingcontract.Environment, []IntegrationEventContractIssue) {
	issues := []IntegrationEventContractIssue{}
	bindings := bindingcontract.Environment{}
	declared := map[string]appschemamodel.IntegrationEventFieldSchema{}
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
		bindings.Add(bindingcontract.Fact{Reference: "$event." + fieldPath, Type: valueType, Producer: "integration_event_contract", Values: values})
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
