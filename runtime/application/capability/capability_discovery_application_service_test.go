package capability

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestCapabilityDiscoveryProgressivelyLoadsRuntimeOwnedDomains(t *testing.T) {
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{
			Objects:   []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status"}}}},
			Actions:   []definitionmodel.ActionSchema{{Key: "order.confirm"}},
			Workflows: []definitionmodel.WorkflowSchema{{Key: "order.approval"}},
		}
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	index, err := service.DiscoveryIndex(t.Context(), admin)
	if err != nil || len(index.Domains) != 6 || index.ContractHash == "" || index.InstanceHash == "" || index.EndpointContractCount == 0 || index.EndpointContractsHash == "" {
		t.Fatalf("index=%#v err=%v", index, err)
	}
	if len(index.EndpointContracts) != 0 {
		t.Fatalf("default discovery index expanded %d endpoint contracts", len(index.EndpointContracts))
	}
	encoded, err := json.Marshal(index)
	if err != nil || json.Valid(encoded) == false || string(encoded) == "" {
		t.Fatalf("marshal compact index: %s err=%v", encoded, err)
	}
	var wire map[string]any
	_ = json.Unmarshal(encoded, &wire)
	if _, expanded := wire["endpoint_contracts"]; expanded {
		t.Fatalf("compact index leaked endpoint contracts: %s", encoded)
	}
	expanded, err := service.DiscoveryIndexExpanded(t.Context(), admin, true)
	if err != nil || len(expanded.EndpointContracts) != index.EndpointContractCount || expanded.EndpointContractsHash != index.EndpointContractsHash {
		t.Fatalf("expanded index=%#v err=%v", expanded, err)
	}
	for _, domain := range index.Domains {
		if domain.Key == "identity" || domain.Key == "integration" || domain.Key == "report" || domain.Key == "scheduler" {
			t.Fatalf("Runtime discovery merged external owner %q", domain.Key)
		}
	}
	domain, err := service.DomainCapabilities(t.Context(), admin, "schema", CapabilityDiscoveryFilter{Status: "supported"})
	if err != nil || len(domain.Capabilities) != 4 {
		t.Fatalf("domain=%#v err=%v", domain, err)
	}
	detail, err := service.CapabilityDetail(t.Context(), admin, "schema.object")
	if err != nil || detail.Domain != "schema" || detail.Capability.InputSchema == nil || detail.Capability.Key != "schema.object" {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
	for _, capabilityKey := range []string{"identity.workspace_provision", "integration.connection", "report.definition", "scheduler.business_job"} {
		if _, err := service.CapabilityDetail(t.Context(), admin, capabilityKey); apperror.CodeOf(err) != "backend.capability.not_found" {
			t.Fatalf("external capability %q remained discoverable: %v", capabilityKey, err)
		}
	}
	if _, err := service.DomainCapabilities(t.Context(), admin, "missing", CapabilityDiscoveryFilter{}); apperror.CodeOf(err) != "backend.capability.domain_not_found" {
		t.Fatalf("domain error=%v", err)
	}
}

func TestCapabilityDiscoveryPropagatesIdentityReferenceSourceFailure(t *testing.T) {
	want := errors.New("identity reference source unavailable")
	service := NewCapabilityAuthoringApplicationService(nil)
	service.UseIdentityReferenceSource(t.Context(), func(context.Context, principalmodel.Principal) (CapabilityIdentityReferences, error) {
		return CapabilityIdentityReferences{}, want
	})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"runtime.appschema.validate_application_definition"}})
	if _, err := service.DiscoveryIndex(t.Context(), admin); !errors.Is(err, want) {
		t.Fatalf("discovery error=%v", err)
	}
}

func TestCapabilityDiscoveryHashPreconditionsReturnStableDriftCodes(t *testing.T) {
	if err := ValidateCapabilityDiscoveryHashes("old", "current", "", "instance"); apperror.CodeOf(err) != "backend.capability.contract_drift" {
		t.Fatalf("contract drift error=%v", err)
	}
	if err := ValidateCapabilityDiscoveryHashes("current", "current", "old", "instance"); apperror.CodeOf(err) != "backend.capability.instance_drift" {
		t.Fatalf("instance drift error=%v", err)
	}
	if err := ValidateCapabilityDiscoveryHashes("current", "current", "instance", "instance"); err != nil {
		t.Fatalf("matching hashes error=%v", err)
	}
}
