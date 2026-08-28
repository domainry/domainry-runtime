package integration

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestConnectorRegistryRetainsOneFrozenPublicProviderRegistry(t *testing.T) {
	provider := &publicBridgeProvider{descriptor: publicBridgeDescriptor(true)}
	providers := connector.NewRegistry()
	if err := providers.Register(provider); err != nil {
		t.Fatal(err)
	}
	providers.Freeze()
	registry := NewConnectorRegistryWithProviders(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "crm", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "acme", Name: "Acme"}},
	}}}, providers)

	if registry.connectorProviders != providers {
		t.Fatal("internal ConnectorRegistry did not retain the runtimehost-owned public Registry instance")
	}
	if _, ok := registry.ProviderAdapter("crm", "acme"); !ok {
		t.Fatal("exact public provider did not resolve from the retained Registry")
	}
	if _, ok := registry.ProviderAdapter("crm", ""); ok {
		t.Fatal("public provider resolved through connector-only fallback")
	}
	_, readiness := registry.Catalog()
	if !readiness["crm:acme"] {
		t.Fatalf("public provider missing from catalog readiness: %#v", readiness)
	}
	projected := registry.Schema().Connectors[0].Providers[0]
	if projected.ProviderRevision != "provider-v3" || len(projected.OperationKeys) != 1 {
		t.Fatalf("public provider schema was not projected from the retained Registry: %#v", projected)
	}

	registry.ReplaceSchema(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "crm", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "acme", Name: "Replacement"}}}}})
	if got := registry.Schema().Connectors[0].Providers[0]; got.Name != "Replacement" || got.ProviderRevision != "provider-v3" {
		t.Fatalf("replacement schema lost public provider overlay: %#v", got)
	}
	registry.MergeConnectors([]integrationmodel.ConnectorSchema{{Key: "crm", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "acme", Description: "Builtin"}}}})
	if got := registry.Schema().Connectors[0].Providers[0]; got.Description != "Builtin" || got.ProviderRevision != "provider-v3" {
		t.Fatalf("builtin schema lost public provider overlay: %#v", got)
	}
}

func TestConnectorRegistryRequiresFrozenPublicProviders(t *testing.T) {
	defer func() {
		recovered := recover()
		if recovered == nil || !strings.Contains(fmt.Sprint(recovered), "requires a frozen connector Registry") {
			t.Fatalf("mutable public Registry was not rejected: %#v", recovered)
		}
	}()
	NewConnectorRegistryWithProviders(integrationmodel.IntegrationSchema{}, connector.NewRegistry())
}

func TestConnectorRegistryHasNoLegacyAdapterRegistrationSurface(t *testing.T) {
	if _, exists := reflect.TypeOf((*ConnectorRegistry)(nil)).MethodByName("RegisterProviderAdapter"); exists {
		t.Fatal("ConnectorRegistry still exposes legacy provider adapter registration")
	}
	if _, exists := reflect.TypeOf((*IntegrationApplicationService)(nil)).MethodByName("RegisterProviderIntegrationAdapter"); exists {
		t.Fatal("IntegrationApplicationService still exposes legacy provider adapter registration")
	}
}

func TestConnectorRegistryCreatesFrozenEmptyPublicRegistryWhenOmitted(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	if registry.connectorProviders == nil || !registry.connectorProviders.Frozen() {
		t.Fatalf("empty public Registry was not normalized and frozen: %#v", registry.connectorProviders)
	}
	release, err := registry.AcquireCredentialLease(t.Context(), " ")
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestConnectorRegistryBuiltinProviderWithoutAdapterRemainsUnmerged(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	registry.MergeConnectors([]integrationmodel.ConnectorSchema{{Key: "missing", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "absent", Name: "Absent"}}}})
	connector := registry.Schema().Connectors[0]
	if connector.Providers[0].Name != "Absent" {
		t.Fatalf("connector=%+v", connector)
	}
}

func TestConnectorRegistryIsIndependentAndRuntimeBuiltinOwnsDuplicateKey(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "payment", Provider: "manifest"}}})
	registry.MergeConnectors([]integrationmodel.ConnectorSchema{{Key: "payment", Type: "payment", Provider: "runtime"}, {Key: "email", Type: "email", Provider: "smtp"}})
	schema := registry.Schema()
	if len(schema.Connectors) != 2 || schema.Connectors[0].Key != "email" || schema.Connectors[1].Provider != "runtime" {
		t.Fatalf("unexpected independent registry schema: %#v", schema.Connectors)
	}
	registry.ReplaceSchema(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "payment", Provider: "changed-manifest"}}, EventMappings: []integrationmodel.IntegrationEventMappingSchema{{Key: "event"}}})
	schema = registry.Schema()
	if len(schema.Connectors) != 2 || schema.Connectors[1].Provider != "runtime" || len(schema.EventMappings) != 1 {
		t.Fatalf("schema replacement lost builtin ownership or event mappings: %#v", schema)
	}
}

func TestConnectorRegistryLeaseOverlayAndReadinessEdges(t *testing.T) {
	descriptor := publicBridgeDescriptor(false)
	descriptor.ConnectorKey, descriptor.ProviderKey, descriptor.ProviderRevision = "payment", "stripe", "stripe-v1"
	descriptor.Operations[0].ConnectorKey, descriptor.Operations[0].ProviderKey, descriptor.Operations[0].Key = "payment", "stripe", "charge"
	providers := connector.NewRegistry()
	if err := providers.Register(&publicBridgeProvider{descriptor: descriptor}); err != nil {
		t.Fatal(err)
	}
	providers.Freeze()
	registry := NewConnectorRegistryWithProviders(integrationmodel.IntegrationSchema{}, providers)
	release, err := registry.AcquireCredentialLease(t.Context(), " ")
	if err != nil || release == nil {
		t.Fatalf("blank lease missing=%v err=%v", release == nil, err)
	}
	release()
	first, err := registry.AcquireCredentialLease(t.Context(), "key")
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := registry.AcquireCredentialLease(cancelled, "key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lease cancellation error=%v", err)
	}
	first()
	first()

	registry.MergeConnectors([]integrationmodel.ConnectorSchema{{Key: " "}, {Key: "payment", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "stripe"}}}})
	registry.MergeConnectors([]integrationmodel.ConnectorSchema{{Key: "payment", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "stripe"}}}})
	provider := registry.Schema().Connectors[0].Providers[0]
	if len(provider.OperationKeys) != 1 || provider.OperationKeys[0] != "charge" {
		t.Fatalf("builtin provider overlay=%#v", provider)
	}
	registry.ReplaceSchema(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "payment", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "stripe"}}}}})
	provider = registry.Schema().Connectors[0].Providers[0]
	if len(provider.OperationKeys) != 1 {
		t.Fatalf("replacement provider overlay=%#v", provider)
	}

	if !registry.AdapterReady(integrationmodel.ConnectorSchema{Key: "payment", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "stripe"}}}) {
		t.Fatal("provider connector not ready")
	}
	if registry.AdapterReady(integrationmodel.ConnectorSchema{Key: "missing", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "none"}}}) {
		t.Fatal("missing connector reported ready")
	}
}

func TestIntegrationApplicationEventAndOutboxRegistrationConditionEdges(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	service.RegisterIntegrationEventHandler(" ", integrationEventWorkerHandlerFunc(nil))
	service.RegisterIntegrationEventHandler("provider", nil)
	service.RegisterIntegrationOutboxSender(" ", integrationOutboxSenderFunc(nil))
	service.RegisterIntegrationOutboxSender("connector", nil)

	fresh := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	fresh.ReplaceSchema(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
		{Key: " ", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "none"}}},
		{Key: "existing", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "none"}}},
	}})
	if len(fresh.Schema().Connectors) != 1 {
		t.Fatalf("fresh registry schema=%#v", fresh.Schema())
	}
}
