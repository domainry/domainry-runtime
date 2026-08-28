package runtime

import (
	capabilityprojection "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	"encoding/json"
	"fmt"
	"strings"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

	"testing"

	apperror "github.com/domainry/domainry-foundation/apperror"

	capabilitybusiness "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/audit"
)

func TestBusinessChangePlanCompositionPublishesSchedulerAsTheOnlyRuntimeDefinitionAuthority(t *testing.T) {
	application, admin := newMetadataCompositionApp(t, "scheduler-metadata-authority", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}, nil)
	defer application.CloseContext(t.Context())
	snapshot := changePlanCompositionSnapshot()
	graph := changeplanmodel.ReferenceGraph{Version: changeplanprojection.ChangePlanReferenceGraphVersion, Hash: "graph-hash"}
	workflow := definitionmodel.WorkflowSchema{
		Key: "customer.refresh", Name: "Customer refresh", Enabled: true,
		Trigger: map[string]any{"type": "scheduled"}, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled"},
		Action: map[string]any{"type": "workflow_graph"}, IdempotencyKeys: []string{"scheduled_at"},
		Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "started", Type: "trigger", Name: "Started"}}},
	}
	workflowPayload, _ := json.Marshal(workflow)
	schedulerPayload := json.RawMessage(`{"key":"customer.refresh","name":"Customer refresh","status":"enabled","target_type":"workflow","target_key":"scheduled:customer.refresh","schedule_type":"interval","interval_seconds":60,"max_attempts":3,"timeout_seconds":300}`)
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: "scheduler-metadata-authority", BusinessReason: "Publish scheduler and target as one reviewed candidate",
		SnapshotHash: snapshot.SnapshotHash, ReferenceGraphHash: graph.Hash, RuntimeVersion: snapshot.RuntimeVersion,
		AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		ReleaseOrder: []string{"workflow", "scheduler"}, RollbackOrder: []string{"scheduler", "workflow"},
		Items: []changeplanmodel.BusinessSystemChangeItem{
			{ItemID: "workflow", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "workflow", ResourceKey: workflow.Key, ResourceOwner: "builder", CapabilityKey: "workflow.graph_v2", After: workflowPayload, ValidationMethods: []string{"candidate.validate"}},
			{ItemID: "scheduler", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "scheduler", ResourceKey: "customer.refresh", ResourceOwner: "builder", CapabilityKey: "scheduler.business_job", After: schedulerPayload, ValidationMethods: []string{"candidate.validate"}},
		},
	}
	result, err := application.records.Applications().BusinessChangePlans.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, admin)
	if err != nil || result.Status != "applied" || len(result.AppliedDefinitions) != 2 {
		t.Fatalf("result=%#v err=%v params=%#v", result, err, apperror.ParamsOf(err))
	}
	definition, found, err := application.records.Applications().Metadata.GetMetadataDefinition(t.Context(), "scheduler", "customer.refresh", admin)
	if err != nil || !found || definition.SourceID != plan.PlanID {
		t.Fatalf("metadata definition=%#v found=%v err=%v", definition, found, err)
	}
	published, err := application.records.Applications().Scheduler.GetDefinition(t.Context(), "customer.refresh", admin)
	if err != nil || published.ID != "customer.refresh" || published.Data["target_key"] != "scheduled:customer.refresh" {
		t.Fatalf("scheduler runtime did not read metadata head: published=%#v err=%v", published, err)
	}
	for _, object := range application.records.SchemaForPrincipal(t.Context(), admin).Objects {
		if object.Key == "job_definition" {
			t.Fatal("legacy job_definition business object remains in the runtime schema")
		}
	}
	publishedGraph, err := application.records.Applications().BusinessReferences.Graph(t.Context(), admin)
	if err != nil {
		t.Fatal(err)
	}
	for _, edge := range publishedGraph.Edges {
		if edge.FromType == "scheduler" && edge.FromKey == "customer.refresh" && edge.ToType == "workflow" && edge.ToKey == workflow.Key && edge.Path == "target_key" {
			return
		}
	}
	t.Fatalf("published scheduler target missing from reference graph: %#v", publishedGraph.Edges)
}

func TestBusinessChangePlanCompositionValidatesAndPublishesOneComposedCandidate(t *testing.T) {
	application, admin := newMetadataCompositionApp(t, "composed-candidate", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}, nil)
	defer application.CloseContext(t.Context())
	snapshot := changePlanCompositionSnapshot()
	graph := changeplanmodel.ReferenceGraph{Version: changeplanprojection.ChangePlanReferenceGraphVersion, Hash: "graph-hash"}
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: "composed-candidate", BusinessReason: "Publish a mutually dependent definition graph",
		SnapshotHash: snapshot.SnapshotHash, ReferenceGraphHash: graph.Hash, RuntimeVersion: snapshot.RuntimeVersion,
		AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		ReleaseOrder: []string{"object", "field", "view"}, RollbackOrder: []string{"view", "field", "object"},
		Items: []changeplanmodel.BusinessSystemChangeItem{
			{ItemID: "object", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "object", ResourceKey: "project", ResourceOwner: "builder", CapabilityKey: "schema.object", After: json.RawMessage(`{"key":"project","name":"Project","description":"Project"}`), ValidationMethods: []string{"candidate.validate"}},
			{ItemID: "field", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "field", ResourceKey: "project.name", ResourceOwner: "builder", CapabilityKey: "schema.field", After: json.RawMessage(`{"key":"name","name":"Name","type":"text","required":true}`), ValidationMethods: []string{"candidate.validate"}},
			{ItemID: "view", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "view", ResourceKey: "project_list", ResourceOwner: "builder", CapabilityKey: "view.definition", After: json.RawMessage(`{"key":"project_list","name":"Projects","object_key":"project","type":"table","config":{"columns":["name"]}}`), ValidationMethods: []string{"candidate.validate"}},
		},
	}
	result, err := application.records.Applications().BusinessChangePlans.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, admin)
	if err != nil || result.Status != "applied" || len(result.AppliedDefinitions) != 3 {
		t.Fatalf("result=%#v err=%v params=%#v", result, err, apperror.ParamsOf(err))
	}
	schema := application.records.SchemaForPrincipal(t.Context(), admin)
	for _, object := range schema.Objects {
		if object.Key == "project" {
			if len(object.Fields) != 1 || object.Fields[0].Key != "name" {
				t.Fatalf("project fields=%#v", object.Fields)
			}
			return
		}
	}
	t.Fatalf("project missing from immutable candidate schema: %#v", schema.Objects)
}

func TestBusinessChangePlanCompositionRejectsInvalidCandidateBeforeAnyWrite(t *testing.T) {
	application, admin := newMetadataCompositionApp(t, "invalid-candidate", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}, nil)
	defer application.CloseContext(t.Context())
	before := application.records.SchemaForPrincipal(t.Context(), admin)
	snapshot := changePlanCompositionSnapshot()
	graph := changeplanmodel.ReferenceGraph{Version: changeplanprojection.ChangePlanReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanCompositionPlan(snapshot, graph)
	plan.PlanID = "invalid-candidate"
	plan.Items[0].ResourceKey = "missing.segment"
	plan.Items[0].After = json.RawMessage(`{"key":"segment","name":"Segment","type":"text"}`)
	_, err := application.records.Applications().BusinessChangePlans.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, admin)
	if apperror.CodeOf(err) != "backend.change_plan.candidate_invalid" {
		t.Fatalf("error=%v params=%#v", err, apperror.ParamsOf(err))
	}
	if _, found, getErr := application.records.Applications().Metadata.GetMetadataDefinition(t.Context(), "field", "missing.segment", admin); getErr != nil || found {
		t.Fatalf("invalid candidate leaked definition: found=%v err=%v", found, getErr)
	}
	after := application.records.SchemaForPrincipal(t.Context(), admin)
	if after.SchemaHash != before.SchemaHash {
		t.Fatalf("invalid candidate changed registry: before=%s after=%s", before.SchemaHash, after.SchemaHash)
	}
}

func TestBusinessChangePlanCompositionPublishesIdentityProfileBindingIntoRegistry(t *testing.T) {
	profile := definitionmodel.ObjectSchema{Key: "operator_profile", Name: "Operator profile", UX: map[string]any{"kind": "identity_profile_extension"}, Fields: []definitionmodel.FieldSchema{
		{Key: "identity_user", Name: "Identity user", Type: "relation", Unique: true, Config: map[string]any{"object_key": "identity_user"}},
		{Key: "status", Name: "Status", Type: "text"},
		{Key: "territory_id", Name: "Territory", Type: "text"},
	}}
	application, admin := newMetadataCompositionApp(t, "identity-profile-binding", []definitionmodel.ObjectSchema{profile}, nil)
	defer application.CloseContext(t.Context())
	binding := profilebindingmodel.Binding{
		ContractVersion: profilebindingmodel.ContractVersion, MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
		ObjectKey: "operator_profile", IdentityRelationField: "identity_user", Cardinality: "one_to_one", DefaultVisibility: "when_readable",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "operator", SurfaceKeys: []string{"admin"}, StatusField: "status", ActiveStatusValues: []string{"active"}, Claims: []profilebindingmodel.ClaimBinding{{ClaimKey: "territory_id", FieldKey: "territory_id"}}},
	}
	payload, _ := json.Marshal(binding)
	snapshot := changePlanCompositionSnapshot()
	graph := changeplanmodel.ReferenceGraph{Version: changeplanprojection.ChangePlanReferenceGraphVersion, Hash: "graph-hash"}
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: "identity-profile-binding", BusinessReason: "Publish a typed business identity binding",
		SnapshotHash: snapshot.SnapshotHash, ReferenceGraphHash: graph.Hash, RuntimeVersion: snapshot.RuntimeVersion,
		AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		Reviewed: true, ReviewedBy: "security-reviewer", ReleaseOrder: []string{"binding"}, RollbackOrder: []string{"binding"},
		Items: []changeplanmodel.BusinessSystemChangeItem{{ItemID: "binding", Operation: "create", ChangeKind: "additive", RiskLevel: "high", ResourceType: "identity_profile_binding", ResourceKey: binding.ObjectKey, ResourceOwner: "builder", OwnerAuthorized: true, CapabilityKey: "principal.profile_binding", After: payload, ValidationMethods: []string{"identity_profile_binding.validate"}}},
	}
	result, err := application.records.Applications().BusinessChangePlans.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, admin)
	if err != nil || result.Status != "applied" || len(result.AppliedDefinitions) != 1 {
		t.Fatalf("result=%#v err=%v params=%#v", result, err, apperror.ParamsOf(err))
	}
	definition, found, err := application.records.Applications().Metadata.GetMetadataDefinition(t.Context(), "identity_profile_binding", binding.ObjectKey, admin)
	if err != nil || !found || definition.SourceID != plan.PlanID {
		t.Fatalf("definition=%#v found=%v err=%v", definition, found, err)
	}
	schema := application.records.SchemaForPrincipal(t.Context(), admin)
	for _, published := range schema.IdentityProfileExtensions {
		if published.ObjectKey == binding.ObjectKey && published.BusinessIdentity.Key == binding.BusinessIdentity.Key {
			return
		}
	}
	t.Fatalf("published identity profile binding missing from immutable schema: %#v", schema.IdentityProfileExtensions)
}

func TestBusinessChangePlanCompositionAppliesMetadataAndFreezesDraft(t *testing.T) {
	application, admin := newMetadataCompositionApp(t, "change-plan", []definitionmodel.ObjectSchema{{Key: "customer", Name: "Customer"}}, nil)
	defer application.CloseContext(t.Context())
	snapshot := changePlanCompositionSnapshot()
	graph := changeplanmodel.ReferenceGraph{Version: changeplanprojection.ChangePlanReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanCompositionPlan(snapshot, graph)
	plan.BuilderTaskID, plan.BusinessReason, admin.RequestID = "task-1", "Add customer segment", "request-change-plan"
	plan.Items[0].After = json.RawMessage(`{"key":"segment","name":"Segment","type":"text"}`)

	draft, err := application.records.Applications().BusinessChangePlans.SaveDraft(t.Context(), plan, 0, admin)
	if err != nil || draft.Revision != 1 || draft.Status != "draft" {
		t.Fatalf("draft=%#v err=%v", draft, err)
	}
	if _, err := application.records.Applications().BusinessChangePlans.SaveDraft(t.Context(), plan, 0, admin); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
		t.Fatalf("conflict=%v", err)
	}
	if err := json.Unmarshal(draft.Payload, &plan); err != nil {
		t.Fatal(err)
	}
	inReview, validation, err := application.records.Applications().BusinessChangePlans.SubmitDraftForReview(t.Context(), plan.PlanID, draft.Revision, snapshot, graph, admin)
	if err != nil || !validation.Valid || inReview.Status != "in_review" || inReview.Revision != 2 {
		t.Fatalf("inReview=%#v validation=%#v err=%v", inReview, validation, err)
	}
	approver := admin
	approver.UserID, approver.RequestID = "change-plan-approver", "request-change-plan-approval"
	approved, validation, err := application.records.Applications().BusinessChangePlans.ApproveDraft(t.Context(), plan.PlanID, inReview.Revision, snapshot, graph, approver)
	if err != nil || !validation.ApplyAllowed || approved.Status != "approved" || approved.Revision != 3 {
		t.Fatalf("approved=%#v validation=%#v err=%v", approved, validation, err)
	}
	result, replayed, err := application.records.Applications().BusinessChangePlans.PublishApprovedDraftIdempotent(t.Context(), plan.PlanID, approved.Revision, plan.PlanID, "apply-plan-1", snapshot, graph, admin)
	if err != nil || replayed || result.Status != "applied" || len(result.AppliedDefinitions) != 1 {
		t.Fatalf("result=%#v replayed=%v err=%v params=%#v", result, replayed, err, apperror.ParamsOf(err))
	}
	replay, replayed, err := application.records.Applications().BusinessChangePlans.PublishApprovedDraftIdempotent(t.Context(), plan.PlanID, approved.Revision, plan.PlanID, "apply-plan-1", snapshot, graph, admin)
	if err != nil || !replayed || replay.SchemaHash != result.SchemaHash || len(replay.AppliedDefinitions) != 1 {
		t.Fatalf("replay=%#v replayed=%v err=%v", replay, replayed, err)
	}
	changedPlan := plan
	changedPlan.DraftRevision, changedPlan.Reviewed, changedPlan.ReviewedBy = approved.Revision, true, approver.UserID
	changedPlan.BusinessReason = "Different publication"
	if _, _, err := application.records.Applications().BusinessChangePlans.ApplyIdempotent(t.Context(), changedPlan, changedPlan.PlanID, "apply-plan-1", snapshot, graph, admin); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("fingerprint conflict=%v", err)
	}
	frozen, err := application.records.Applications().BusinessChangePlans.Draft(t.Context(), plan.PlanID, admin)
	if err != nil || frozen.Status != "published" || frozen.Revision != 4 {
		t.Fatalf("frozen=%#v err=%v", frozen, err)
	}
	definition, found, err := application.records.Applications().Metadata.GetMetadataDefinition(t.Context(), "field", "customer.segment", admin)
	if err != nil || !found || definition.SourceKind != "builder" || definition.SourceID != plan.PlanID {
		t.Fatalf("definition=%#v found=%v err=%v", definition, found, err)
	}
	audits, err := auditpersistence.NewAuditStore(application.store).ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{Event: "business_change_plan.published", Limit: 10})
	if err != nil || len(audits) != 1 || audits[0].Metadata["request_id"] != admin.RequestID {
		t.Fatalf("audits=%#v err=%v", audits, err)
	}
	for _, status := range []string{"succeeded", "replayed", "fingerprint_conflict"} {
		idempotencyAudits, listErr := auditpersistence.NewAuditStore(application.store).ListAuditEvents(t.Context(), "default", auditmodel.AuditEventQuery{Event: "business_change_plan.idempotency_" + status, Limit: 10})
		if listErr != nil || len(idempotencyAudits) != 1 {
			t.Fatalf("idempotency status=%s audits=%#v err=%v", status, idempotencyAudits, listErr)
		}
		metadata := idempotencyAudits[0].Metadata
		encoded := fmt.Sprint(metadata)
		if metadata["workspace_id"] != admin.WorkspaceID || metadata["idempotency_scope"] != "change_plan.apply" || metadata["idempotency_status"] != status || metadata["fencing_token"] == nil || metadata["idempotency_key_hash"] == nil || metadata["request_fingerprint_hash"] == nil {
			t.Fatalf("idempotency status=%s metadata=%#v", status, metadata)
		}
		if strings.Contains(encoded, "apply-plan-1") || strings.Contains(encoded, plan.BusinessReason) || strings.Contains(encoded, string(plan.Items[0].After)) {
			t.Fatalf("idempotency audit leaked request data: %s", encoded)
		}
	}
}

func TestIndustryMaintenancePlanCompositionPublishesThroughSystemDraft(t *testing.T) {
	for _, tc := range []struct{ name, objectKey, fieldKey, fieldType string }{
		{"HR", "leave_request", "leave_type_extension", "text"},
		{"CRM", "opportunity", "next_step", "text"},
		{"ERP", "purchase_request", "approval_threshold", "currency"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			application, admin := newMetadataCompositionApp(t, "maintenance-"+tc.objectKey, []definitionmodel.ObjectSchema{{Key: tc.objectKey, Name: tc.name}}, nil)
			defer application.CloseContext(t.Context())
			snapshot := changePlanCompositionSnapshot()
			graph := changeplanmodel.ReferenceGraph{Version: changeplanprojection.ChangePlanReferenceGraphVersion, Hash: "graph-hash"}
			plan := changePlanCompositionPlan(snapshot, graph)
			plan.PlanID, plan.BuilderTaskID = "maintain-"+tc.objectKey, "builder-"+tc.name
			plan.Items[0].ResourceKey = tc.objectKey + "." + tc.fieldKey
			plan.Items[0].After, _ = json.Marshal(definitionmodel.FieldSchema{Key: tc.fieldKey, Name: tc.fieldKey, Type: tc.fieldType})
			if _, err := application.records.Applications().BusinessChangePlans.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, admin); err != nil {
				t.Fatal(err)
			}
			metadata := application.records.Applications().Metadata
			definition, found, err := metadata.GetMetadataDefinition(t.Context(), "field", plan.Items[0].ResourceKey, admin)
			if err != nil || !found {
				t.Fatalf("definition=%#v found=%v err=%v", definition, found, err)
			}
			versions, err := metadata.ListMetadataDefinitionVersions(t.Context(), "field", plan.Items[0].ResourceKey, admin)
			if err != nil || len(versions) != 1 {
				t.Fatalf("versions=%#v err=%v", versions, err)
			}
		})
	}
}

func changePlanCompositionSnapshot() changeplanprojection.BusinessSystemSnapshot {
	contract := capabilityprojection.RuntimeAuthoringCapabilities()
	capabilityKeys := []string{}
	for _, domain := range contract.Domains {
		for _, capability := range domain.Capabilities {
			capabilityKeys = append(capabilityKeys, capability.Key)
		}
	}
	return changeplanprojection.BusinessSystemSnapshot{SnapshotVersion: changeplanprojection.BusinessSystemSnapshotVersion, SnapshotHash: "snapshot-hash", RuntimeVersion: capabilitybusiness.RuntimeCapabilityContractVersion, AuthoringContractVersion: contract.ContractVersion, AuthoringContractHash: contract.ContractHash, CapabilityKeys: capabilityKeys}
}

func changePlanCompositionPlan(snapshot changeplanprojection.BusinessSystemSnapshot, graph changeplanmodel.ReferenceGraph) changeplanmodel.BusinessSystemChangePlan {
	return changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: "plan-1", BusinessReason: "Add customer segment", SnapshotHash: snapshot.SnapshotHash, ReferenceGraphHash: graph.Hash,
		RuntimeVersion: snapshot.RuntimeVersion, AuthoringContractVersion: snapshot.AuthoringContractVersion, AuthoringContractHash: snapshot.AuthoringContractHash,
		ReleaseOrder: []string{"add-segment"}, RollbackOrder: []string{"add-segment"}, Items: []changeplanmodel.BusinessSystemChangeItem{{ItemID: "add-segment", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "field", ResourceKey: "customer.segment", ResourceOwner: "builder", CapabilityKey: "schema.field", After: json.RawMessage(`{"key":"segment","type":"text"}`), ValidationMethods: []string{"metadata.validate"}}},
	}
}
