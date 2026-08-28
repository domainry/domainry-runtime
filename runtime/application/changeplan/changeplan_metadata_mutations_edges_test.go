package changeplan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type changePlanRuntimeFake struct {
	validateErr error
	reloadHash  string
	reloadErr   error
	validated   []string
	candidate   []metadatamodel.MetadataDefinitionMutation
}

func (f *changePlanRuntimeFake) ValidateMetadataDefinitionPayload(_ context.Context, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
	f.validated = append(f.validated, resourceType+":"+resourceKey)
	request.Name = "normalized-" + request.Name
	return request, f.validateErr
}

func (f *changePlanRuntimeFake) CanonicalizeMetadataCandidate(_ context.Context, mutations []metadatamodel.MetadataDefinitionMutation) ([]metadatamodel.MetadataDefinitionMutation, error) {
	f.validated = append(f.validated, "candidate")
	f.candidate = append([]metadatamodel.MetadataDefinitionMutation(nil), mutations...)
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	return mutations, nil
}

func (f *changePlanRuntimeFake) ReloadMetadata(context.Context, principalmodel.Principal) (string, error) {
	return f.reloadHash, f.reloadErr
}

func TestChangePlanMetadataMutationProjectionAndErrors(t *testing.T) {
	plan := BusinessSystemChangePlan{PlanID: "plan-1", ReleaseOrder: []string{"noop", "create", "update", "delete"}, Items: []BusinessSystemChangeItem{
		{ItemID: "noop", Operation: "noop", ResourceType: "field", ResourceKey: "order.skip"},
		{ItemID: "create", Operation: "create", ResourceType: "field", ResourceKey: "order.status", After: json.RawMessage(`{"name":"Status"}`)},
		{ItemID: "update", Operation: "update", ResourceType: "object", ResourceKey: "order", ExpectedResourceHash: "old-hash", After: json.RawMessage(`{"object_key":"order","name":"Order"}`)},
		{ItemID: "delete", Operation: "delete", ResourceType: "view", ResourceKey: "order_list", ExpectedResourceHash: "view-hash", Before: json.RawMessage(`{"name":"Old"}`)},
	}}
	runtime := &changePlanRuntimeFake{}
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, &changePlanAuditFake{}, runtime)
	mutations, audits, err := service.metadataChangePlanMutations(t.Context(), plan, changePlanAdmin())
	if err != nil || len(mutations) != 3 || len(audits) != 3 || len(runtime.validated) != 1 || len(runtime.candidate) != 3 {
		t.Fatalf("mutations=%+v audits=%d validated=%+v err=%v", mutations, len(audits), runtime.validated, err)
	}
	if mutations[0].Request.ObjectKey != "order" || *mutations[0].Request.ExpectedSchemaHash != "" || mutations[1].Request.Name != "Order" || *mutations[1].Request.ExpectedSchemaHash != "old-hash" {
		t.Fatalf("mutation requests = %+v", mutations)
	}

	wantErr := errors.New("validation failed")
	runtime.validateErr = wantErr
	if _, _, err := service.metadataChangePlanMutations(t.Context(), plan, changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("validation error = %v", err)
	}
	unsupported := plan
	unsupported.ReleaseOrder = []string{"unsupported"}
	unsupported.Items = []BusinessSystemChangeItem{{ItemID: "unsupported", Operation: "create", ResourceType: "unknown_resource", ResourceKey: "approval"}}
	if _, _, err := service.metadataChangePlanMutations(t.Context(), unsupported, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.apply_resource_unsupported" {
		t.Fatalf("unsupported error = %v", err)
	}
}

func TestChangePlanMetadataMutationHelpers(t *testing.T) {
	for _, resourceType := range []string{"object", "field", "validation", "view", "action", "workflow", "scheduler", "automation_rule", "preference", "rule_set", "dictionary", "connector", "integration_event_mapping", "report", "operation_state_example", "sensitive_field_policy", "report_export_control", "role", "identity_profile_binding", "entrypoint", "skill", "agent"} {
		if !businessChangePlanMetadataResourceType(resourceType) {
			t.Fatalf("metadata type %q rejected", resourceType)
		}
	}
	for _, resourceType := range []string{"unknown_resource", "surface", "component"} {
		if businessChangePlanMetadataResourceType(resourceType) {
			t.Fatalf("retired or unsupported metadata type %q accepted", resourceType)
		}
	}
	plan := BusinessSystemChangePlan{PlanID: "plan"}
	create := metadataChangePlanRequest(plan, BusinessSystemChangeItem{Operation: "create", ResourceType: "field", ResourceKey: "order.status", ExpectedResourceHash: "ignored", After: json.RawMessage(`{"name":12}`)})
	if create.ObjectKey != "order" || create.Name != "" || *create.ExpectedSchemaHash != "" {
		t.Fatalf("create request = %+v", create)
	}
	if got := businessChangeJSONMap(json.RawMessage("bad")); len(got) != 0 || stringValue(12) != "" || stringValue("value") != "value" {
		t.Fatalf("JSON/string helpers = %+v", got)
	}
	audit := buildBusinessChangePlanAudit(plan, BusinessSystemChangeItem{Operation: "update", ResourceType: "object", ResourceKey: "order", Before: json.RawMessage(`{"name":"Old"}`), After: json.RawMessage(`{"name":"New"}`)}, changePlanAdmin())
	if audit.Event != "business_change_plan.item_applied" || audit.ObjectKey != "object" || audit.RecordID != "order" || audit.Before["name"] != "Old" || audit.After["name"] != "New" {
		t.Fatalf("audit = %+v", audit)
	}
	if businessReferenceResourceType("role_data_permission") == "" {
		t.Fatal("canonical resource type empty")
	}
}
