package service

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

	"fmt"

	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
)

func ValidateIntegrationOutput(connectors []connectormodel.ConnectorSchema, action automationmodel.AutomationInstructionSchema, output map[string]any) error {
	for _, connector := range connectors {
		if strings.TrimSpace(connector.Key) != strings.TrimSpace(action.ConnectorKey) {
			continue
		}
		operation := connectorOperation(connector, action.Operation)
		if operation == nil {
			if len(connector.Operations) == 0 {
				return nil
			}
			return automationError(apperror.KindBadRequest, "backend.automation.connector_operation_not_found", nil, "operation", action.Operation)
		}
		for _, field := range operation.Output {
			value, present := output[field.Key]
			if field.Required && (!present || recordcontract.RecordIsEmptyValue(value)) {
				return automationError(apperror.KindBadRequest, "backend.automation.operation_output_required", nil, "operation", operation.Key, "field", field.Key)
			}
			if present && !ProtocolValueMatchesType(value, field.Type) {
				return automationError(apperror.KindBadRequest, "backend.automation.operation_output_type_invalid", nil, "operation", operation.Key, "field", field.Key, "type", field.Type)
			}
		}
		return nil
	}
	return automationError(apperror.KindBadRequest, "backend.automation.connector_not_found", nil, "connector", action.ConnectorKey)
}

func MatchingRules(rules []automationmodel.AutomationRuleSchema, objectKey, phase, operation string, before, candidate map[string]any) []automationmodel.AutomationRuleSchema {
	out := []automationmodel.AutomationRuleSchema{}
	for _, rule := range rules {
		if rule.Enabled && rule.ObjectKey == objectKey && rule.Trigger.Phase == phase && rule.Trigger.Operation == operation && ChangedFieldsMatch(rule.Trigger.ChangedFields, before, candidate) {
			out = append(out, rule)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func ChangedFieldsMatch(required []string, before, candidate map[string]any) bool {
	if len(required) == 0 {
		return true
	}
	for _, key := range required {
		if fmt.Sprint(before[key]) != fmt.Sprint(candidate[key]) {
			return true
		}
	}
	return false
}

func connectorOperation(connector connectormodel.ConnectorSchema, operationKey string) *connectormodel.ConnectorOperationSchema {
	for index := range connector.Operations {
		if strings.TrimSpace(connector.Operations[index].Key) == strings.TrimSpace(operationKey) {
			return &connector.Operations[index]
		}
	}
	return nil
}

func automationError(kind apperror.ErrorKind, code string, err error, params ...string) error {
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
