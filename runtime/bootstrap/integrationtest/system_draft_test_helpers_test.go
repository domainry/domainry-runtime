package integrationtest

import (
	"encoding/json"
	"net/http"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
)

func publishSystemDefinitionUpdateFixture(t *testing.T, handler http.Handler, role, authorID, approverID, planID string, current appschemamodel.ApplicationDefinition, after any, capabilityKey string) appschemamodel.ApplicationDefinition {
	t.Helper()
	afterPayload, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + integrationIdentityAccessTokenFor(authorID, role)}
	snapshot := runtimeFixtureRequestWithHeaders[changeplanprojection.BusinessSystemSnapshot](t, handler, role, http.MethodGet, "/domain-system-snapshot", nil, headers)
	graph := runtimeFixtureRequestWithHeaders[changeplanmodel.ReferenceGraph](t, handler, role, http.MethodGet, "/domain-reference-graph", nil, headers)
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: planID, BusinessReason: "Publish integration-test definition through reviewed system draft",
		SnapshotHash: snapshot.SnapshotHash, ReferenceGraphHash: graph.Hash, RuntimeVersion: snapshot.RuntimeVersion,
		AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		ReleaseOrder: []string{"definition"}, RollbackOrder: []string{"definition"},
		Items: []changeplanmodel.BusinessSystemChangeItem{{
			ItemID: "definition", Operation: "update", ChangeKind: "compatible", RiskLevel: "high",
			ResourceType: current.ResourceType, ResourceKey: current.ResourceKey, ResourceOwner: changeplanpolicy.ChangePlanResourceOwnerForSourceKind(current.SourceKind),
			ExpectedResourceHash: current.SchemaHash, OwnerAuthorized: true, CapabilityKey: capabilityKey,
			Before: current.Payload, After: afterPayload, ValidationMethods: []string{"metadata.candidate.validate"}, RollbackMethod: "restore_as_new_system_draft",
		}},
	}
	draft := runtimeFixtureRequestWithHeaders[changeplanmodel.BusinessChangePlanDraft](t, handler, role, http.MethodPut, "/tenant-admin/change-plans/"+planID, map[string]any{"expected_revision": 0, "plan": plan}, headers)
	type transitionResponse struct {
		Draft changeplanmodel.BusinessChangePlanDraft `json:"draft"`
	}
	inReview := runtimeFixtureRequestWithHeaders[transitionResponse](t, handler, role, http.MethodPost, "/tenant-admin/change-plans/"+planID+"/review", map[string]any{"expected_revision": draft.Revision}, headers)
	approved := runtimeFixtureRequestWithHeaders[transitionResponse](t, handler, role, http.MethodPost, "/tenant-admin/change-plans/"+planID+"/approve", map[string]any{"expected_revision": inReview.Draft.Revision}, map[string]string{"Authorization": "Bearer " + integrationIdentityAccessTokenFor(approverID, role)})
	runtimeFixtureRequestWithHeaders[map[string]any](t, handler, role, http.MethodPost, "/tenant-admin/change-plans/apply", map[string]any{"plan_id": planID, "expected_revision": approved.Draft.Revision, "confirmation": planID}, map[string]string{"Authorization": "Bearer " + integrationIdentityAccessTokenFor(authorID, role), "Idempotency-Key": "apply:" + planID})
	return loadApplicationDefinitionFixture(t, handler, role, current.ResourceType, current.ResourceKey)
}

func publishSystemDefinitionCreateFixture(t *testing.T, handler http.Handler, role, authorID, approverID, planID, resourceType, resourceKey string, after any, capabilityKey string) (appschemamodel.ApplicationDefinition, changeplanmodel.BusinessChangePlanDraft) {
	t.Helper()
	afterPayload, err := json.Marshal(after)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + integrationIdentityAccessTokenFor(authorID, role)}
	snapshot := runtimeFixtureRequestWithHeaders[changeplanprojection.BusinessSystemSnapshot](t, handler, role, http.MethodGet, "/domain-system-snapshot", nil, headers)
	graph := runtimeFixtureRequestWithHeaders[changeplanmodel.ReferenceGraph](t, handler, role, http.MethodGet, "/domain-reference-graph", nil, headers)
	itemID := resourceType + ":" + resourceKey
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: planID,
		BusinessReason:           "Publish integration-test definition through independently reviewed system draft",
		SnapshotHash:             snapshot.SnapshotHash,
		ReferenceGraphHash:       graph.Hash,
		RuntimeVersion:           snapshot.RuntimeVersion,
		AuthoringContractVersion: snapshot.AuthoringContractVersion,
		AuthoringContractHash:    snapshot.AuthoringContractHash,
		ReleaseOrder:             []string{itemID},
		RollbackOrder:            []string{itemID},
		Items: []changeplanmodel.BusinessSystemChangeItem{{
			ItemID: itemID, Operation: "create", ChangeKind: "additive", RiskLevel: "high",
			ResourceType: resourceType, ResourceKey: resourceKey, ResourceOwner: "manual",
			OwnerAuthorized: true, CapabilityKey: capabilityKey, After: afterPayload,
			ValidationMethods: []string{"metadata.validate", "automation.validate", "reference_graph.validate"},
			RollbackMethod:    "restore_as_new_system_draft",
		}},
	}
	draft := runtimeFixtureRequestWithHeaders[changeplanmodel.BusinessChangePlanDraft](t, handler, role, http.MethodPut, "/tenant-admin/change-plans/"+planID, map[string]any{"expected_revision": 0, "plan": plan}, headers)
	type transitionResponse struct {
		Draft changeplanmodel.BusinessChangePlanDraft `json:"draft"`
	}
	inReview := runtimeFixtureRequestWithHeaders[transitionResponse](t, handler, role, http.MethodPost, "/tenant-admin/change-plans/"+planID+"/review", map[string]any{"expected_revision": draft.Revision}, headers)
	approved := runtimeFixtureRequestWithHeaders[transitionResponse](t, handler, role, http.MethodPost, "/tenant-admin/change-plans/"+planID+"/approve", map[string]any{"expected_revision": inReview.Draft.Revision}, map[string]string{"Authorization": "Bearer " + integrationIdentityAccessTokenFor(approverID, role)})
	runtimeFixtureRequestWithHeaders[map[string]any](t, handler, role, http.MethodPost, "/tenant-admin/change-plans/apply", map[string]any{"plan_id": planID, "expected_revision": approved.Draft.Revision, "confirmation": planID}, map[string]string{"Authorization": "Bearer " + integrationIdentityAccessTokenFor(authorID, role), "Idempotency-Key": "apply:" + planID})
	return loadApplicationDefinitionFixture(t, handler, role, resourceType, resourceKey), approved.Draft
}

func loadApplicationDefinitionFixture(t *testing.T, handler http.Handler, role, resourceType, resourceKey string) appschemamodel.ApplicationDefinition {
	t.Helper()
	response := runtimeFixtureRequest[struct {
		Definition appschemamodel.ApplicationDefinition `json:"definition"`
	}](t, handler, role, http.MethodGet, "/tenant-admin/metadata/definitions/"+resourceType+"/"+resourceKey, nil)
	if response.Definition.ResourceType == "" || response.Definition.ResourceKey == "" || response.Definition.SchemaHash == "" {
		t.Fatalf("metadata definition envelope is incomplete: %#v", response)
	}
	return response.Definition
}
