package changeplan

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func (s *ChangePlanApplicationService) Draft(ctx context.Context, planID string, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, error) {
	if err := changePlanAuthorizeQuery(principal); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return changeplanmodel.BusinessChangePlanDraft{}, forbidden("auth.permission_denied")
	}
	draft, found, err := s.repository.GetDraft(ctx, principal.WorkspaceID, strings.TrimSpace(planID))
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, internalError("get domain change plan draft", err)
	}
	if !found {
		return changeplanmodel.BusinessChangePlanDraft{}, notFound("backend.change_plan.draft_not_found")
	}
	return draft, nil
}

func (s *ChangePlanApplicationService) SaveDraft(ctx context.Context, plan changeplanmodel.BusinessSystemChangePlan, expectedRevision int, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, error) {
	if err := changePlanAuthorizeCommand(principal); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return changeplanmodel.BusinessChangePlanDraft{}, forbidden("auth.permission_denied")
	}
	plan.PlanID = strings.TrimSpace(plan.PlanID)
	if plan.PlanID == "" {
		return changeplanmodel.BusinessChangePlanDraft{}, badRequest("backend.change_plan.plan_id_required")
	}
	// Review evidence is server-owned lifecycle state. A draft author cannot
	// self-approve by posting Reviewed fields in the plan document.
	plan.Reviewed, plan.ReviewedBy = false, ""
	current, found, err := s.repository.GetDraft(ctx, principal.WorkspaceID, plan.PlanID)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, internalError("get domain change plan draft", err)
	}
	if (expectedRevision == 0 && found) || (expectedRevision > 0 && (!found || current.Revision != expectedRevision || current.Status != "draft")) {
		return changeplanmodel.BusinessChangePlanDraft{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", plan.PlanID)
	}
	plan.DraftRevision = expectedRevision + 1
	payload, err := json.Marshal(plan)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, badRequest("backend.change_plan.draft_invalid")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	createdAt, createdBy := now, principal.UserID
	if found {
		createdAt, createdBy = current.CreatedAt, current.CreatedBy
	}
	draft := changeplanmodel.BusinessChangePlanDraft{WorkspaceID: principal.WorkspaceID, PlanID: plan.PlanID, Revision: plan.DraftRevision, Status: "draft", Payload: payload, CreatedBy: createdBy, UpdatedBy: principal.UserID, CreatedAt: createdAt, UpdatedAt: now}
	saved, savedOK, err := s.repository.SaveDraft(ctx, principal.WorkspaceID, draft, expectedRevision)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, internalError("save domain change plan draft", err)
	}
	if !savedOK {
		return changeplanmodel.BusinessChangePlanDraft{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", plan.PlanID)
	}
	audit := buildAuditEvent("business_change_plan.draft_saved", "business_change_plan", plan.PlanID, principal, "Saved domain change plan draft", nil, map[string]any{"revision": saved.Revision}, map[string]any{"business_reason": plan.BusinessReason, "builder_task_id": plan.BuilderTaskID, "change_plan_id": plan.PlanID})
	if err := s.audit.InsertAuditEvent(ctx, audit.WorkspaceID, audit); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, internalError("insert domain change plan audit", err)
	}
	return saved, nil
}

func businessChangePlanDraftConflict(err error) bool {
	var conflictErr *changeplanmodel.BusinessChangePlanDraftConflictError
	return errors.As(err, &conflictErr)
}

func (s *ChangePlanApplicationService) SubmitDraftForReview(ctx context.Context, planID string, expectedRevision int, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, changeplanmodel.BusinessChangePlanValidation, error) {
	if err := changePlanAuthorizeCommand(principal); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessChangePlanValidation{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessChangePlanValidation{}, forbidden("auth.permission_denied")
	}
	draft, plan, err := s.reviewableDraft(ctx, planID, expectedRevision, "draft", principal)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessChangePlanValidation{}, err
	}
	validation, err := s.validateReviewedDraft(ctx, plan, "pending-review", snapshot, graph, principal)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, validation, err
	}
	transitioned, err := s.transitionDraft(ctx, draft, "draft", "in_review", principal)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, validation, err
	}
	s.auditDraftLifecycle(ctx, transitioned, principal, "submitted_for_review")
	return transitioned, validation, nil
}

func (s *ChangePlanApplicationService) ApproveDraft(ctx context.Context, planID string, expectedRevision int, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, changeplanmodel.BusinessChangePlanValidation, error) {
	if err := changePlanAuthorizeCommand(principal); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessChangePlanValidation{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessChangePlanValidation{}, forbidden("auth.permission_denied")
	}
	draft, plan, err := s.reviewableDraft(ctx, planID, expectedRevision, "in_review", principal)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessChangePlanValidation{}, err
	}
	if strings.TrimSpace(principal.UserID) == "" || strings.TrimSpace(principal.UserID) == strings.TrimSpace(draft.UpdatedBy) {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessChangePlanValidation{}, forbidden("backend.change_plan.maker_checker_required")
	}
	validation, err := s.validateReviewedDraft(ctx, plan, principal.UserID, snapshot, graph, principal)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, validation, err
	}
	transitioned, err := s.transitionDraft(ctx, draft, "in_review", "approved", principal)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, validation, err
	}
	s.auditDraftLifecycle(ctx, transitioned, principal, "approved")
	return transitioned, validation, nil
}

func (s *ChangePlanApplicationService) PublishApprovedDraftIdempotent(ctx context.Context, planID string, expectedRevision int, confirmation, operationKey string, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (BusinessChangePlanApplyResult, bool, error) {
	if err := changePlanAuthorizeCommand(principal); err != nil {
		return BusinessChangePlanApplyResult{}, false, err
	}
	if !principal.HasPermission("workspace.admin") {
		return BusinessChangePlanApplyResult{}, false, forbidden("auth.permission_denied")
	}
	draft, plan, err := s.approvedDraftForPublish(ctx, planID, expectedRevision, principal)
	if err != nil {
		return BusinessChangePlanApplyResult{}, false, err
	}
	plan.DraftRevision = expectedRevision
	plan.Reviewed, plan.ReviewedBy = true, strings.TrimSpace(draft.UpdatedBy)
	return s.ApplyIdempotent(ctx, plan, confirmation, operationKey, snapshot, graph, principal)
}

func (s *ChangePlanApplicationService) approvedDraftForPublish(ctx context.Context, planID string, expectedRevision int, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, changeplanmodel.BusinessSystemChangePlan, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, badRequest("backend.change_plan.plan_id_required")
	}
	draft, found, err := s.repository.GetDraft(ctx, principal.WorkspaceID, planID)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, internalError("get domain change plan draft", err)
	}
	if !found {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, notFound("backend.change_plan.draft_not_found")
	}
	validState := draft.Status == "approved" && draft.Revision == expectedRevision
	// applying/published is accepted only to reach the idempotency receipt for
	// this exact approved revision; Apply is never executed again on replay.
	validReplayState := (draft.Status == "applying" || draft.Status == "published") && draft.Revision == expectedRevision+1
	if expectedRevision <= 0 || (!validState && !validReplayState) {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", planID, "expected_status", "approved")
	}
	var plan changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &plan); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, badRequest("backend.change_plan.draft_invalid")
	}
	if strings.TrimSpace(plan.PlanID) != planID {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, conflict("backend.change_plan.draft_identity_mismatch")
	}
	return draft, plan, nil
}

func (s *ChangePlanApplicationService) reviewableDraft(ctx context.Context, planID string, expectedRevision int, status string, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, changeplanmodel.BusinessSystemChangePlan, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, badRequest("backend.change_plan.plan_id_required")
	}
	draft, found, err := s.repository.GetDraft(ctx, principal.WorkspaceID, planID)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, internalError("get domain change plan draft", err)
	}
	if !found {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, notFound("backend.change_plan.draft_not_found")
	}
	if expectedRevision <= 0 || draft.Revision != expectedRevision || draft.Status != status {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", planID, "expected_status", status)
	}
	var plan changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &plan); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, badRequest("backend.change_plan.draft_invalid")
	}
	if strings.TrimSpace(plan.PlanID) != planID {
		return changeplanmodel.BusinessChangePlanDraft{}, changeplanmodel.BusinessSystemChangePlan{}, conflict("backend.change_plan.draft_identity_mismatch")
	}
	return draft, plan, nil
}

func (s *ChangePlanApplicationService) validateReviewedDraft(ctx context.Context, plan changeplanmodel.BusinessSystemChangePlan, reviewer string, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanValidation, error) {
	plan.Reviewed, plan.ReviewedBy = true, strings.TrimSpace(reviewer)
	validation, err := s.Validate(ctx, plan, snapshot, graph, principal)
	if err != nil {
		return validation, err
	}
	// This path sets server-owned review evidence before validation, therefore
	// ApplyAllowed is equivalent to Valid under the validation contract.
	if !validation.Valid {
		// Invalid validation results always carry at least one issue by the
		// domain validator contract.
		issueCode := validation.Issues[0].Code
		return validation, conflict("backend.change_plan.review_not_ready", "plan_id", plan.PlanID, "issue", issueCode)
	}
	mutations, _, err := s.metadataChangePlanMutations(ctx, plan, principal)
	if err != nil {
		return validation, err
	}
	if len(plan.AcceptanceScenarios) > 0 {
		simulation, err := s.simulateAcceptanceScenarios(ctx, plan, mutations, principal)
		if err != nil {
			return validation, err
		}
		if !simulation.Passed {
			failed := ""
			for _, scenario := range simulation.Results {
				if !scenario.Passed {
					failed = scenario.Key
					break
				}
			}
			return validation, conflict("backend.change_plan.acceptance_scenario_failed", "plan_id", plan.PlanID, "scenario_key", failed)
		}
	}
	return validation, nil
}

func (s *ChangePlanApplicationService) transitionDraft(ctx context.Context, draft changeplanmodel.BusinessChangePlanDraft, fromStatus, toStatus string, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, error) {
	repository, ok := s.repository.(changeplanrepository.ChangePlanLifecycleRepository)
	if !ok {
		return changeplanmodel.BusinessChangePlanDraft{}, internalError("transition domain change plan draft", errors.New("change plan lifecycle repository unavailable"))
	}
	transitioned, changed, err := repository.TransitionDraft(ctx, principal.WorkspaceID, draft.PlanID, draft.Revision, fromStatus, toStatus, principal.UserID, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, internalError("transition domain change plan draft", err)
	}
	if !changed {
		return changeplanmodel.BusinessChangePlanDraft{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", draft.PlanID)
	}
	return transitioned, nil
}

func (s *ChangePlanApplicationService) auditDraftLifecycle(ctx context.Context, draft changeplanmodel.BusinessChangePlanDraft, principal principalmodel.Principal, event string) {
	if s.audit == nil {
		return
	}
	audit := buildAuditEvent("business_change_plan."+event, "business_change_plan", draft.PlanID, principal, "Business change plan "+event, nil, map[string]any{"status": draft.Status, "revision": draft.Revision}, map[string]any{"change_plan_id": draft.PlanID, "revision": draft.Revision, "status": draft.Status})
	_ = s.audit.InsertAuditEvent(ctx, audit.WorkspaceID, audit)
}

type changePlanDefinitionLister interface {
	ListDefinitions(context.Context, principalmodel.SystemScope, string) ([]metadatamodel.MetadataDefinition, error)
}

func (s *ChangePlanApplicationService) CloneCurrentDraft(ctx context.Context, planID, businessReason string, expectedRevision int, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanDraft, error) {
	if err := changePlanAuthorizeCommand(principal); err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, err
	}
	lister, ok := s.metadata.(changePlanDefinitionLister)
	if !ok {
		return changeplanmodel.BusinessChangePlanDraft{}, internalError("clone current metadata definitions", errors.New("metadata definition lister unavailable"))
	}
	snapshotValue, graphValue := snapshot.ChangePlanSnapshot(), graph.ChangePlanReferenceGraph()
	plan := changeplanmodel.BusinessSystemChangePlan{
		PlanVersion: changeplanmodel.BusinessSystemChangePlanVersion, PlanID: strings.TrimSpace(planID), BusinessReason: strings.TrimSpace(businessReason),
		SnapshotHash: snapshotValue.SnapshotHash, ReferenceGraphHash: graphValue.Hash, RuntimeVersion: snapshotValue.RuntimeVersion,
		AuthoringContractVersion: snapshotValue.AuthoringContractVersion, AuthoringContractHash: snapshotValue.AuthoringContractHash,
		ReleaseOrder: []string{}, RollbackOrder: []string{}, Items: []changeplanmodel.BusinessSystemChangeItem{}, AcceptanceScenarios: []changeplanmodel.BusinessAcceptanceScenario{},
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "clone current metadata definitions into reviewed system draft")
	for _, resourceType := range businessChangePlanMetadataResourceTypes() {
		definitions, err := lister.ListDefinitions(ctx, scope, resourceType)
		if err != nil {
			return changeplanmodel.BusinessChangePlanDraft{}, internalError("clone current metadata definitions", err)
		}
		for _, definition := range definitions {
			if strings.TrimSpace(definition.DisabledAt) != "" {
				continue
			}
			itemID := resourceType + ":" + strings.TrimSpace(definition.ResourceKey)
			owner := changePlanCloneOwner(definition.SourceKind)
			plan.Items = append(plan.Items, changeplanmodel.BusinessSystemChangeItem{
				ItemID: itemID, Operation: "noop", ChangeKind: "additive", RiskLevel: "low", ResourceType: resourceType, ResourceKey: definition.ResourceKey,
				ResourceOwner: owner, OwnerAuthorized: owner == "builder", CapabilityKey: "maintenance.change_plan_validation", ExpectedResourceHash: definition.SchemaHash,
				Before: append([]byte(nil), definition.Payload...), After: append([]byte(nil), definition.Payload...), ValidationMethods: []string{"metadata.snapshot_clone"},
			})
		}
	}
	sort.Slice(plan.Items, func(i, j int) bool { return plan.Items[i].ItemID < plan.Items[j].ItemID })
	for _, item := range plan.Items {
		plan.ReleaseOrder = append(plan.ReleaseOrder, item.ItemID)
	}
	for index := len(plan.Items) - 1; index >= 0; index-- {
		plan.RollbackOrder = append(plan.RollbackOrder, plan.Items[index].ItemID)
	}
	return s.SaveDraft(ctx, plan, expectedRevision, principal)
}

func changePlanCloneOwner(sourceKind string) string {
	switch strings.TrimSpace(sourceKind) {
	case "builder", "builder_v4", "builder_v5":
		return "builder"
	case "plugin":
		return "plugin"
	case "manual":
		return "manual"
	case "platform", "runtime":
		return "platform"
	case "template", "generated", "manifest":
		return "template"
	default:
		return "unknown"
	}
}
