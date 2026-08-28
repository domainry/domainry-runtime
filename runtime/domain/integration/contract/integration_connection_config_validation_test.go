package integrationcontract

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestIntegrationValidateConnectionConfig(t *testing.T) {
	for _, key := range []string{"provider", " providers ", "payment_providers"} {
		err := IntegrationValidateConnectionConfig(integrationmodel.ConnectorSchema{Key: "connector"}, "", "disabled", map[string]any{key: "forbidden"})
		assertIntegrationValidationCode(t, err, "backend.integration.connection.provider_in_config_forbidden")
	}
	if err := IntegrationValidateConnectionConfig(integrationmodel.ConnectorSchema{Type: "http"}, "", "disabled", nil); err != nil {
		t.Fatalf("disabled connection should bypass active validation: %v", err)
	}
	for _, connectorType := range []string{"http", "webhook"} {
		connector := integrationmodel.ConnectorSchema{Key: "hook", Type: connectorType}
		assertIntegrationValidationCode(t, IntegrationValidateConnectionConfig(connector, "", "active", nil), "backend.integration.connection.url_required")
		assertIntegrationValidationCode(t, IntegrationValidateConnectionConfig(connector, "", "active", map[string]any{"url": nil}), "backend.integration.connection.url_required")
		if err := IntegrationValidateConnectionConfig(connector, "", "active", map[string]any{"url": "https://example.test"}); err != nil {
			t.Fatalf("valid %s config: %v", connectorType, err)
		}
	}
	providerConnector := integrationmodel.ConnectorSchema{Key: "provider-connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "primary", ConfigFields: []definitionmodel.FieldSchema{{Key: "tenant", Type: "text", Required: true}}}}}
	assertIntegrationValidationCode(t, IntegrationValidateConnectionConfig(providerConnector, "primary", "active", nil), "backend.integration.connection.provider_config_required")
}

func TestIntegrationValidateMockConnectionConfig(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "mock", Type: "mock", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "lookup", Output: []definitionmodel.FieldSchema{{Key: "id", Type: "integer", Required: true}, {Key: "note", Type: "text"}}}}}
	tests := []struct {
		name   string
		config map[string]any
		code   string
	}{
		{name: "responses missing", config: nil, code: "backend.integration.connection.mock_responses_required"},
		{name: "responses wrong type", config: map[string]any{"responses": "bad"}, code: "backend.integration.connection.mock_responses_required"},
		{name: "unknown operation", config: map[string]any{"responses": map[string]any{"missing": map[string]any{}}}, code: "backend.integration.connection.mock_operation_unknown"},
		{name: "required output missing", config: map[string]any{"responses": map[string]any{"lookup": map[string]any{}}}, code: "backend.integration.connection.mock_output_required"},
		{name: "required output empty", config: map[string]any{"responses": map[string]any{"lookup": map[string]any{"id": nil}}}, code: "backend.integration.connection.mock_output_required"},
		{name: "output type", config: map[string]any{"responses": map[string]any{"lookup": map[string]any{"id": "one"}}}, code: "backend.integration.connection.mock_output_type_invalid"},
		{name: "valid", config: map[string]any{"responses": map[string]any{"lookup": map[string]any{"id": 1, "note": "ok"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := IntegrationValidateConnectionConfig(connector, "", "active", test.config)
			if test.code == "" && err != nil {
				t.Fatalf("valid mock config: %v", err)
			}
			if test.code != "" {
				assertIntegrationValidationCode(t, err, test.code)
			}
		})
	}
}

func TestIntegrationValidateProviderConfigRules(t *testing.T) {
	min, max := 2.0, 5.0
	connector := integrationmodel.ConnectorSchema{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", ConfigFields: []definitionmodel.FieldSchema{
		{Key: "region", Type: "select", Required: true, Validation: definitionmodel.FieldValidation{Options: []string{"us", "eu"}}},
		{Key: "alias", Type: "text", Validation: definitionmodel.FieldValidation{MinLength: 2, MaxLength: 4, Pattern: "^[a-z]+$"}},
		{Key: "retries", Type: "integer", Validation: definitionmodel.FieldValidation{Min: &min, Max: &max}},
		{Key: "endpoint", Type: "string", Config: map[string]any{"required_with": []any{" token ", "", nil}}},
	}}}}
	if err := IntegrationValidateProviderConfig(connector, "missing", nil); err != nil {
		t.Fatalf("unknown provider should not impose schema: %v", err)
	}
	tests := []struct {
		name   string
		config map[string]any
		code   string
	}{
		{name: "required", config: nil, code: "backend.integration.connection.provider_config_required"},
		{name: "type", config: map[string]any{"region": true}, code: "backend.integration.connection.provider_config_type_invalid"},
		{name: "options", config: map[string]any{"region": "ap"}, code: "backend.integration.connection.provider_config_validation_failed"},
		{name: "min length", config: map[string]any{"region": "us", "alias": "a"}, code: "backend.integration.connection.provider_config_validation_failed"},
		{name: "max length", config: map[string]any{"region": "us", "alias": "abcde"}, code: "backend.integration.connection.provider_config_validation_failed"},
		{name: "pattern", config: map[string]any{"region": "us", "alias": "A1"}, code: "backend.integration.connection.provider_config_validation_failed"},
		{name: "invalid regexp", config: map[string]any{"region": "us", "alias": "abc"}, code: ""},
		{name: "numeric text type", config: map[string]any{"region": "us", "retries": "bad"}, code: "backend.integration.connection.provider_config_type_invalid"},
		{name: "JSON integer", config: map[string]any{"region": "us", "retries": json.Number("3")}},
		{name: "JSON fractional integer", config: map[string]any{"region": "us", "retries": json.Number("3.5")}, code: "backend.integration.connection.provider_config_type_invalid"},
		{name: "JSON integer below minimum", config: map[string]any{"region": "us", "retries": json.Number("1")}, code: "backend.integration.connection.provider_config_validation_failed"},
		{name: "min", config: map[string]any{"region": "us", "retries": 1}, code: "backend.integration.connection.provider_config_validation_failed"},
		{name: "max", config: map[string]any{"region": "us", "retries": 6}, code: "backend.integration.connection.provider_config_validation_failed"},
		{name: "required with", config: map[string]any{"region": "us", "endpoint": "https://example.test"}, code: "backend.integration.connection.provider_config_validation_failed"},
		{name: "valid", config: map[string]any{"region": "us", "alias": "abc", "retries": "3", "token": "ready", "endpoint": "https://example.test"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			testConnector := connector
			if test.name == "invalid regexp" {
				testConnector.Providers = append([]integrationmodel.ConnectorProviderSchema(nil), connector.Providers...)
				testConnector.Providers[0].ConfigFields = append([]definitionmodel.FieldSchema(nil), connector.Providers[0].ConfigFields...)
				testConnector.Providers[0].ConfigFields[1].Validation.Pattern = "["
				test.code = "backend.integration.connection.provider_config_validation_failed"
			}
			err := IntegrationValidateProviderConfig(testConnector, "provider", test.config)
			if test.code == "" && err != nil {
				t.Fatalf("valid provider config: %v", err)
			}
			if test.code != "" {
				assertIntegrationValidationCode(t, err, test.code)
			}
		})
	}
}

func TestIntegrationApplyProviderConfigDefaultsClonesMutableValues(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", ConfigFields: []definitionmodel.FieldSchema{
		{Key: "map", Default: map[string]any{"nested": true}},
		{Key: "slice", Default: []any{"a"}},
		{Key: "strings", Default: []string{"a"}},
		{Key: "scalar", Default: "value"},
		{Key: "conditional", Default: true, Config: map[string]any{"required_with": []string{"dependency", " "}}},
	}}}}
	input := map[string]any{"existing": "kept", "map": map[string]any{"custom": true}}
	got := IntegrationApplyProviderConfigDefaults(connector, "provider", input)
	if got["existing"] != "kept" || !reflect.DeepEqual(got["map"], map[string]any{"custom": true}) || got["scalar"] != "value" {
		t.Fatalf("defaults = %#v", got)
	}
	if _, exists := got["conditional"]; exists {
		t.Fatalf("conditional default applied without dependency: %#v", got)
	}
	withDependency := IntegrationApplyProviderConfigDefaults(connector, "provider", map[string]any{"dependency": "ready"})
	if withDependency["conditional"] != true {
		t.Fatalf("conditional default missing: %#v", withDependency)
	}
	withDependency["slice"].([]any)[0] = "changed"
	withDependency["strings"].([]string)[0] = "changed"
	withDependency["map"].(map[string]any)["nested"] = false
	fields := connector.Providers[0].ConfigFields
	if fields[0].Default.(map[string]any)["nested"] != true || fields[1].Default.([]any)[0] != "a" || fields[2].Default.([]string)[0] != "a" {
		t.Fatal("default application mutated provider schema defaults")
	}
	if got := IntegrationApplyProviderConfigDefaults(connector, "missing", nil); got == nil || len(got) != 0 {
		t.Fatalf("unknown provider defaults = %#v", got)
	}
}

func TestIntegrationProviderConfigValueTypesAndNumbers(t *testing.T) {
	for _, value := range []any{int(1), int8(1), int16(1), int32(1), int64(1), uint(1), uint8(1), uint16(1), uint32(1), uint64(1), float32(1), float64(1), "1"} {
		if _, ok := integrationProviderConfigNumber(value); !ok {
			t.Fatalf("number rejected: %T", value)
		}
	}
	for _, value := range []any{"bad", true} {
		if _, ok := integrationProviderConfigNumber(value); ok {
			t.Fatalf("non-number accepted: %#v", value)
		}
	}
	tests := []struct {
		value any
		kind  string
		want  bool
	}{
		{value: 1, kind: "integer", want: true}, {value: " 1 ", kind: "integer", want: true}, {value: "1.2", kind: "integer", want: false},
		{value: true, kind: "boolean", want: true}, {value: "true", kind: "boolean", want: true}, {value: "bad", kind: "boolean", want: false},
		{value: map[string]any{}, kind: "json", want: true}, {value: []any{}, kind: "json", want: true}, {value: []string{}, kind: "json", want: true}, {value: "bad", kind: "json", want: false},
		{value: "text", kind: "select", want: true}, {value: 1, kind: "text", want: false}, {value: make(chan int), kind: "custom", want: true},
	}
	for _, test := range tests {
		if got := integrationProviderConfigValueMatchesType(test.value, test.kind); got != test.want {
			t.Fatalf("value %T matches %q = %v, want %v", test.value, test.kind, got, test.want)
		}
	}
	if got := integrationConfigMap("bad"); len(got) != 0 || got == nil {
		t.Fatalf("invalid config map = %#v", got)
	}
	if got := integrationCloneConfigMap(nil); got != nil {
		t.Fatalf("nil clone = %#v", got)
	}
	min := 1.0
	err := integrationValidateProviderConfigFieldValue("connector", "provider", definitionmodel.FieldSchema{Key: "count", Validation: definitionmodel.FieldValidation{Min: &min}}, true, nil)
	assertIntegrationValidationCode(t, err, "backend.integration.connection.provider_config_validation_failed")
	if err := integrationConnectionValidationError(" code "); integrationValidationCode(err) != "code" {
		t.Fatalf("validation error = %v", err)
	}
}

func TestIntegrationProviderConfigAcceptsConnectorFieldSemantics(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{
		Key: "provider", ConfigFields: []definitionmodel.FieldSchema{
			{Key: "email", Type: "email", Config: map[string]any{"contract_owner": "connector"}},
			{Key: "count", Type: "integer", Config: map[string]any{"contract_owner": "connector"}},
			{Key: "amount", Type: "decimal", Config: map[string]any{"contract_owner": "connector"}},
			{Key: "enabled", Type: "boolean", Config: map[string]any{"contract_owner": "connector"}},
			{Key: "metadata", Type: "json", Config: map[string]any{"contract_owner": "connector"}},
		},
	}}}
	valid := map[string]any{"email": "ada@example.com", "count": float64(2), "amount": json.Number("2.5"), "enabled": true, "metadata": "scalar-json"}
	if err := IntegrationValidateProviderConfig(connector, "provider", valid); err != nil {
		t.Fatalf("public config rejected: %v", err)
	}
	for _, invalid := range []map[string]any{
		{"email": "not-an-email", "count": 2, "amount": 2.5, "enabled": true, "metadata": map[string]any{}},
		{"email": "ada@example.com", "count": 2.5, "amount": 2.5, "enabled": true, "metadata": map[string]any{}},
		{"email": "ada@example.com", "count": 2, "amount": "2.5", "enabled": true, "metadata": map[string]any{}},
		{"email": "ada@example.com", "count": 2, "amount": math.NaN(), "enabled": true, "metadata": map[string]any{}},
		{"email": "ada@example.com", "count": 2, "amount": 2.5, "enabled": "true", "metadata": map[string]any{}},
		{"email": "ada@example.com", "count": 2, "amount": 2.5, "enabled": true, "metadata": make(chan int)},
	} {
		if err := IntegrationValidateProviderConfig(connector, "provider", invalid); err == nil {
			t.Fatalf("invalid public config accepted: %#v", invalid)
		}
	}
}

func assertIntegrationValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	if got := integrationValidationCode(err); got != code {
		t.Fatalf("error code = %q, want %q; err=%v", got, code, err)
	}
}

func integrationValidationCode(err error) string {
	var coded *apperror.CodedError
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return apperror.CodeOf(err)
}
