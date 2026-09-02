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
			Objects:   []definitionmodel.ObjectSchema{{Key: "object-a", Fields: []definitionmodel.FieldSchema{{Key: "field-a"}}}},
			Workflows: []definitionmodel.WorkflowSchema{{Key: "workflow-a"}},
			Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{{
				Key: "connector", Providers: []connectormodel.ConnectorProviderSchema{{Key: "provider"}}, Operations: []connectormodel.ConnectorOperationSchema{{Key: "operation"}},
			}}},
		}
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	return service, admin
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
