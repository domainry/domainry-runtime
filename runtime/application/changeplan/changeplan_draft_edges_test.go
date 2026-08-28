package changeplan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type changePlanDraftRepositoryFake struct {
	draft         changeplanmodel.BusinessChangePlanDraft
	found         bool
	getErr        error
	saveErr       error
	saveOK        bool
	publishErr    error
	publishOK     bool
	published     changeplanmodel.BusinessChangePlanDraft
	transitionErr error
}

func (f *changePlanDraftRepositoryFake) GetDraft(context.Context, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	return f.draft, f.found, f.getErr
}

func (f *changePlanDraftRepositoryFake) SaveDraft(_ context.Context, _ string, draft changeplanmodel.BusinessChangePlanDraft, _ int) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	if f.saveErr != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, f.saveErr
	}
	if !f.saveOK {
		return changeplanmodel.BusinessChangePlanDraft{}, false, nil
	}
	f.draft, f.found = draft, true
	return draft, true, nil
}

func (f *changePlanDraftRepositoryFake) TransitionDraft(_ context.Context, workspaceID, _ string, expected int, fromStatus, toStatus, by, at string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	if f.transitionErr != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, f.transitionErr
	}
	if !f.found || f.draft.Revision != expected || f.draft.Status != fromStatus {
		return changeplanmodel.BusinessChangePlanDraft{}, false, nil
	}
	f.draft.WorkspaceID = workspaceID
	f.draft.Revision++
	f.draft.Status = toStatus
	f.draft.UpdatedBy = by
	f.draft.UpdatedAt = at
	return f.draft, true, nil
}

func (f *changePlanDraftRepositoryFake) PublishDraft(context.Context, string, string, int, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	if f.published.PlanID != "" {
		return f.published, f.publishOK, f.publishErr
	}
	return f.draft, f.publishOK, f.publishErr
}

type changePlanAuditFake struct {
	events []auditmodel.AuditEvent
	err    error
}

type changePlanCloneMetadataFake struct {
	definitions map[string][]metadatamodel.MetadataDefinition
	listed      []string
	err         error
}

func (f *changePlanCloneMetadataFake) ListDefinitions(_ context.Context, _ principalmodel.SystemScope, resourceType string) ([]metadatamodel.MetadataDefinition, error) {
	f.listed = append(f.listed, resourceType)
	if f.err != nil {
		return nil, f.err
	}
	return append([]metadatamodel.MetadataDefinition(nil), f.definitions[resourceType]...), nil
}

func (*changePlanCloneMetadataFake) ApplyDefinitionMutations(context.Context, principalmodel.SystemScope, []metadatamodel.MetadataDefinitionMutation, []auditmodel.AuditEvent, *changeplanmodel.BusinessChangePlanPublication) ([]metadatamodel.MetadataDefinition, error) {
	return nil, nil
}

func (f *changePlanAuditFake) InsertAuditEvent(_ context.Context, _ string, event auditmodel.AuditEvent) error {
	f.events = append(f.events, event)
	return f.err
}

func TestChangePlanDraftQueryBoundaries(t *testing.T) {
	wantErr := errors.New("repository failed")
	repository := &changePlanDraftRepositoryFake{}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)
	if _, err := service.Draft(t.Context(), "plan", changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_not_found" {
		t.Fatalf("not found error = %v", err)
	}
	repository.getErr = wantErr
	if _, err := service.Draft(t.Context(), "plan", changePlanAdmin()); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("get error = %v", err)
	}
	repository.getErr = nil
	repository.found = true
	repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: "plan", Revision: 2}
	if draft, err := service.Draft(t.Context(), " plan ", changePlanAdmin()); err != nil || draft.Revision != 2 {
		t.Fatalf("draft = %+v, %v", draft, err)
	}
	denied := changePlanAdmin()
	denied = changePlanWithoutPermissions(denied)
	if _, err := service.Draft(t.Context(), "plan", denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error = %v", err)
	}
}

func TestChangePlanSaveDraftConflictAndFailureMatrix(t *testing.T) {
	wantErr := errors.New("repository failed")
	plan := changeplanmodel.BusinessSystemChangePlan{PlanID: " plan ", BusinessReason: "test"}

	t.Run("permission and required id", func(t *testing.T) {
		service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, &changePlanAuditFake{}, nil)
		denied := changePlanAdmin()
		denied = changePlanWithoutPermissions(denied)
		if _, err := service.SaveDraft(t.Context(), plan, 0, denied); apperror.CodeOf(err) != "auth.permission_denied" {
			t.Fatalf("permission error = %v", err)
		}
		if _, err := service.SaveDraft(t.Context(), changeplanmodel.BusinessSystemChangePlan{}, 0, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.plan_id_required" {
			t.Fatalf("plan id error = %v", err)
		}
	})

	t.Run("get error and version conflicts", func(t *testing.T) {
		repository := &changePlanDraftRepositoryFake{getErr: wantErr}
		service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)
		if _, err := service.SaveDraft(t.Context(), plan, 0, changePlanAdmin()); !errors.Is(err, wantErr) {
			t.Fatalf("get error = %v", err)
		}
		repository.getErr, repository.found = nil, true
		repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: "plan", Revision: 1, Status: "draft"}
		if _, err := service.SaveDraft(t.Context(), plan, 0, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
			t.Fatalf("create conflict = %v", err)
		}
		repository.found = false
		if _, err := service.SaveDraft(t.Context(), plan, 1, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
			t.Fatalf("missing update conflict = %v", err)
		}
		repository.found = true
		repository.draft.Revision = 2
		if _, err := service.SaveDraft(t.Context(), plan, 1, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
			t.Fatalf("revision conflict = %v", err)
		}
		repository.draft.Revision, repository.draft.Status = 1, "published"
		if _, err := service.SaveDraft(t.Context(), plan, 1, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
			t.Fatalf("status conflict = %v", err)
		}
	})

	t.Run("marshal save and audit failures", func(t *testing.T) {
		repository := &changePlanDraftRepositoryFake{saveOK: true}
		audit := &changePlanAuditFake{}
		service := NewChangePlanApplicationService(repository, nil, audit, nil)
		invalid := plan
		invalid.Items = []changeplanmodel.BusinessSystemChangeItem{{After: json.RawMessage("{")}}
		if _, err := service.SaveDraft(t.Context(), invalid, 0, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_invalid" {
			t.Fatalf("marshal error = %v", err)
		}
		repository.saveErr = wantErr
		if _, err := service.SaveDraft(t.Context(), plan, 0, changePlanAdmin()); !errors.Is(err, wantErr) {
			t.Fatalf("save error = %v", err)
		}
		repository.saveErr, repository.saveOK = nil, false
		if _, err := service.SaveDraft(t.Context(), plan, 0, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
			t.Fatalf("compare miss = %v", err)
		}
		repository.saveOK = true
		audit.err = wantErr
		if _, err := service.SaveDraft(t.Context(), plan, 0, changePlanAdmin()); !errors.Is(err, wantErr) {
			t.Fatalf("audit error = %v", err)
		}
	})

	t.Run("update preserves creator", func(t *testing.T) {
		repository := &changePlanDraftRepositoryFake{found: true, saveOK: true, draft: changeplanmodel.BusinessChangePlanDraft{PlanID: "plan", Revision: 1, Status: "draft", CreatedAt: "created", CreatedBy: "creator"}}
		service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)
		draft, err := service.SaveDraft(t.Context(), plan, 1, changePlanAdmin())
		if err != nil || draft.Revision != 2 || draft.CreatedAt != "created" || draft.CreatedBy != "creator" {
			t.Fatalf("updated draft = %+v, %v", draft, err)
		}
	})
}

func TestChangePlanReviewApprovalIsServerOwnedAndMakerCheckerProtected(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	plan.Reviewed, plan.ReviewedBy = true, "self-reported"
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	repository := &changePlanDraftRepositoryFake{found: true, draft: changeplanmodel.BusinessChangePlanDraft{WorkspaceID: "workspace-1", PlanID: plan.PlanID, Revision: 1, Status: "draft", Payload: payload, CreatedBy: "author", UpdatedBy: "author"}}
	runtime := &changePlanRuntimeFake{}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, runtime)
	author := changePlanAdmin()
	author.UserID = "author"
	inReview, validation, err := service.SubmitDraftForReview(t.Context(), plan.PlanID, 1, snapshot, graph, author)
	if err != nil || !validation.Valid || inReview.Status != "in_review" || inReview.Revision != 2 || len(runtime.candidate) != 1 {
		t.Fatalf("review draft=%#v validation=%#v candidate=%#v err=%v", inReview, validation, runtime.candidate, err)
	}
	if _, _, err := service.ApproveDraft(t.Context(), plan.PlanID, 2, snapshot, graph, author); apperror.CodeOf(err) != "backend.change_plan.maker_checker_required" {
		t.Fatalf("self approval error=%v", err)
	}
	approver := changePlanAdmin()
	approver.UserID = "approver"
	approved, validation, err := service.ApproveDraft(t.Context(), plan.PlanID, 2, snapshot, graph, approver)
	if err != nil || !validation.ApplyAllowed || approved.Status != "approved" || approved.Revision != 3 || approved.UpdatedBy != "approver" {
		t.Fatalf("approved=%#v validation=%#v err=%v", approved, validation, err)
	}
	if _, _, err := service.PublishApprovedDraftIdempotent(t.Context(), plan.PlanID, 2, plan.PlanID, "publish", snapshot, graph, approver); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
		t.Fatalf("stale approved revision error=%v", err)
	}
}

func TestChangePlanSaveDraftDropsClientSuppliedReviewEvidence(t *testing.T) {
	repository := &changePlanDraftRepositoryFake{saveOK: true}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)
	plan := changeplanmodel.BusinessSystemChangePlan{PlanID: "plan", Reviewed: true, ReviewedBy: "forged"}
	draft, err := service.SaveDraft(t.Context(), plan, 0, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	var persisted changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.Reviewed || persisted.ReviewedBy != "" {
		t.Fatalf("client review evidence persisted: %#v", persisted)
	}
}

func TestChangePlanExportPackageIsDeterministicAndTargetsEmptyWorkspace(t *testing.T) {
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanID: "package-plan", RuntimeVersion: "runtime-v1", AuthoringContractVersion: "contract-v1", AuthoringContractHash: "contract-hash",
		AcceptanceScenarios: []changeplanmodel.BusinessAcceptanceScenario{{Key: "create-and-read", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.create", Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true}}},
		Items: []changeplanmodel.BusinessSystemChangeItem{
			{Operation: "update", ResourceType: "workflow", ResourceKey: "order.review", After: json.RawMessage(`{ "name": "Review", "key": "order.review" }`), Dependencies: []changeplanmodel.BusinessChangeTarget{{ResourceType: "object", ResourceKey: "order", Reason: "workflow object"}}},
			{Operation: "delete", ResourceType: "view", ResourceKey: "retired", Before: json.RawMessage(`{"key":"retired"}`)},
			{Operation: "create", ResourceType: "object", ResourceKey: "order", After: json.RawMessage(`{"name":"Order","key":"order"}`)},
		},
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	repository := &changePlanDraftRepositoryFake{found: true, draft: changeplanmodel.BusinessChangePlanDraft{WorkspaceID: "workspace-1", PlanID: plan.PlanID, Revision: 3, Status: "approved", Payload: payload}}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)
	first, err := service.ExportPackage(t.Context(), plan.PlanID, 3, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ExportPackage(t.Context(), plan.PlanID, 3, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if first.PackageVersion != changeplanmodel.BusinessSystemPackageVersion || first.PackageHash == "" || first.PackageHash != second.PackageHash || len(first.Resources) != 2 || len(first.Dependencies) != 1 || len(first.AcceptanceScenarios) != 1 {
		t.Fatalf("package=%#v second_hash=%s", first, second.PackageHash)
	}
	if first.EmptyWorkspaceApplyPlan.Target != "empty_workspace" || !first.EmptyWorkspaceApplyPlan.RequiresSnapshotBinding || !first.EmptyWorkspaceApplyPlan.RequiresReferenceBinding || len(first.EmptyWorkspaceApplyPlan.Operations) != 2 {
		t.Fatalf("apply plan=%#v", first.EmptyWorkspaceApplyPlan)
	}
	for _, operation := range first.EmptyWorkspaceApplyPlan.Operations {
		if operation.Operation != "create" {
			t.Fatalf("empty workspace operation=%#v", operation)
		}
	}
	if _, err := service.ExportPackage(t.Context(), plan.PlanID, 2, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
		t.Fatalf("stale export error=%v", err)
	}
}

func TestChangePlanExportPackageCoversDraftPayloadAndDependencyEdges(t *testing.T) {
	repository := &changePlanDraftRepositoryFake{getErr: errors.New("read")}
	service := NewChangePlanApplicationService(repository, nil, &changePlanAuditFake{}, nil)
	if _, err := service.ExportPackage(t.Context(), "plan", 1, changePlanAdmin()); err == nil {
		t.Fatal("draft read error ignored")
	}

	plan := changeplanmodel.BusinessSystemChangePlan{PlanID: "plan", Items: []changeplanmodel.BusinessSystemChangeItem{
		{Operation: "create", ResourceType: "workflow", ResourceKey: "order.flow", After: json.RawMessage(`{"key":"order.flow"}`), Dependencies: []changeplanmodel.BusinessChangeTarget{{ResourceType: "object", ResourceKey: "order"}, {ResourceType: "action", ResourceKey: "order.submit"}}},
	}}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	repository.getErr, repository.found = nil, true
	repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 2, Status: "approved", Payload: payload}
	if _, err := service.ExportPackage(t.Context(), plan.PlanID, 0, changePlanAdmin()); err == nil {
		t.Fatal("zero revision accepted")
	}
	repository.draft.Status = "draft"
	if _, err := service.ExportPackage(t.Context(), plan.PlanID, 2, changePlanAdmin()); err == nil {
		t.Fatal("draft status accepted")
	}
	repository.draft.Status, repository.draft.Payload = "published", json.RawMessage(`{`)
	if _, err := service.ExportPackage(t.Context(), plan.PlanID, 2, changePlanAdmin()); err == nil {
		t.Fatal("malformed draft payload accepted")
	}
	repository.draft.Payload = payload
	result, err := service.ExportPackage(t.Context(), plan.PlanID, 2, changePlanAdmin())
	if err != nil || len(result.Dependencies) != 2 {
		t.Fatalf("published export=%#v err=%v", result, err)
	}
}

func TestChangePlanPackageImportsAsCurrentContractDraftForEmptyWorkspace(t *testing.T) {
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanID: "source-plan", RuntimeVersion: "runtime-v1", AuthoringContractVersion: "contract-v1", AuthoringContractHash: "contract-hash",
		AcceptanceScenarios: []changeplanmodel.BusinessAcceptanceScenario{{Key: "submit", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.submit", Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true}}},
		Items: []changeplanmodel.BusinessSystemChangeItem{
			{Operation: "create", ResourceType: "action", ResourceKey: "order.submit", After: json.RawMessage(`{"key":"order.submit","object_key":"order"}`), Dependencies: []changeplanmodel.BusinessChangeTarget{{ResourceType: "object", ResourceKey: "order", Reason: "target object"}}},
			{Operation: "create", ResourceType: "object", ResourceKey: "order", After: json.RawMessage(`{"key":"order","name":"Order"}`)},
		},
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	sourceRepository := &changePlanDraftRepositoryFake{found: true, draft: changeplanmodel.BusinessChangePlanDraft{WorkspaceID: "source", PlanID: plan.PlanID, Revision: 2, Status: "published", Payload: payload}}
	systemPackage, err := NewChangePlanApplicationService(sourceRepository, nil, &changePlanAuditFake{}, nil).ExportPackage(t.Context(), plan.PlanID, 2, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	targetRepository := &changePlanDraftRepositoryFake{saveOK: true}
	targetService := NewChangePlanApplicationService(targetRepository, nil, &changePlanAuditFake{}, nil)
	snapshot := changeplanmodel.Snapshot{SnapshotHash: "empty-snapshot", RuntimeVersion: "runtime-v1", AuthoringContractVersion: "contract-v1", AuthoringContractHash: "contract-hash"}
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "empty-graph"}
	draft, err := targetService.ImportPackageDraft(t.Context(), "replay-plan", "Replay package", 0, systemPackage, snapshot, graph, changePlanAdmin())
	if err != nil {
		t.Fatal(err)
	}
	if draft.Status != "draft" || draft.Revision != 1 {
		t.Fatalf("draft=%#v", draft)
	}
	var imported changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &imported); err != nil {
		t.Fatal(err)
	}
	if imported.PlanID != "replay-plan" || imported.SnapshotHash != "empty-snapshot" || imported.ReferenceGraphHash != "empty-graph" || imported.BuilderTaskID != "package:"+systemPackage.PackageHash || len(imported.Items) != 2 || len(imported.AcceptanceScenarios) != 1 {
		t.Fatalf("imported=%#v", imported)
	}
	if got := imported.ReleaseOrder; len(got) != 2 || got[0] != "object:order" || got[1] != "action:order.submit" {
		t.Fatalf("dependency release order=%v", got)
	}
	if imported.RollbackOrder[0] != "action:order.submit" || imported.Items[0].Operation != "create" || imported.Items[0].ExpectedResourceHash != "" {
		t.Fatalf("rollback=%v item=%#v", imported.RollbackOrder, imported.Items[0])
	}
}

func TestChangePlanPackageImportRejectsTamperingContractDriftAndNonEmptyTarget(t *testing.T) {
	resourcePayload := json.RawMessage(`{"key":"order"}`)
	canonical, resourceHash, err := canonicalPackagePayload(resourcePayload)
	if err != nil {
		t.Fatal(err)
	}
	systemPackage := changeplanmodel.BusinessSystemPackage{
		PackageVersion: changeplanmodel.BusinessSystemPackageVersion, RuntimeVersion: "runtime-v1", AuthoringContractVersion: "contract-v1", AuthoringContractHash: "contract-hash",
		Resources:               []changeplanmodel.BusinessSystemPackageResource{{ResourceType: "object", ResourceKey: "order", ResourceHash: resourceHash, Payload: canonical}},
		EmptyWorkspaceApplyPlan: changeplanmodel.BusinessSystemPackageApplyPlan{Target: "empty_workspace", RequiresSnapshotBinding: true, RequiresReferenceBinding: true, Operations: []changeplanmodel.BusinessSystemPackageApplyOperation{{Operation: "create", ResourceType: "object", ResourceKey: "order", Payload: canonical}}},
	}
	systemPackage.PackageHash = hashBusinessSystemPackage(systemPackage)
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{saveOK: true}, nil, &changePlanAuditFake{}, nil)
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph"}
	base := changeplanmodel.Snapshot{SnapshotHash: "empty", RuntimeVersion: "runtime-v1", AuthoringContractVersion: "contract-v1", AuthoringContractHash: "contract-hash"}

	tampered := systemPackage
	tampered.Resources = append([]changeplanmodel.BusinessSystemPackageResource(nil), systemPackage.Resources...)
	tampered.Resources[0].Payload = json.RawMessage(`{"key":"other"}`)
	if _, err := service.ImportPackageDraft(t.Context(), "tampered", "", 0, tampered, base, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.package_hash_mismatch" {
		t.Fatalf("tamper error=%v", err)
	}
	drifted := base
	drifted.AuthoringContractHash = "new-contract"
	if _, err := service.ImportPackageDraft(t.Context(), "drifted", "", 0, systemPackage, drifted, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.package_contract_mismatch" {
		t.Fatalf("contract error=%v", err)
	}
	nonEmpty := base
	nonEmpty.ResourceSources = []changeplanmodel.ResourceSource{{ResourceType: "object", ResourceKey: "order"}}
	if _, err := service.ImportPackageDraft(t.Context(), "non-empty", "", 0, systemPackage, nonEmpty, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.package_target_not_empty" {
		t.Fatalf("target error=%v", err)
	}
}

func TestChangePlanCloneCurrentCreatesPinnedEditableSnapshotDraft(t *testing.T) {
	repository := &changePlanDraftRepositoryFake{saveOK: true}
	metadata := &changePlanCloneMetadataFake{definitions: map[string][]metadatamodel.MetadataDefinition{
		"object": {
			{ResourceType: "object", ResourceKey: "order", SchemaHash: "object-hash", SourceKind: "generated", Payload: json.RawMessage(`{"key":"order"}`)},
			{ResourceType: "object", ResourceKey: "retired", SchemaHash: "retired-hash", SourceKind: "builder", DisabledAt: "2026-01-01", Payload: json.RawMessage(`{"key":"retired"}`)},
		},
		"action": {{ResourceType: "action", ResourceKey: "order.submit", SchemaHash: "action-hash", SourceKind: "builder", Payload: json.RawMessage(`{"key":"order.submit"}`)}},
	}}
	service := NewChangePlanApplicationService(repository, metadata, &changePlanAuditFake{}, nil)
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	draft, err := service.CloneCurrentDraft(t.Context(), "clone-plan", "Edit current system", 0, snapshot, graph, changePlanAdmin())
	if err != nil || draft.Status != "draft" || draft.Revision != 1 || len(metadata.listed) != len(businessChangePlanMetadataResourceTypes()) {
		t.Fatalf("draft=%#v listed=%#v err=%v", draft, metadata.listed, err)
	}
	var plan changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 2 || plan.Items[0].ItemID != "action:order.submit" || plan.Items[1].ItemID != "object:order" || plan.Items[0].Operation != "noop" || plan.Items[1].ExpectedResourceHash != "object-hash" {
		t.Fatalf("cloned plan=%#v", plan)
	}
	if plan.Items[0].ResourceOwner != "builder" || plan.Items[1].ResourceOwner != "template" || len(plan.ReleaseOrder) != 2 || plan.RollbackOrder[0] != "object:order" {
		t.Fatalf("clone ownership/order=%#v", plan)
	}
}
