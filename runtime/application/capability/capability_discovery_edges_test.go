package capability

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func capabilityDiscoveryEdgeService() (*CapabilityAuthoringApplicationService, principalmodel.Principal) {
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{
			Objects:   []definitionmodel.ObjectSchema{{Key: "object-a", Fields: []definitionmodel.FieldSchema{{Key: "field-a"}}}},
			Workflows: []definitionmodel.WorkflowSchema{{Key: "workflow-a"}}, Reports: []reportmodel.ReportSchema{{Key: "report-a"}},
			Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
				{Key: "first"},
				{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "first-provider"}, {Key: "provider"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "first-operation"}, {Key: "operation"}}},
			}},
		}
	})
	return service, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
}

func TestCapabilityDiscoveryAuthorizationAndDomainFilterEdges(t *testing.T) {
	service, admin := capabilityDiscoveryEdgeService()
	unknown := principalmodel.Principal{}
	if _, err := service.DomainCapabilities(t.Context(), unknown, "schema", CapabilityDiscoveryFilter{}); err == nil {
		t.Fatal("unauthorized domain discovery accepted")
	}
	if _, err := service.CapabilityDetail(t.Context(), unknown, "schema.object"); err == nil {
		t.Fatal("unauthorized detail accepted")
	}
	if _, err := service.ReferenceValues(t.Context(), unknown, "object_key", ""); err == nil {
		t.Fatal("unauthorized references accepted")
	}

	unfiltered, err := service.DomainCapabilities(t.Context(), admin, "schema", CapabilityDiscoveryFilter{})
	if err != nil || len(unfiltered.Capabilities) == 0 {
		t.Fatalf("unfiltered=%#v err=%v", unfiltered, err)
	}
	if filtered, err := service.DomainCapabilities(t.Context(), admin, "schema", CapabilityDiscoveryFilter{Status: "missing"}); err != nil || len(filtered.Capabilities) != 0 {
		t.Fatalf("status filtered=%#v err=%v", filtered, err)
	}
	if filtered, err := service.DomainCapabilities(t.Context(), admin, "schema", CapabilityDiscoveryFilter{Requires: "missing"}); err != nil || len(filtered.Capabilities) != 0 {
		t.Fatalf("requires filtered=%#v err=%v", filtered, err)
	}

	contract := RuntimeAuthoringCapabilities()
	for _, domain := range contract.Domains {
		for _, definition := range domain.Capabilities {
			if len(definition.Requires) == 0 {
				continue
			}
			filtered, err := service.DomainCapabilities(t.Context(), admin, domain.Key, CapabilityDiscoveryFilter{Requires: definition.Requires[0]})
			if err != nil || len(filtered.Capabilities) == 0 {
				t.Fatalf("matching require=%q domain=%s result=%#v err=%v", definition.Requires[0], domain.Key, filtered, err)
			}
			return
		}
	}
	t.Fatal("authoring contract has no required capability")
}

func TestCapabilityDiscoverySelectionErrorAndOptionalEdges(t *testing.T) {
	service, admin := capabilityDiscoveryEdgeService()
	if detail, err := service.CapabilityDetailSelected(t.Context(), admin, "seed.record", CapabilityDetailSelection{}); err != nil || len(detail.Selection) != 0 {
		t.Fatalf("generic seed=%#v err=%v", detail, err)
	}
	if detail, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.connection.rotate", CapabilityDetailSelection{}); err != nil || len(detail.Selection) != 0 {
		t.Fatalf("generic rotate=%#v err=%v", detail, err)
	}

	connectionSelections := []CapabilityDetailSelection{
		{ProviderKey: "provider"},
		{ConnectorKey: "missing", ProviderKey: "provider"},
	}
	for _, selection := range connectionSelections {
		if _, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.connection", selection); err == nil {
			t.Fatalf("connection selection accepted: %#v", selection)
		}
	}
	if _, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.connection.rotate", CapabilityDetailSelection{ConnectorKey: "connector", ProviderKey: "provider"}); err != nil {
		t.Fatalf("rotate specialization=%v", err)
	}

	if detail, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.operation_test", CapabilityDetailSelection{}); err != nil || len(detail.Selection) != 0 {
		t.Fatalf("generic operation=%#v err=%v", detail, err)
	}
	for _, selection := range []CapabilityDetailSelection{{OperationKey: "operation"}, {ConnectorKey: "connector"}, {ConnectorKey: "missing", OperationKey: "operation"}} {
		if _, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.operation_test", selection); err == nil {
			t.Fatalf("operation selection accepted: %#v", selection)
		}
	}

	if detail, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.binding_validation", CapabilityDetailSelection{}); err != nil || len(detail.Selection) != 0 {
		t.Fatalf("generic binding=%#v err=%v", detail, err)
	}
	for _, selection := range []CapabilityDetailSelection{
		{ProviderKey: "provider"}, {OperationKey: "operation"}, {ConnectorKey: "missing"},
		{ConnectorKey: "connector", ProviderKey: "missing"}, {ConnectorKey: "connector", OperationKey: "missing"},
	} {
		if _, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.binding_validation", selection); err == nil {
			t.Fatalf("binding selection accepted: %#v", selection)
		}
	}
	providerOnly, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.binding_validation", CapabilityDetailSelection{ConnectorKey: "connector", ProviderKey: "provider"})
	if err != nil || providerOnly.Selection["provider_key"] != "provider" || providerOnly.Selection["operation_key"] != "" {
		t.Fatalf("provider-only=%#v err=%v", providerOnly, err)
	}
	operationOnly, err := service.CapabilityDetailSelected(t.Context(), admin, "integration.binding_validation", CapabilityDetailSelection{ConnectorKey: "connector", OperationKey: "operation"})
	if err != nil || operationOnly.Selection["operation_key"] != "operation" || operationOnly.Selection["provider_key"] != "" {
		t.Fatalf("operation-only=%#v err=%v", operationOnly, err)
	}
}

func TestCapabilityDiscoveryLookupAndReferenceEdges(t *testing.T) {
	service, admin := capabilityDiscoveryEdgeService()
	snapshot := capabilitycontract.CapabilityInstanceSchema{
		Objects:      []definitionmodel.ObjectSchema{{Key: "first"}, {Key: "target"}},
		Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "first"}, {Key: "target", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "first"}, {Key: "target"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "first"}, {Key: "target"}}}}},
	}
	if object, found := capabilityObjectByKey(snapshot, "target"); !found || object.Key != "target" {
		t.Fatalf("object=%#v found=%v", object, found)
	}
	if _, found := capabilityObjectByKey(snapshot, "missing"); found {
		t.Fatal("missing object found")
	}
	connector, found := capabilityConnectorByKey(snapshot, "target")
	if !found || connector.Key != "target" {
		t.Fatalf("connector=%#v found=%v", connector, found)
	}
	if _, found := capabilityConnectorByKey(snapshot, "missing"); found {
		t.Fatal("missing connector found")
	}
	if provider := capabilityConnectorProviderByKey(&connector, "target"); provider == nil || provider.Key != "target" {
		t.Fatalf("provider=%#v", provider)
	}
	if capabilityConnectorProviderByKey(&connector, "missing") != nil {
		t.Fatal("missing provider found")
	}
	if operation := capabilityConnectorOperationByKey(&connector, "target"); operation == nil || operation.Key != "target" {
		t.Fatalf("operation=%#v", operation)
	}
	if capabilityConnectorOperationByKey(&connector, "missing") != nil {
		t.Fatal("missing operation found")
	}

	for _, test := range []struct{ kind, scope string }{
		{"workflow_key", ""}, {"report_key", ""}, {"provider_key", "connector"},
		{"field_key", "missing"}, {"operation_key", "missing"}, {"provider_key", "missing"},
	} {
		if _, err := service.ReferenceValues(t.Context(), admin, test.kind, test.scope); err != nil {
			t.Fatalf("kind=%s scope=%s err=%v", test.kind, test.scope, err)
		}
	}
	for _, kind := range []string{"operation_key", "provider_key"} {
		if _, err := service.ReferenceValues(t.Context(), admin, kind, ""); err == nil {
			t.Fatalf("scope-less %s accepted", kind)
		}
	}
	if _, err := service.ReferenceValues(t.Context(), admin, "invalid", ""); err == nil {
		t.Fatal("invalid reference kind accepted")
	}
}

func TestCapabilityDiscoveryHashOptionalPreconditions(t *testing.T) {
	if err := ValidateCapabilityDiscoveryHashes("", "contract", "", "instance"); err != nil {
		t.Fatalf("optional hashes=%v", err)
	}
	if err := ValidateCapabilityDiscoveryHashes("contract", "contract", "", "instance"); err != nil {
		t.Fatalf("optional instance=%v", err)
	}
}
