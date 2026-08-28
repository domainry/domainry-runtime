package integrationcontract

import (
	"encoding/json"
	"fmt"
	"math"
	"net/mail"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	apperror "github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
)

func IntegrationValidateConnectionConfig(connector integrationmodel.ConnectorSchema, providerKey, status string, config map[string]any) error {
	for key := range config {
		trimmed := strings.TrimSpace(key)
		if trimmed == "provider" || trimmed == "providers" || strings.HasSuffix(trimmed, "_providers") {
			return integrationConnectionValidationError("backend.integration.connection.provider_in_config_forbidden", "field_path", "config."+key, "actual", key, "expected", "provider_key")
		}
	}
	if status == "disabled" {
		return nil
	}
	if err := IntegrationValidateProviderConfig(connector, providerKey, config); err != nil {
		return err
	}
	switch strings.TrimSpace(connector.Type) {
	case "http", "webhook":
		if strings.TrimSpace(fmt.Sprint(config["url"])) == "" || fmt.Sprint(config["url"]) == "<nil>" {
			return integrationConnectionValidationError("backend.integration.connection.url_required", "field_path", "config.url", "connector", connector.Key)
		}
	case "mock":
		responses := integrationConfigMap(config["responses"])
		if len(responses) == 0 {
			return integrationConnectionValidationError("backend.integration.connection.mock_responses_required", "field_path", "config.responses", "connector", connector.Key)
		}
		operations := make(map[string]integrationmodel.ConnectorOperationSchema, len(connector.Operations))
		for _, operation := range connector.Operations {
			operations[operation.Key] = operation
		}
		for operationKey, rawResponse := range responses {
			operation, ok := operations[operationKey]
			if !ok {
				return integrationConnectionValidationError("backend.integration.connection.mock_operation_unknown", "field_path", "config.responses."+operationKey, "operation", operationKey)
			}
			response := integrationConfigMap(rawResponse)
			for _, field := range operation.Output {
				value, present := response[field.Key]
				if field.Required && (!present || recordcontract.RecordIsEmptyValue(value)) {
					return integrationConnectionValidationError("backend.integration.connection.mock_output_required", "field_path", "config.responses."+operationKey+"."+field.Key, "operation", operation.Key, "field", field.Key)
				}
				if present && !IntegrationProtocolValueMatchesType(value, field.Type) {
					return integrationConnectionValidationError("backend.integration.connection.mock_output_type_invalid", "field_path", "config.responses."+operationKey+"."+field.Key, "operation", operation.Key, "field", field.Key, "expected", field.Type, "actual", fmt.Sprintf("%T", value))
				}
			}
		}
	}
	return nil
}

func IntegrationValidateProviderConfig(connector integrationmodel.ConnectorSchema, providerKey string, config map[string]any) error {
	config = IntegrationApplyProviderConfigDefaults(connector, providerKey, config)
	var provider *integrationmodel.ConnectorProviderSchema
	for index := range connector.Providers {
		if connector.Providers[index].Key == providerKey {
			provider = &connector.Providers[index]
			break
		}
	}
	if provider == nil {
		return nil
	}
	declared := make(map[string]bool, len(provider.ConfigFields))
	for _, field := range connector.ConfigFields {
		declared[strings.TrimSpace(field)] = true
	}
	switch strings.TrimSpace(connector.Type) {
	case "http", "webhook":
		declared["url"] = true
	case "mock":
		declared["responses"] = true
		declared["scenarios"] = true
	}
	for _, field := range provider.ConfigFields {
		declared[strings.TrimSpace(field.Key)] = true
		for _, dependency := range integrationProviderConfigDependencyKeys(field.Config["required_with"]) {
			declared[dependency] = true
		}
	}
	for key := range config {
		if !declared[strings.TrimSpace(key)] {
			return integrationConnectionValidationError("backend.integration.connection.provider_config_unknown", "field_path", "config."+key, "connector", connector.Key, "provider", providerKey, "field", key)
		}
	}
	for _, field := range provider.ConfigFields {
		value, exists := config[field.Key]
		if field.Required && (!exists || recordcontract.RecordIsEmptyValue(value)) {
			return integrationConnectionValidationError("backend.integration.connection.provider_config_required", "field_path", "config."+field.Key, "connector", connector.Key, "provider", providerKey, "field", field.Key)
		}
		if exists && !integrationProviderConfigFieldValueMatchesType(value, field) {
			return integrationConnectionValidationError("backend.integration.connection.provider_config_type_invalid", "field_path", "config."+field.Key, "connector", connector.Key, "provider", providerKey, "field", field.Key, "expected", field.Type, "actual", fmt.Sprintf("%T", value))
		}
		if exists {
			if err := integrationValidateProviderConfigFieldValue(connector.Key, providerKey, field, value, config); err != nil {
				return err
			}
		}
	}
	return nil
}

func IntegrationApplyProviderConfigDefaults(connector integrationmodel.ConnectorSchema, providerKey string, config map[string]any) map[string]any {
	result := integrationCloneConfigMap(config)
	if result == nil {
		result = map[string]any{}
	}
	for _, provider := range connector.Providers {
		if provider.Key != providerKey {
			continue
		}
		for _, field := range provider.ConfigFields {
			if _, exists := result[field.Key]; !exists && field.Default != nil {
				ready := true
				for _, dependency := range integrationProviderConfigDependencyKeys(field.Config["required_with"]) {
					if recordcontract.RecordIsEmptyValue(result[dependency]) {
						ready = false
						break
					}
				}
				if ready {
					result[field.Key] = integrationCloneProviderConfigDefault(field.Default)
				}
			}
		}
		break
	}
	return result
}

func integrationCloneProviderConfigDefault(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return integrationCloneConfigMap(typed)
	case []any:
		return append([]any(nil), typed...)
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}

func integrationValidateProviderConfigFieldValue(connectorKey, providerKey string, field definitionmodel.FieldSchema, value any, config map[string]any) error {
	path := "config." + field.Key
	invalid := func(rule string) error {
		return integrationConnectionValidationError("backend.integration.connection.provider_config_validation_failed", "field_path", path, "connector", connectorKey, "provider", providerKey, "field", field.Key, "rule", rule)
	}
	if !recordcontract.RecordIsEmptyValue(value) {
		for _, dependency := range integrationProviderConfigDependencyKeys(field.Config["required_with"]) {
			if recordcontract.RecordIsEmptyValue(config[dependency]) {
				return invalid("required_with:" + dependency)
			}
		}
	}
	if len(field.Validation.Options) > 0 {
		matched := false
		for _, option := range field.Validation.Options {
			if reflect.DeepEqual(value, option) || fmt.Sprint(value) == option {
				matched = true
				break
			}
		}
		if !matched {
			return invalid("options")
		}
	}
	if text, ok := value.(string); ok {
		length := len([]rune(text))
		if field.Validation.MinLength > 0 && length < field.Validation.MinLength {
			return invalid("min_length")
		}
		if field.Validation.MaxLength > 0 && length > field.Validation.MaxLength {
			return invalid("max_length")
		}
		if pattern := strings.TrimSpace(field.Validation.Pattern); pattern != "" {
			expression, err := regexp.Compile(pattern)
			if err != nil || !expression.MatchString(text) {
				return invalid("pattern")
			}
		}
	}
	if field.Validation.Min != nil || field.Validation.Max != nil {
		number, ok := integrationProviderConfigNumber(value)
		if !ok {
			return invalid("number")
		}
		if field.Validation.Min != nil && number < *field.Validation.Min {
			return invalid("min")
		}
		if field.Validation.Max != nil && number > *field.Validation.Max {
			return invalid("max")
		}
	}
	return nil
}

func integrationProviderConfigDependencyKeys(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if item == nil {
				continue
			}
			if key := strings.TrimSpace(fmt.Sprint(item)); key != "" && key != "<nil>" {
				result = append(result, key)
			}
		}
	case []string:
		for _, item := range typed {
			if key := strings.TrimSpace(item); key != "" {
				result = append(result, key)
			}
		}
	}
	return result
}

func integrationProviderConfigNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func integrationProviderConfigValueMatchesType(value any, fieldType string) bool {
	switch strings.ToLower(strings.TrimSpace(fieldType)) {
	case "integer":
		switch typed := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		case json.Number:
			_, err := typed.Int64()
			return err == nil
		case string:
			_, err := strconv.Atoi(strings.TrimSpace(typed))
			return err == nil
		}
	case "boolean":
		if _, ok := value.(bool); ok {
			return true
		}
		if text, ok := value.(string); ok {
			_, err := strconv.ParseBool(strings.TrimSpace(text))
			return err == nil
		}
	case "json":
		switch value.(type) {
		case map[string]any, []any, []string:
			return true
		}
	case "text", "string", "select":
		_, ok := value.(string)
		return ok
	default:
		return true
	}
	return false
}

func integrationProviderConfigFieldValueMatchesType(value any, field definitionmodel.FieldSchema) bool {
	if field.Config["contract_owner"] != "connector" {
		return integrationProviderConfigValueMatchesType(value, field.Type)
	}
	switch strings.ToLower(strings.TrimSpace(field.Type)) {
	case "text", "select":
		_, ok := value.(string)
		return ok
	case "email":
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) != text {
			return false
		}
		address, err := mail.ParseAddress(text)
		return err == nil && address.Address == text
	case "integer":
		number, ok := integrationProviderConfigNumber(value)
		return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number && !integrationProviderStringValue(value)
	case "decimal":
		number, ok := integrationProviderConfigNumber(value)
		return ok && !math.IsNaN(number) && !math.IsInf(number, 0) && !integrationProviderStringValue(value)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "json":
		_, err := json.Marshal(value)
		return err == nil
	default:
		return false
	}
}

func integrationProviderStringValue(value any) bool {
	_, ok := value.(string)
	return ok
}

func integrationConfigMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}

func integrationCloneConfigMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func integrationConnectionValidationError(code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.CodedError{Code: strings.TrimSpace(code), Params: values}
}
