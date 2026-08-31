package capability

import (
	"reflect"
	"testing"

	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

func TestRuntimeAuthoringCapabilitiesAreValidAndDeterministic(t *testing.T) {
	first := RuntimeAuthoringCapabilities()
	second := RuntimeAuthoringCapabilities()
	if err := first.Validate(); err != nil {
		t.Fatalf("validate authoring capability contract: %v", err)
	}
	if !reflect.DeepEqual(first, second) || first.ContractHash == "" {
		t.Fatal("authoring capability contract must be deterministic")
	}
	if first.ContractHash != RuntimeAuthoringContractHash {
		t.Fatalf("published authoring contract hash is stale: catalog=%s published=%s", first.ContractHash, RuntimeAuthoringContractHash)
	}
	if first.SurfaceContractVersion != surfacemodel.ContractVersion {
		t.Fatalf("surface contract version=%q want=%q", first.SurfaceContractVersion, surfacemodel.ContractVersion)
	}
	endpointContracts := tenantAdminEndpointSurfaceContracts()
	if len(endpointContracts) == 0 {
		t.Fatal("capability discovery must publish compiled endpoint Surface contracts")
	}
	for _, endpointContract := range endpointContracts {
		if err := endpointContract.Validate(); err != nil {
			t.Fatalf("invalid discovered endpoint contract %q: %v", endpointContract.EndpointIdentity, err)
		}
	}
	for _, domain := range first.Domains {
		for _, capability := range domain.Capabilities {
			if capability.Surface != surfacemodel.ProductSurfaceAdminConsole ||
				len(capability.ActorAudiences) != 1 ||
				capability.ActorAudiences[0] != surfacemodel.ActorAudiencePlatformAdmin ||
				capability.ExposureClass != surfacemodel.ExposureClassPlatformAdmin {
				t.Fatalf("capability %s has incomplete Surface contract: %+v", capability.Key, capability)
			}
		}
	}
}

func TestRuntimeAuthoringErrorContractClassification(t *testing.T) {
	contract := RuntimeAuthoringErrorContract("backend.integration.connector.operation_method_invalid", map[string]string{"field": "operations[0].method"})
	if contract.CapabilityKey != "integration.catalog" || contract.FieldPath != "operations[0].method" {
		t.Fatalf("unexpected authoring error contract: %#v", contract)
	}
}

func TestRuntimeAuthoringErrorContractFallbackClassification(t *testing.T) {
	unknown := RuntimeAuthoringErrorContract("backend.example.unknown", map[string]string{"field_path": "items[2].value"})
	if unknown.ContractVersion == "" || unknown.CapabilityKey != "" || unknown.FieldPath != "items[2].value" {
		t.Fatalf("unexpected unknown fallback: %#v", unknown)
	}
}
