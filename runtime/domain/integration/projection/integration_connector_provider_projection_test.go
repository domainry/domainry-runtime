package projection

import (
	"reflect"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationReplaceConnectorProvider(t *testing.T) {
	providers := []integrationmodel.ConnectorProviderSchema{{Key: "a", Name: "A"}, {Key: "b", Name: "B", Description: "base"}}
	got := IntegrationReplaceConnectorProvider(providers, integrationmodel.ConnectorProviderSchema{Key: "b", Name: "Override"})
	if got[1].Name != "Override" || got[1].Description != "base" {
		t.Fatalf("replacement = %#v", got[1])
	}
	unchanged := IntegrationReplaceConnectorProvider(got, integrationmodel.ConnectorProviderSchema{Key: "missing", Name: "Missing"})
	if len(unchanged) != 2 || unchanged[0].Key != "a" || unchanged[1].Key != "b" {
		t.Fatalf("missing replacement changed providers: %#v", unchanged)
	}
}

func TestIntegrationMergeConnectorProviderSchemaUsesBaseFallbacksAndOverlayValues(t *testing.T) {
	min, max := 1.0, 10.0
	base := integrationmodel.ConnectorProviderSchema{
		Key: "provider", ProviderRevision: "base-v1", Name: "Base", Description: "Base description",
		I18n:          map[string]map[string]string{"en-US": {"name": "Base", "description": "Base description"}},
		OperationKeys: []string{"read"},
		ConfigFields: []definitionmodel.FieldSchema{{
			Key: "endpoint", Name: "Endpoint", Description: "Base endpoint", Type: "url", Default: "https://base", DefaultValue: "fallback", Options: []string{"a"},
			Config: map[string]any{"base": true}, I18n: map[string]map[string]string{"en-US": {"name": "Endpoint"}},
			Validation: definitionmodel.FieldValidation{MinLength: 2, MaxLength: 100, Min: &min, Max: &max, Pattern: "https://.*", Options: []string{"a"}, Target: "url"},
		}},
		SecretFields: []definitionmodel.FieldSchema{{Key: "api_key", Name: "API key", Type: "password"}},
	}
	overlay := integrationmodel.ConnectorProviderSchema{
		Key: "provider", I18n: map[string]map[string]string{"en-US": {"name": "Overlay"}, "zh-CN": {"name": "提供商"}},
		ConfigFields: []definitionmodel.FieldSchema{{Key: "endpoint", Config: map[string]any{"overlay": true}, I18n: map[string]map[string]string{"en-US": {"description": "Overlay endpoint"}, "zh-CN": {"name": "地址"}}}, {Key: "region_code"}},
		SecretFields: []definitionmodel.FieldSchema{{Key: "access_token"}},
	}
	got := IntegrationMergeConnectorProviderSchema(base, overlay)
	if got.Name != "Base" || got.Description != "Base description" || got.ProviderRevision != "base-v1" || !reflect.DeepEqual(got.OperationKeys, []string{"read"}) {
		t.Fatalf("provider fallbacks = %#v", got)
	}
	if got.I18n["en-US"]["name"] != "Overlay" || got.I18n["en-US"]["description"] != "Base description" || got.I18n["zh-CN"]["name"] != "提供商" {
		t.Fatalf("provider i18n = %#v", got.I18n)
	}
	endpoint := got.ConfigFields[0]
	if endpoint.Name != "Endpoint" || endpoint.Description != "Base endpoint" || endpoint.Type != "url" || endpoint.Default != "https://base" || endpoint.DefaultValue != "fallback" || endpoint.Options == nil {
		t.Fatalf("config field fallback = %#v", endpoint)
	}
	if endpoint.Config["base"] != true || endpoint.Config["overlay"] != true || endpoint.I18n["en-US"]["name"] != "Endpoint" || endpoint.I18n["zh-CN"]["name"] != "地址" {
		t.Fatalf("config field merge = %#v", endpoint)
	}
	if endpoint.Validation.MinLength != 2 || endpoint.Validation.MaxLength != 100 || endpoint.Validation.Min == nil || endpoint.Validation.Max == nil || endpoint.Validation.Pattern != "https://.*" || endpoint.Validation.Target != "url" || len(endpoint.Validation.Options) != 1 {
		t.Fatalf("validation fallback = %#v", endpoint.Validation)
	}
	if field := got.ConfigFields[1]; field.Name != "region code" || !strings.Contains(field.Description, "Configuration value") || field.I18n["zh-CN"]["description"] == "" {
		t.Fatalf("config display fallback = %#v", field)
	}
	if field := got.SecretFields[0]; field.Name != "access token" || !strings.Contains(field.Description, "Sensitive credential") || field.I18n["en-US"]["description"] == "" {
		t.Fatalf("secret display fallback = %#v", field)
	}
	got.I18n["en-US"]["name"] = "mutated"
	if base.I18n["en-US"]["name"] != "Base" {
		t.Fatal("merge mutated base localized text")
	}
}

func TestIntegrationMergeConnectorProviderSchemaPreservesExplicitOverlay(t *testing.T) {
	min, max := 3.0, 7.0
	base := integrationmodel.ConnectorProviderSchema{Key: "provider", Name: "Base", Description: "Base", OperationKeys: []string{"base"}, ConfigFields: []definitionmodel.FieldSchema{{Key: "field", Validation: definitionmodel.FieldValidation{MinLength: 1, MaxLength: 9, Pattern: "base", Options: []string{"base"}, Target: "base"}}}}
	overlay := integrationmodel.ConnectorProviderSchema{Key: "provider", ProviderRevision: "overlay-v2", Name: "Explicit", Description: "Explicit", OperationKeys: []string{"write"}, ConfigFields: []definitionmodel.FieldSchema{{Key: "field", Name: "Field", Description: "Description", Type: "text", Default: false, DefaultValue: 0, Options: []string{}, Validation: definitionmodel.FieldValidation{MinLength: 2, MaxLength: 8, Min: &min, Max: &max, Pattern: "overlay", Options: []string{"overlay"}, Target: "overlay"}}}}
	got := IntegrationMergeConnectorProviderSchema(base, overlay)
	field := got.ConfigFields[0]
	if got.Name != "Explicit" || got.Description != "Explicit" || got.ProviderRevision != "overlay-v2" || !reflect.DeepEqual(got.OperationKeys, []string{"write"}) || field.Validation.MinLength != 2 || field.Validation.MaxLength != 8 || field.Validation.Pattern != "overlay" || field.Validation.Target != "overlay" || !reflect.DeepEqual(field.Validation.Options, []string{"overlay"}) {
		t.Fatalf("explicit overlay was not preserved: %#v %#v", got, field)
	}
	if field.Default != false || field.DefaultValue != 0 || field.Options == nil {
		t.Fatalf("false/zero/empty explicit values changed: %#v", field)
	}
	if fallback := integrationConnectorFieldDisplayFallback(definitionmodel.FieldSchema{Key: "key", Name: " Explicit "}, false); fallback.Name != "Explicit" {
		t.Fatalf("explicit fallback name = %#v", fallback)
	}
}
