package capability

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestAuthoringContractDisclosesInstalledAssigneeResolverDescriptors(t *testing.T) {
	descriptors := []capabilitycontract.CapabilityAuthoringAssigneeResolver{{
		ResolverKey: "regional_approver", ResolverRevision: "v1", ConfigContractSHA256: "hash",
		ConfigFields:         []capabilitycontract.CapabilityAuthoringAssigneeResolverConfig{{Key: "region", Type: "string", Required: true, Enum: []string{"west", "east"}}},
		RecordCapabilities:   []capabilitycontract.CapabilityAuthoringAssigneeRecordCapability{{Key: "order", ObjectKey: "order", Fields: []string{"region"}, FilterFields: []string{}, MaxRows: 1}},
		RelationCapabilities: []capabilitycontract.CapabilityAuthoringAssigneeRelation{}, IdentityProjections: []string{"find_user"}, CandidateRoleKeys: []string{"approver"},
		MaxReadOperations: 3, MaxCandidates: 10, TimeoutMilliseconds: 200,
	}}
	service := NewCapabilityAuthoringApplicationService(nil)
	service.UseAssigneeResolverReferenceSource(func() []capabilitycontract.CapabilityAuthoringAssigneeResolver { return descriptors })
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
	contract, err := service.Capabilities(t.Context(), principal)
	if err != nil || len(contract.Instance.AssigneeResolvers) != 1 || contract.Instance.AssigneeResolvers[0].ResolverKey != "regional_approver" {
		t.Fatalf("contract=%+v err=%v", contract.Instance.AssigneeResolvers, err)
	}
	contract.Instance.AssigneeResolvers[0].ConfigFields[0].Enum[0] = "mutated"
	again, err := service.Capabilities(t.Context(), principal)
	if err != nil || again.Instance.AssigneeResolvers[0].ConfigFields[0].Enum[0] != "east" {
		t.Fatalf("descriptor projection aliases caller state: %+v err=%v", again.Instance.AssigneeResolvers, err)
	}
	references, err := service.ReferenceValues(t.Context(), principal, "assignee_resolver_key", "")
	if err != nil || len(references.Values) != 1 || references.Values[0] != "regional_approver" || references.InstanceHash == "" {
		t.Fatalf("references=%+v err=%v", references, err)
	}
}
