package capability

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestCapabilityAuthoringSourcesAndInstanceRemainingConditions(t *testing.T) {
	var nilService *CapabilityAuthoringApplicationService
	nilService.UseIdentityReferenceSource(t.Context(), nil)
	service := NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{Actions: []definitionmodel.ActionSchema{{Key: "empty"}, {Key: "allowed", RequiresPermission: "booking.approve"}}}
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	service.UseIdentityReferenceSource(t.Context(), func(context.Context, principalmodel.Principal) (CapabilityIdentityReferences, error) {
		return CapabilityIdentityReferences{}, errors.New("identity")
	})
	if _, err := service.Capabilities(t.Context(), principal); err == nil {
		t.Fatal("identity reference error ignored")
	}
	if _, err := service.DirectAuthoringSuccessProjection(t.Context(), "schema.object", principal); err == nil {
		t.Fatal("projection source error ignored")
	}
	service.identityReferences = nil
	contract, err := service.Capabilities(t.Context(), principal)
	if err != nil || len(contract.Instance.ActionKeys) != 2 {
		t.Fatalf("contract=%+v err=%v", contract.Instance, err)
	}
	if got := normalizedCapabilityReferences([]string{"", " one ", "one", "two"}); len(got) != 2 {
		t.Fatalf("references=%v", got)
	}
}

func TestDirectAuthoringSuccessProjectionRemainingContractShapes(t *testing.T) {
	contract := capabilitycontract.CapabilityRuntimeAuthoringContract{InstanceHash: "snapshot", Domains: []capabilitycontract.CapabilityAuthoringDomain{{Key: "test", Capabilities: []capabilitycontract.CapabilityAuthoringDefinition{
		{Key: "source", Status: "supported", OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{
			{VisibleTo: "caller", Type: "ignored"},
			{VisibleTo: "subsequent_capability_calls"},
			{VisibleTo: "subsequent_capability_calls", Type: "object_key"},
		}},
		{Key: "unsupported", Status: "planned", Requires: []string{"source"}},
		{Key: "successor", Status: "supported", Requires: []string{"source"}},
		{Key: "successor", Status: "supported", Requires: []string{"source"}},
	}}}}
	projection, err := directAuthoringSuccessProjection(contract, "source")
	if err != nil || len(projection.AvailableSuccessors) != 1 || projection.AvailableSuccessors[0].Key != "successor" {
		t.Fatalf("projection=%+v err=%v", projection, err)
	}
}

func TestCapabilityCatalogAndReferenceRemainingConditions(t *testing.T) {
	materializeAuthoringCapabilityPermissions(nil)
	service, admin := capabilityDiscoveryEdgeService()
	if _, err := service.ReferenceValues(t.Context(), admin, "scheduler_target_key", ""); err == nil {
		t.Fatal("empty scheduler scope accepted")
	}
	service.schema = func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{Workflows: []definitionmodel.WorkflowSchema{{Key: "scheduled:existing"}, {Key: "plain"}}}
	}
	values, err := service.ReferenceValues(t.Context(), admin, "scheduler_target_key", "workflow")
	if err != nil || len(values.Values) != 2 {
		t.Fatalf("scheduler values=%+v err=%v", values, err)
	}
	if _, err := service.ReferenceValues(t.Context(), admin, "scheduler_target_key", "report_snapshot_refresh"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReferenceValues(t.Context(), admin, "scheduler_target_key", "unknown"); err == nil {
		t.Fatal("unknown scheduler scope accepted")
	}
}
