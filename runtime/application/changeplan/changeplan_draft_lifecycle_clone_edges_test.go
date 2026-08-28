package changeplan

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func changePlanDraftPayload(t *testing.T, planID string) []byte {
	t.Helper()
	payload, err := json.Marshal(changeplanmodel.BusinessSystemChangePlan{PlanID: planID})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestApprovedDraftForPublishEdges(t *testing.T) {
	principal := changePlanAdmin()
	repository := &changePlanDraftRepositoryFake{}
	service := NewChangePlanApplicationService(repository, nil, nil, nil)
	if _, _, err := service.approvedDraftForPublish(t.Context(), " ", 1, principal); apperror.CodeOf(err) != "backend.change_plan.plan_id_required" {
		t.Fatalf("blank id=%v", err)
	}
	wantErr := errors.New("get")
	repository.getErr = wantErr
	if _, _, err := service.approvedDraftForPublish(t.Context(), "plan", 1, principal); !errors.Is(err, wantErr) {
		t.Fatalf("get=%v", err)
	}
	repository.getErr = nil
	if _, _, err := service.approvedDraftForPublish(t.Context(), "plan", 1, principal); apperror.CodeOf(err) != "backend.change_plan.draft_not_found" {
		t.Fatalf("missing=%v", err)
	}
	repository.found = true
	for _, draft := range []changeplanmodel.BusinessChangePlanDraft{
		{Status: "approved", Revision: 1, Payload: []byte("{")},
		{Status: "approved", Revision: 1, Payload: changePlanDraftPayload(t, "other")},
	} {
		repository.draft = draft
		if _, _, err := service.approvedDraftForPublish(t.Context(), "plan", 1, principal); err == nil {
			t.Fatalf("invalid draft accepted: %#v", draft)
		}
	}
	for _, testCase := range []struct {
		status   string
		revision int
		expected int
	}{
		{"approved", 1, 0}, {"draft", 1, 1}, {"applying", 1, 1}, {"published", 1, 1},
	} {
		repository.draft = changeplanmodel.BusinessChangePlanDraft{Status: testCase.status, Revision: testCase.revision, Payload: changePlanDraftPayload(t, "plan")}
		if _, _, err := service.approvedDraftForPublish(t.Context(), "plan", testCase.expected, principal); err == nil {
			t.Fatalf("invalid state accepted: %#v", testCase)
		}
	}
	for _, testCase := range []struct {
		status   string
		revision int
		expected int
	}{{"approved", 2, 2}, {"applying", 3, 2}, {"published", 3, 2}} {
		repository.draft = changeplanmodel.BusinessChangePlanDraft{Status: testCase.status, Revision: testCase.revision, Payload: changePlanDraftPayload(t, "plan")}
		if _, _, err := service.approvedDraftForPublish(t.Context(), "plan", testCase.expected, principal); err != nil {
			t.Fatalf("valid state %#v: %v", testCase, err)
		}
	}
}

func TestReviewableDraftEdges(t *testing.T) {
	principal := changePlanAdmin()
	repository := &changePlanDraftRepositoryFake{}
	service := NewChangePlanApplicationService(repository, nil, nil, nil)
	if _, _, err := service.reviewableDraft(t.Context(), "", 1, "draft", principal); err == nil {
		t.Fatal("blank review id accepted")
	}
	repository.getErr = errors.New("get")
	if _, _, err := service.reviewableDraft(t.Context(), "plan", 1, "draft", principal); !errors.Is(err, repository.getErr) {
		t.Fatalf("get=%v", err)
	}
	repository.getErr = nil
	if _, _, err := service.reviewableDraft(t.Context(), "plan", 1, "draft", principal); err == nil {
		t.Fatal("missing draft accepted")
	}
	repository.found = true
	for _, testCase := range []struct {
		revision int
		status   string
		expected int
	}{{1, "draft", 0}, {2, "draft", 1}, {1, "review", 1}} {
		repository.draft = changeplanmodel.BusinessChangePlanDraft{Revision: testCase.revision, Status: testCase.status, Payload: changePlanDraftPayload(t, "plan")}
		if _, _, err := service.reviewableDraft(t.Context(), "plan", testCase.expected, "draft", principal); err == nil {
			t.Fatalf("invalid review state accepted: %#v", testCase)
		}
	}
	repository.draft = changeplanmodel.BusinessChangePlanDraft{Revision: 1, Status: "draft", Payload: []byte("{")}
	if _, _, err := service.reviewableDraft(t.Context(), "plan", 1, "draft", principal); err == nil {
		t.Fatal("malformed draft accepted")
	}
	repository.draft.Payload = changePlanDraftPayload(t, "other")
	if _, _, err := service.reviewableDraft(t.Context(), "plan", 1, "draft", principal); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	repository.draft.Payload = changePlanDraftPayload(t, "plan")
	if _, _, err := service.reviewableDraft(t.Context(), "plan", 1, "draft", principal); err != nil {
		t.Fatal(err)
	}
}

func TestDraftLifecycleAuthorizationTransitionAndAuditEdges(t *testing.T) {
	repository := &changePlanDraftRepositoryFake{found: true, draft: changeplanmodel.BusinessChangePlanDraft{PlanID: "plan", Revision: 1, Status: "draft", Payload: changePlanDraftPayload(t, "plan")}}
	service := NewChangePlanApplicationService(repository, nil, nil, nil)
	unknown := principalmodel.Principal{}
	denied := changePlanAdmin()
	denied = changePlanWithoutPermissions(denied)
	for _, principal := range []principalmodel.Principal{unknown, denied} {
		if _, _, err := service.SubmitDraftForReview(t.Context(), "plan", 1, nil, nil, principal); err == nil {
			t.Fatalf("submit authorized %#v", principal)
		}
		if _, _, err := service.ApproveDraft(t.Context(), "plan", 1, nil, nil, principal); err == nil {
			t.Fatalf("approve authorized %#v", principal)
		}
		if _, _, err := service.PublishApprovedDraftIdempotent(t.Context(), "plan", 1, "", "", nil, nil, principal); err == nil {
			t.Fatalf("publish authorized %#v", principal)
		}
	}
	if _, _, err := service.SubmitDraftForReview(t.Context(), "missing", 1, nil, nil, changePlanAdmin()); err == nil {
		t.Fatal("submit reviewable error lost")
	}
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph"}
	plan := changePlanTestPlan(snapshot, graph)
	repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 1, Status: "draft", Payload: changePlanDraftPayload(t, plan.PlanID)}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := service.SubmitDraftForReview(cancelled, plan.PlanID, 1, snapshot, graph, changePlanAdmin()); err == nil {
		t.Fatal("submit validation error lost")
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	repository.draft.Payload = payload
	repository.transitionErr = errors.New("submit transition")
	service = NewChangePlanApplicationService(repository, nil, nil, &changePlanRuntimeFake{})
	if _, _, err := service.SubmitDraftForReview(t.Context(), plan.PlanID, 1, snapshot, graph, changePlanAdmin()); !errors.Is(err, repository.transitionErr) {
		t.Fatalf("submit transition=%v", err)
	}
	repository.transitionErr = nil
	repository.draft.Status = "in_review"
	repository.draft.UpdatedBy = "author"
	blankReviewer := changePlanAdmin()
	blankReviewer.UserID = ""
	if _, _, err := service.ApproveDraft(t.Context(), plan.PlanID, 1, snapshot, graph, blankReviewer); apperror.CodeOf(err) != "backend.change_plan.maker_checker_required" {
		t.Fatalf("blank reviewer=%v", err)
	}
	if _, _, err := service.ApproveDraft(t.Context(), "missing", 1, snapshot, graph, changePlanAdmin()); err == nil {
		t.Fatal("approve reviewable error lost")
	}
	repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 1, Status: "in_review", Payload: payload, UpdatedBy: "author"}
	approver := changePlanAdmin()
	approver.UserID = "approver"
	runtime := &changePlanRuntimeFake{validateErr: errors.New("validation")}
	service = NewChangePlanApplicationService(repository, nil, nil, runtime)
	if _, _, err := service.ApproveDraft(t.Context(), plan.PlanID, 1, snapshot, graph, approver); !errors.Is(err, runtime.validateErr) {
		t.Fatalf("approve validation=%v", err)
	}
	runtime.validateErr = nil
	repository.transitionErr = errors.New("approve transition")
	if _, _, err := service.ApproveDraft(t.Context(), plan.PlanID, 1, snapshot, graph, approver); !errors.Is(err, repository.transitionErr) {
		t.Fatalf("approve transition=%v", err)
	}
	repository.transitionErr = nil
	repository.draft = changeplanmodel.BusinessChangePlanDraft{PlanID: plan.PlanID, Revision: 1, Status: "approved", Payload: payload, UpdatedBy: "approver"}
	if _, _, err := service.PublishApprovedDraftIdempotent(t.Context(), plan.PlanID, 1, plan.PlanID, "operation", snapshot, graph, approver); err == nil {
		t.Fatal("expected downstream publish failure without metadata repository")
	}
	service.auditDraftLifecycle(t.Context(), repository.draft, changePlanAdmin(), "event")
	wantErr := errors.New("transition")
	repository.transitionErr = wantErr
	if _, err := service.transitionDraft(t.Context(), repository.draft, "in_review", "approved", changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("transition error=%v", err)
	}
	repository.transitionErr = nil
	repository.draft.Status = "other"
	if _, err := service.transitionDraft(t.Context(), repository.draft, "in_review", "approved", changePlanAdmin()); err == nil {
		t.Fatal("unchanged transition accepted")
	}
}

func TestValidateReviewedDraftFailureAndScenarioEdges(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph"}
	plan := changePlanTestPlan(snapshot, graph)
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, nil, &changePlanRuntimeFake{})
	if _, err := service.validateReviewedDraft(t.Context(), plan, "reviewer", snapshot, graph, principalmodel.Principal{}); err == nil {
		t.Fatal("unauthorized validation accepted")
	}
	invalid := plan
	invalid.BusinessReason = ""
	validation, err := service.validateReviewedDraft(t.Context(), invalid, "reviewer", snapshot, graph, changePlanAdmin())
	if err == nil || validation.Valid || len(validation.Issues) == 0 {
		t.Fatalf("invalid validation=%#v err=%v", validation, err)
	}
	runtime := &changePlanRuntimeFake{validateErr: errors.New("metadata")}
	service = NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, nil, runtime)
	if _, err := service.validateReviewedDraft(t.Context(), plan, "reviewer", snapshot, graph, changePlanAdmin()); !errors.Is(err, runtime.validateErr) {
		t.Fatalf("metadata validation=%v", err)
	}
	runtime.validateErr = nil
	missingScenario := plan
	missingScenario.AcceptanceScenarios = []changeplanmodel.BusinessAcceptanceScenario{{
		Key: "scenario", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "missing",
		Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true},
	}}
	if _, err := service.validateReviewedDraft(t.Context(), missingScenario, "reviewer", snapshot, graph, changePlanAdmin()); err == nil {
		t.Fatal("scenario simulation error lost")
	}
}

func TestValidateReviewedDraftAcceptanceScenarioPassAndFailure(t *testing.T) {
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph"}
	plan := changePlanTestPlan(snapshot, graph)
	plan.BusinessReason = "Add an order approval command"
	plan.ReleaseOrder = []string{"object", "action"}
	plan.RollbackOrder = []string{"action", "object"}
	plan.Items = []changeplanmodel.BusinessSystemChangeItem{
		{ItemID: "object", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "object", ResourceKey: "order", ResourceOwner: "builder", CapabilityKey: "schema.object", After: json.RawMessage(`{"key":"order","name":"Order","fields":[]}`), ValidationMethods: []string{"metadata.validate"}},
		{ItemID: "action", Operation: "create", ChangeKind: "additive", RiskLevel: "low", ResourceType: "action", ResourceKey: "order.approve", ResourceOwner: "builder", CapabilityKey: "action.definition", After: json.RawMessage(`{"key":"order.approve","object_key":"order","kind":"record_operation","config":{"steps":[]}}`), Dependencies: []changeplanmodel.BusinessChangeTarget{{ResourceType: "object", ResourceKey: "order", Reason: "action target"}}, ValidationMethods: []string{"metadata.validate"}},
	}
	plan.AcceptanceScenarios = []changeplanmodel.BusinessAcceptanceScenario{
		{Key: "approve", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.approve", Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true}},
		{Key: "approve-again", Kind: changeplanmodel.BusinessAcceptanceScenarioKindActionDefinition, ResourceKey: "order.approve", Expected: changeplanmodel.BusinessAcceptanceScenarioExpectation{Valid: true}},
	}
	metadata := &changePlanRuntimeFake{}
	scenarios := &changePlanScenarioRuntimeFake{result: AcceptanceScenarioRuntimeResult{Valid: true, SideEffectFree: true}}
	service := NewChangePlanApplicationService(&changePlanDraftRepositoryFake{}, nil, nil, metadata, scenarios)
	validation, err := service.validateReviewedDraft(t.Context(), plan, "reviewer", snapshot, graph, changePlanAdmin())
	if err != nil || !validation.Valid {
		t.Fatalf("passing validation=%#v err=%v", validation, err)
	}
	scenarios.results = []AcceptanceScenarioRuntimeResult{{Valid: true, SideEffectFree: true}, {Valid: false, SideEffectFree: true}}
	if _, err := service.validateReviewedDraft(t.Context(), plan, "reviewer", snapshot, graph, changePlanAdmin()); apperror.CodeOf(err) != "backend.change_plan.acceptance_scenario_failed" {
		t.Fatalf("failed scenario=%v", err)
	}
}

func TestCloneCurrentDraftFailureAndOwnerEdges(t *testing.T) {
	repository := &changePlanDraftRepositoryFake{saveOK: true}
	snapshot := changePlanTestSnapshot()
	graph := ReferenceGraph{Version: BusinessReferenceGraphVersion, Hash: "graph"}
	if _, err := NewChangePlanApplicationService(repository, nil, nil, nil).CloneCurrentDraft(t.Context(), "plan", "reason", 0, snapshot, graph, principalmodel.Principal{}); err == nil {
		t.Fatal("unauthorized clone accepted")
	}
	if _, err := NewChangePlanApplicationService(repository, nil, nil, nil).CloneCurrentDraft(t.Context(), "plan", "reason", 0, snapshot, graph, changePlanAdmin()); err == nil {
		t.Fatal("clone without lister accepted")
	}
	wantErr := errors.New("list")
	metadata := &changePlanCloneMetadataFake{err: wantErr}
	if _, err := NewChangePlanApplicationService(repository, metadata, nil, nil).CloneCurrentDraft(t.Context(), "plan", "reason", 0, snapshot, graph, changePlanAdmin()); !errors.Is(err, wantErr) {
		t.Fatalf("clone list=%v", err)
	}
	for source, owner := range map[string]string{
		"builder": "builder", "builder_v4": "builder", "builder_v5": "builder", "plugin": "plugin", "manual": "manual",
		"platform": "platform", "runtime": "platform", "template": "template", "generated": "template", "manifest": "template", "other": "unknown",
	} {
		if got := changePlanCloneOwner(source); got != owner {
			t.Fatalf("owner %s=%s want=%s", source, got, owner)
		}
	}
}

func TestTransitionDraftRejectsRepositoryWithoutLifecyclePort(t *testing.T) {
	service := NewChangePlanApplicationService(&serviceRepositoryStub{}, nil, nil, nil)
	if _, err := service.transitionDraft(t.Context(), changeplanmodel.BusinessChangePlanDraft{PlanID: "plan"}, "draft", "in_review", changePlanAdmin()); err == nil {
		t.Fatal("repository without lifecycle port accepted")
	}
}
