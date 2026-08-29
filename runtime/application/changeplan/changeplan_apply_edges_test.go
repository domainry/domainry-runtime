package changeplan

import (
	"context"
	"errors"
	"testing"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type changePlanMetadataFake struct {
	definitions []metadatamodel.MetadataDefinition
	err         error
	mutations   []metadatamodel.MetadataDefinitionMutation
	publication *changeplanmodel.BusinessChangePlanPublication
}

func (f *changePlanMetadataFake) ApplyDefinitionMutations(_ context.Context, _ principalmodel.SystemScope, mutations []metadatamodel.MetadataDefinitionMutation, _ []auditmodel.AuditEvent, publication *changeplanmodel.BusinessChangePlanPublication) ([]metadatamodel.MetadataDefinition, error) {
	f.mutations, f.publication = mutations, publication
	return f.definitions, f.err
}

func TestChangePlanApplySuccessAndPersistenceFailures(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	metadata := &changePlanMetadataFake{definitions: []metadatamodel.MetadataDefinition{{ResourceType: "field", ResourceKey: "customer.segment"}}}
	runtime := &changePlanRuntimeFake{reloadHash: "schema-hash"}
	repository := &changePlanDraftRepositoryFake{}
	audit := &changePlanAuditFake{}
	service := NewChangePlanApplicationService(repository, metadata, audit, runtime)

	result, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin())
	if err != nil || result.Status != "applied" || result.SchemaHash != "schema-hash" || len(result.AppliedDefinitions) != 1 || len(metadata.mutations) != 1 || metadata.publication.PlanID != plan.PlanID {
		t.Fatalf("apply result=%+v err=%v mutations=%+v publication=%+v", result, err, metadata.mutations, metadata.publication)
	}
	if len(audit.events) == 0 || audit.events[len(audit.events)-1].Event != "business_change_plan.published" {
		t.Fatalf("apply audit = %+v", audit.events)
	}

	wantErr := errors.New("persistence failed")
	metadata.err = &metadatamodel.MetadataDefinitionConflictError{ResourceType: "field", ResourceKey: "customer.segment"}
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.metadata.definition_version_conflict" {
		t.Fatalf("metadata version conflict = %v", err)
	}
	metadata.err = &changeplanmodel.BusinessChangePlanDraftConflictError{PlanID: plan.PlanID}
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
		t.Fatalf("publication conflict = %v", err)
	}
	metadata.err = wantErr
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); !errors.Is(err, wantErr) || apperror.CodeOf(err) != "backend.internal" {
		t.Fatalf("metadata error = %v", err)
	}
	metadata.err = nil
	runtime.reloadErr = wantErr
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("reload error = %v", err)
	}
}

func TestChangePlanApplyReportsMissingMetadataRuntimeAfterValidation(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, &changePlanMetadataFake{}, &changePlanAuditFake{}, nil)
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); err == nil {
		t.Fatal("missing metadata runtime accepted")
	}
}

func TestChangePlanApplyFinalizesDraftAndDetectsFinalizeRaces(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	plan.DraftRevision = 2
	metadata := &changePlanMetadataFake{}
	runtime := &changePlanRuntimeFake{reloadHash: "schema-hash"}
	repository := &changePlanDraftRepositoryFake{publishOK: true, published: changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 3, Status: "published"}}
	service := NewChangePlanApplicationService(repository, metadata, &changePlanAuditFake{}, runtime)
	if result, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); err != nil || result.Status != "applied" {
		t.Fatalf("finalized result=%+v err=%v", result, err)
	}
	wantErr := errors.New("publish failed")
	repository.publishErr = wantErr
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("publish error = %v", err)
	}
	repository.publishErr, repository.publishOK = nil, false
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
		t.Fatalf("publish compare miss = %v", err)
	}
	repository.publishOK = true
	repository.published.Status = "draft"
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.draft_version_conflict" {
		t.Fatalf("publish status conflict = %v", err)
	}
}

func TestChangePlanApplicationErrorAndAuditHelpers(t *testing.T) {
	wantErr := errors.New("plain")
	if ErrorCodeOf(wantErr) != "backend.internal" || wrapMetadataError(nil) != nil {
		t.Fatal("error code helpers mismatch")
	}
	appErr := forbidden("denied", "resource", "plan")
	if wrapMetadataError(appErr) != appErr || ErrorCodeOf(appErr) != "denied" {
		t.Fatal("app error was not preserved")
	}
	version := &metadatamodel.MetadataDefinitionConflictError{ResourceType: "field", ResourceKey: "order.status", ExpectedHash: "old", CurrentHash: "new"}
	if apperror.CodeOf(wrapMetadataError(version)) != "backend.metadata.definition_version_conflict" {
		t.Fatalf("version conflict = %v", wrapMetadataError(version))
	}
	for _, err := range []error{badRequest("bad"), notFound("missing"), conflict("conflict"), internalError("operation", wantErr)} {
		if ErrorCodeOf(err) == "" {
			t.Fatalf("empty code for %v", err)
		}
	}
	principal := changePlanAdmin()
	principal.RequestID = "request-1"
	event := buildAuditEvent("event", "object", "record", principal, "summary", nil, nil, nil)
	next := buildAuditEvent("event", "object", "record", principal, "summary", nil, nil, nil)
	if next.ID == event.ID {
		t.Fatalf("change plan audit IDs collided: %q", event.ID)
	}
	if event.ActorID != principal.UserID || event.Metadata["request_id"] != "request-1" || event.Metadata["workspace_id"] != principal.WorkspaceID {
		t.Fatalf("audit event = %+v", event)
	}
	principal.UserID, principal.RoleKey = "", ""
	event = buildAuditEvent("event", "object", "record", principal, "summary", nil, nil, map[string]any{})
	if event.ActorID != "system" {
		t.Fatalf("system actor event = %+v", event)
	}
}

func TestChangePlanApplyAndValidateAuthorizationEdges(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph-hash"}
	plan := changePlanTestPlan(snapshot, graph)
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, &changePlanMetadataFake{}, &changePlanAuditFake{}, &changePlanRuntimeFake{})
	missingWorkspace := changePlanAdmin()
	missingWorkspace.WorkspaceID = ""
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, missingWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("apply workspace error=%v", err)
	}
	if _, err := service.Validate(t.Context(), plan, snapshot, graph, missingWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("validate workspace error=%v", err)
	}
	denied := changePlanAdmin()
	denied = changePlanWithoutPermissions(denied)
	if _, err := service.Apply(t.Context(), plan, plan.PlanID, snapshot, graph, denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("apply denied error=%v", err)
	}
	if _, err := service.Validate(t.Context(), plan, snapshot, graph, denied); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("validate denied error=%v", err)
	}
	if _, err := service.Validate(t.Context(), plan, snapshot, graph, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("validate unknown error=%v", err)
	}
	event := buildAuditEvent("event", "object", "record", changePlanAdmin(), "summary", nil, nil, nil)
	if event.Metadata == nil {
		t.Fatal("nil metadata was not initialized")
	}
}
