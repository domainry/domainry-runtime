package integration

import integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type providerMarkerAdapter struct{ provider string }

func (adapter providerMarkerAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{Response: map[string]any{"provider": adapter.provider}}, nil
}

type providerSchemaOverlayAdapter struct{ providerMarkerAdapter }

func (providerSchemaOverlayAdapter) ProviderSchema() integrationmodel.ConnectorProviderSchema {
	return integrationmodel.ConnectorProviderSchema{OperationKeys: []string{"charge"}, ConfigFields: []definitionmodel.FieldSchema{{Key: "endpoint", Name: "Endpoint", Type: "text", Required: true}}}
}

func TestProviderAdapterRegistrySeparatesProvidersWithinConnector(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
	registerTestProviderAdapter(service, "payment", "stripe", providerMarkerAdapter{provider: "stripe"})
	registerTestProviderAdapter(service, "payment", "paypal", providerMarkerAdapter{provider: "paypal"})
	for _, provider := range []string{"stripe", "paypal"} {
		adapter, ok := service.AdapterForConnection(integrationmodel.IntegrationConnection{ConnectorKey: "payment", ProviderKey: provider})
		if !ok {
			t.Fatalf("provider adapter %s was not registered", provider)
		}
		result, err := adapter.Call(t.Context(), integrationcontract.CallRequest{ConnectorKey: "payment", Connection: integrationmodel.IntegrationConnection{ConnectorKey: "payment", ProviderKey: provider}, Operation: "test_connection"})
		if err != nil || result.Response["provider"] != provider {
			t.Fatalf("provider adapter %s result=%#v err=%v", provider, result, err)
		}
	}
}

func TestProviderAdapterSchemaOverlayPreservesDescriptorLocalization(t *testing.T) {
	localized := localizationmodel.LocalizedTextMap{"en-US": {"name": "Stripe", "description": "Stripe provider"}, "zh-CN": {"name": "Stripe", "description": "Stripe 支付 Provider"}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
		{Key: "payment", Providers: []integrationmodel.ConnectorProviderSchema{
			{Key: "stripe", Name: "Stripe", Description: "Stripe provider", I18n: localized, ConfigFields: []definitionmodel.FieldSchema{
				{Key: "endpoint", Description: "Endpoint", Type: "text", Default: "https://api.example", Validation: definitionmodel.FieldValidation{MaxLength: 2048}, Config: map[string]any{"contract_owner": "runtime"}, I18n: localized},
			}},
		}},
	}})
	application := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	registerTestProviderAdapter(application, "payment", "stripe", providerSchemaOverlayAdapter{})

	provider := registry.Schema().Connectors[0].Providers[0]
	if provider.Name != "Stripe" || provider.Description != "Stripe provider" || provider.I18n["zh-CN"]["description"] != "Stripe 支付 Provider" {
		t.Fatalf("descriptor localization was lost: %+v", provider)
	}
	if len(provider.ConfigFields) != 1 || provider.ConfigFields[0].Key != "endpoint" {
		t.Fatalf("adapter schema was not overlaid: %+v", provider.ConfigFields)
	}
	if provider.ConfigFields[0].Description == "" || provider.ConfigFields[0].I18n["zh-CN"]["description"] == "" {
		t.Fatalf("adapter field localization fallback was not merged: %+v", provider.ConfigFields[0])
	}
	if provider.ConfigFields[0].Default != "https://api.example" || provider.ConfigFields[0].Validation.MaxLength != 2048 || provider.ConfigFields[0].Config["contract_owner"] != "connector" {
		t.Fatalf("descriptor field constraints were lost: %+v", provider.ConfigFields[0])
	}
	if !application.ProviderSupportsIntegrationOperation(integrationmodel.IntegrationConnection{ConnectorKey: "payment", ProviderKey: "stripe"}, "charge") {
		t.Fatal("provider-owned operation was rejected")
	}
	if application.ProviderSupportsIntegrationOperation(integrationmodel.IntegrationConnection{ConnectorKey: "payment", ProviderKey: "stripe"}, "refund") {
		t.Fatal("operation outside the provider closed set was accepted")
	}
}
