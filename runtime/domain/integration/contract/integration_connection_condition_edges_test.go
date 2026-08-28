package integrationcontract

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationConnectionConditionEdges(t *testing.T) {
	httpConnector := integrationmodel.ConnectorSchema{Key: "http", Type: "http"}
	assertIntegrationValidationCode(t, IntegrationValidateConnectionConfig(httpConnector, "", "active", map[string]any{"url": ""}), "backend.integration.connection.url_required")

	mock := integrationmodel.ConnectorSchema{Key: "mock", Type: "mock", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "lookup", Output: []definitionmodel.FieldSchema{{Key: "id", Type: "integer", Required: true}, {Key: "optional", Type: "text"}}}}}
	if err := IntegrationValidateConnectionConfig(mock, "", "active", map[string]any{"responses": map[string]any{"lookup": map[string]any{"id": 1}}}); err != nil {
		t.Fatal(err)
	}

	provider := integrationmodel.ConnectorSchema{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", ConfigFields: []definitionmodel.FieldSchema{
		{Key: "required", Type: "text", Required: true},
		{Key: "without_default", Type: "text"},
	}}}}
	assertIntegrationValidationCode(t, IntegrationValidateProviderConfig(provider, "provider", map[string]any{"required": ""}), "backend.integration.connection.provider_config_required")
	defaults := IntegrationApplyProviderConfigDefaults(provider, "provider", nil)
	if len(defaults) != 0 {
		t.Fatalf("unexpected defaults: %#v", defaults)
	}

	max := 5.0
	min := 2.0
	for _, test := range []struct {
		field definitionmodel.FieldSchema
		value any
	}{
		{definitionmodel.FieldSchema{Key: "optional", Config: map[string]any{"required_with": []string{"other"}}}, ""},
		{definitionmodel.FieldSchema{Key: "choice", Validation: definitionmodel.FieldValidation{Options: []string{"1"}}}, 1},
		{definitionmodel.FieldSchema{Key: "number", Validation: definitionmodel.FieldValidation{Max: &max}}, 4},
		{definitionmodel.FieldSchema{Key: "number", Validation: definitionmodel.FieldValidation{Min: &min}}, 3},
		{definitionmodel.FieldSchema{Key: "number", Validation: definitionmodel.FieldValidation{Min: &min, Max: &max}}, 3},
	} {
		if err := integrationValidateProviderConfigFieldValue("connector", "provider", test.field, test.value, map[string]any{}); err != nil {
			t.Fatalf("field %#v value %#v: %v", test.field, test.value, err)
		}
	}
	if got := integrationProviderConfigDependencyKeys([]any{"<nil>"}); len(got) != 0 {
		t.Fatalf("nil marker dependency = %#v", got)
	}
	if got := integrationConfigMap([]any{}); len(got) != 0 {
		t.Fatalf("non-map config = %#v", got)
	}
	if integrationProviderConfigValueMatchesType(1, "boolean") {
		t.Fatal("integer accepted as boolean")
	}
	err := integrationConnectionValidationError("code", "", "ignored")
	if integrationValidationCode(err) != "code" {
		t.Fatalf("error = %v", err)
	}
}

func TestIntegrationProviderConfigFieldTypeEdges(t *testing.T) {
	email := definitionmodel.FieldSchema{Type: "email", Config: map[string]any{"contract_owner": "connector"}}
	if integrationProviderConfigFieldValueMatchesType(123, email) {
		t.Fatal("numeric email accepted")
	}
	if integrationProviderConfigFieldValueMatchesType(" ada@example.com ", email) {
		t.Fatal("padded email accepted")
	}
	if integrationProviderConfigFieldValueMatchesType("value", definitionmodel.FieldSchema{Type: "unknown", Config: map[string]any{"contract_owner": "connector"}}) {
		t.Fatal("unknown connector field accepted")
	}
}
