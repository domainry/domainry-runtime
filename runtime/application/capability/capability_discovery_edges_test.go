package capability

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func capabilityDiscoveryEdgeService() (*CapabilityAuthoringApplicationService, principalmodel.Principal) {
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{
			Objects: []definitionmodel.ObjectSchema{
				{Key: "object-a", Fields: []definitionmodel.FieldSchema{{Key: "field-a"}}},
				{Key: "object-b", Fields: []definitionmodel.FieldSchema{{Key: "field-b"}}},
			},
			Actions:   []definitionmodel.ActionSchema{{Key: "object-a.run", ObjectKey: "object-a"}, {Key: "object-b.run", ObjectKey: "object-b"}},
			Workflows: []definitionmodel.WorkflowSchema{{Key: "workflow-a"}},
			Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{
				{Key: "connector", Providers: []connectormodel.ConnectorProviderSchema{{Key: "provider"}, {Key: "unused-provider"}}, Operations: []connectormodel.ConnectorOperationSchema{{Key: "operation"}, {Key: "unused-operation"}}},
				{Key: "unused-connector", Providers: []connectormodel.ConnectorProviderSchema{{Key: "other"}}, Operations: []connectormodel.ConnectorOperationSchema{{Key: "other"}}},
			}},
		}
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	return service, admin
}

func TestCapabilityDetailSelectionReturnsOnlySelectedInstanceFacts(t *testing.T) {
	service, admin := capabilityDiscoveryEdgeService()
	detail, err := service.CapabilityDetailSelected(t.Context(), admin, "automation.rule", CapabilityDetailSelection{
		ObjectKey: "object-a", ConnectorKey: "connector", ProviderKey: "provider", OperationKey: "operation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if detail.Instance == nil || detail.Selection["object_key"] != "object-a" || detail.Selection["connector_key"] != "connector" {
		t.Fatalf("selection was not projected: %#v", detail)
	}
	instance := detail.Instance
	if len(instance.ObjectKeys) != 1 || instance.ObjectKeys[0] != "object-a" || len(instance.FieldKeys) != 1 || len(instance.FieldKeys[0].Values) != 1 || instance.FieldKeys[0].Values[0] != "field-a" {
		t.Fatalf("object selection leaked unrelated facts: %#v", instance)
	}
	if len(instance.ActionKeys) != 1 || instance.ActionKeys[0] != "object-a.run" || len(instance.PermissionKeys) != 1 || instance.PermissionKeys[0] != "object-a.run" {
		t.Fatalf("action selection leaked unrelated facts: %#v", instance)
	}
	if len(instance.ConnectorOperations) != 1 || len(instance.ConnectorOperations[0].ProviderKeys) != 1 || instance.ConnectorOperations[0].ProviderKeys[0] != "provider" || len(instance.ConnectorOperations[0].Operations) != 1 || instance.ConnectorOperations[0].Operations[0] != "operation" {
		t.Fatalf("connector selection leaked unrelated facts: %#v", instance)
	}

	unselected, err := service.CapabilityDetail(t.Context(), admin, "automation.rule")
	if err != nil || unselected.Instance != nil || len(unselected.Selection) != 0 {
		t.Fatalf("unselected detail regrew instance context: %#v err=%v", unselected, err)
	}
}

func TestCapabilityDetailSelectionRejectsInvalidOrUnboundReferences(t *testing.T) {
	service, admin := capabilityDiscoveryEdgeService()
	for _, selection := range []CapabilityDetailSelection{
		{ObjectKey: "missing"},
		{ProviderKey: "provider"},
		{OperationKey: "operation"},
		{ConnectorKey: "missing"},
		{ConnectorKey: "connector", ProviderKey: "missing"},
		{ConnectorKey: "connector", OperationKey: "missing"},
	} {
		if _, err := service.CapabilityDetailSelected(t.Context(), admin, "automation.rule", selection); err == nil {
			t.Fatalf("invalid selection accepted: %#v", selection)
		}
	}
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
}

func TestCapabilityDiscoveryReferenceEdges(t *testing.T) {
	service, admin := capabilityDiscoveryEdgeService()
	for _, test := range []struct{ kind, scope string }{
		{"workflow_key", ""}, {"provider_key", "connector"}, {"field_key", "missing"}, {"operation_key", "missing"},
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
