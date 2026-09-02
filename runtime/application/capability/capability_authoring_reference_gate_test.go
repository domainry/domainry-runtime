package capability

import (
	"context"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRuntimeAuthoringReferenceParametersAreDeclared(t *testing.T) {
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, definition := range domain.Capabilities {
			declared := map[string]bool{}
			for _, reference := range definition.ReferenceContracts {
				declared[reference.InputJSONPointer] = true
				if strings.TrimSpace(reference.Kind) == "" || strings.TrimSpace(reference.ResolverEndpoint) == "" {
					t.Errorf("%s has incomplete reference contract %#v", definition.Key, reference)
				}
			}
			for _, parameter := range definition.Parameters {
				// A stable identity named "key" declares the resource being created;
				// it is not a reference to an existing resource.
				if parameter.Key == "key" || !authoringReferenceParameterType(parameter.Type) {
					continue
				}
				pointer := "/" + parameter.Key
				if !declared[pointer] && !declared["/@path/"+parameter.Key] && !authoringHasReferencePointerSuffix(declared, pointer) {
					t.Errorf("%s reference parameter %s (%s) has no reference_contract", definition.Key, parameter.Key, parameter.Type)
				}
			}
		}
	}
}

func TestEveryPublishedPlatformReferenceKindUsesSnapshotBoundResolver(t *testing.T) {
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{
			Objects:      []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status"}}}},
			Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{{Key: "erp", Providers: []connectormodel.ConnectorProviderSchema{{Key: "default"}}, Operations: []connectormodel.ConnectorOperationSchema{{Key: "sync"}}}}},
		}
	})
	service.UseIdentityReferenceSource(t.Context(), func(context.Context, principalmodel.Principal) (CapabilityIdentityReferences, error) {
		return CapabilityIdentityReferences{}, nil
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	checked := map[string]bool{}
	for _, domain := range RuntimeAuthoringCapabilities().Domains {
		for _, definition := range domain.Capabilities {
			for _, reference := range definition.ReferenceContracts {
				if !strings.Contains(reference.ResolverEndpoint, "/tenant-admin/platform-capabilities/references/") || checked[reference.Kind] {
					continue
				}
				scope := ""
				switch reference.Kind {
				case "field_key":
					scope = "order"
				case "scheduler_target_key":
					scope = "workflow"
				case "operation_key", "provider_key":
					scope = "erp"
				}
				result, err := service.ReferenceValues(t.Context(), principal, reference.Kind, scope)
				if err != nil || result.InstanceHash == "" || result.Kind != reference.Kind {
					t.Errorf("capability=%s reference=%s result=%#v err=%v", definition.Key, reference.Kind, result, err)
				}
				checked[reference.Kind] = true
			}
		}
	}
	if len(checked) == 0 {
		t.Fatal("authoring catalog publishes no platform reference contracts")
	}
}

func authoringHasReferencePointerSuffix(declared map[string]bool, suffix string) bool {
	for pointer := range declared {
		if strings.HasSuffix(pointer, suffix) {
			return true
		}
	}
	return false
}

func authoringReferenceParameterType(value string) bool {
	switch value {
	case "object_key", "relation_target_object_key", "field_key", "action_key", "workflow_key", "report_key", "role_key", "permission_key", "connector_key", "connection_key", "operation_key", "user_id", "org_id", "role_id", "menu_id":
		return true
	default:
		return false
	}
}
