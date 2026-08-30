package changeplan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	changeplancontract "github.com/domainry/domainry-runtime/runtime/domain/changeplan/contract"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	changeplanvalidation "github.com/domainry/domainry-runtime/runtime/domain/changeplan/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"

	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

type BusinessChangePlanApplyResult struct {
	PlanID             string                                       `json:"plan_id"`
	Status             string                                       `json:"status"`
	AppliedDefinitions []appschemamodel.ApplicationDefinition       `json:"applied_definitions"`
	SchemaHash         string                                       `json:"schema_hash"`
	Validation         changeplanmodel.BusinessChangePlanValidation `json:"validation"`
}

type changePlanApplyReceipt struct {
	Result      BusinessChangePlanApplyResult `json:"result"`
	ErrorKind   apperror.ErrorKind            `json:"error_kind,omitempty"`
	ErrorCode   string                        `json:"error_code,omitempty"`
	ErrorParams map[string]string             `json:"error_params,omitempty"`
}

func (s *ChangePlanApplicationService) ApplyIdempotent(ctx context.Context, plan changeplanmodel.BusinessSystemChangePlan, confirmation, operationKey string, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (BusinessChangePlanApplyResult, bool, error) {
	if err := changePlanAuthorizeCommand(principal); err != nil {
		return BusinessChangePlanApplyResult{}, false, err
	}
	operationKey = strings.TrimSpace(operationKey)
	if operationKey == "" {
		return BusinessChangePlanApplyResult{}, false, badRequest(idempotency.ErrorCodeMissingKey, "use_case", "change_plan.apply")
	}
	if !principal.HasPermission("workspace.admin") {
		return BusinessChangePlanApplyResult{}, false, forbidden("auth.permission_denied")
	}
	plan.PlanID = strings.TrimSpace(plan.PlanID)
	if plan.PlanID == "" {
		return BusinessChangePlanApplyResult{}, false, badRequest("backend.change_plan.plan_id_required")
	}
	if strings.TrimSpace(confirmation) != plan.PlanID {
		return BusinessChangePlanApplyResult{}, false, badRequest("backend.change_plan.confirmation_invalid", "expected", plan.PlanID)
	}
	if s.operations == nil {
		return BusinessChangePlanApplyResult{}, false, internalError("claim change plan operation", errors.New(idempotency.ErrorCodeReceiptUnavailable))
	}
	fingerprint, err := changePlanApplyFingerprint(plan, confirmation)
	if err != nil {
		return BusinessChangePlanApplyResult{}, false, internalError("fingerprint change plan operation", err)
	}
	leaseOwner := strings.TrimSpace(principal.RequestID)
	if leaseOwner == "" {
		leaseOwner = requestcontext.RequestID(ctx)
	}
	if leaseOwner == "" {
		leaseOwner = requestcontext.NewRequestID()
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	claim, err := s.operations.TryBeginOperation(ctx, workspaceID, changeplanmodel.ChangePlanOperationClaimRequest{
		Execution:          changeplanmodel.ChangePlanOperationExecution{WorkspaceID: workspaceID, PlanID: plan.PlanID, PlanRevision: plan.DraftRevision, Operation: "apply", IdempotencyKey: operationKey, ActorID: principal.UserID},
		RequestFingerprint: fingerprint, LeaseOwner: leaseOwner, LeaseTTL: 30 * time.Second, Now: time.Now().UTC(),
	})
	if err != nil {
		return BusinessChangePlanApplyResult{}, false, internalError("claim change plan operation", err)
	}
	switch claim.Decision {
	case idempotency.DecisionReplay:
		s.auditChangePlanIdempotency(ctx, plan, principal, claim, "replayed")
		var receipt changePlanApplyReceipt
		if err := json.Unmarshal(claim.Execution.Result, &receipt); err != nil {
			return BusinessChangePlanApplyResult{}, false, internalError("decode change plan operation replay", err)
		}
		if receipt.ErrorCode != "" {
			return BusinessChangePlanApplyResult{}, true, &apperror.AppError{Kind: receipt.ErrorKind, Code: receipt.ErrorCode, Params: receipt.ErrorParams}
		}
		return receipt.Result, true, nil
	case idempotency.DecisionFingerprintConflict:
		s.auditChangePlanIdempotency(ctx, plan, principal, claim, "fingerprint_conflict")
		return BusinessChangePlanApplyResult{}, false, conflict(idempotency.ErrorCodeKeyReused, "plan_id", plan.PlanID)
	case idempotency.DecisionInProgress:
		s.auditChangePlanIdempotency(ctx, plan, principal, claim, "in_progress")
		return BusinessChangePlanApplyResult{}, false, conflict(idempotency.ErrorCodeInProgress, "plan_id", plan.PlanID)
	case idempotency.DecisionAcquired:
	default:
		return BusinessChangePlanApplyResult{}, false, internalError("classify change plan operation", errors.New(idempotency.ErrorCodeReceiptUnavailable))
	}

	result, applyErr := s.Apply(ctx, plan, confirmation, snapshot, graph, principal)
	if applyErr != nil {
		receipt := changePlanReceiptForError(applyErr)
		if _, failErr := s.operations.FailOperation(ctx, workspaceID, changeplanmodel.ChangePlanOperationFailure{ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken, ErrorCode: receipt.ErrorCode, Result: receipt, Retryable: receipt.ErrorKind == apperror.KindInternal, ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour), Now: time.Now().UTC()}); failErr != nil {
			return BusinessChangePlanApplyResult{}, false, internalError("fail change plan operation", failErr)
		}
		status := "failed_terminal"
		if receipt.ErrorKind == apperror.KindInternal {
			status = "failed_retryable"
		}
		s.auditChangePlanIdempotency(ctx, plan, principal, claim, status)
		return BusinessChangePlanApplyResult{}, false, applyErr
	}
	if _, err := s.operations.CompleteOperation(ctx, workspaceID, changeplanmodel.ChangePlanOperationCompletion{ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken, Result: changePlanApplyReceipt{Result: result}, ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour), Now: time.Now().UTC()}); err != nil {
		return BusinessChangePlanApplyResult{}, false, internalError("complete change plan operation", err)
	}
	s.auditChangePlanIdempotency(ctx, plan, principal, claim, "succeeded")
	return result, false, nil
}

func (s *ChangePlanApplicationService) auditChangePlanIdempotency(ctx context.Context, plan changeplanmodel.BusinessSystemChangePlan, principal principalmodel.Principal, claim changeplanmodel.ChangePlanOperationClaimResult, status string) {
	if strings.TrimSpace(claim.Execution.ID) == "" {
		return
	}
	facts := idempotency.AuditFacts{
		WorkspaceID: claim.Execution.WorkspaceID, Scope: "change_plan.apply", Key: claim.Execution.IdempotencyKey,
		RequestFingerprint: claim.Execution.RequestFingerprint, Status: status, FencingToken: claim.Execution.FencingToken,
	}
	metadata := idempotency.AuditMetadata(facts)
	logging.LogIdempotency(ctx, facts, principal.RequestID)
	if s.audit == nil {
		return
	}
	event := buildAuditEvent("business_change_plan.idempotency_"+status, "business_change_plan", plan.PlanID, principal, "Observed idempotent change plan apply", nil, nil, metadata)
	_ = s.audit.InsertAuditEvent(ctx, event.WorkspaceID, event)
}

func changePlanApplyFingerprint(plan changeplanmodel.BusinessSystemChangePlan, confirmation string) (string, error) {
	// Approval evidence is server-owned lifecycle state and intentionally not
	// part of the request fingerprint. This lets a published draft replay its
	// receipt after updated_by moves from approver to publisher without weakening
	// the immutable plan/revision fingerprint.
	plan.Reviewed, plan.ReviewedBy = false, ""
	raw, err := json.Marshal(plan)
	if err != nil {
		return "", err
	}
	var canonical any
	// json.Marshal emitted valid JSON above, so decoding the same bytes into a
	// generic value cannot fail.
	_ = json.Unmarshal(raw, &canonical)
	return idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "change_plan.apply", ResourceType: "business_change_plan", TargetID: strings.TrimSpace(plan.PlanID), Payload: canonical, Preconditions: map[string]any{"plan_version": plan.PlanVersion, "draft_revision": plan.DraftRevision, "confirmation": strings.TrimSpace(confirmation)}})
}

func changePlanReceiptForError(err error) changePlanApplyReceipt {
	receipt := changePlanApplyReceipt{ErrorKind: apperror.KindOf(err), ErrorCode: ErrorCodeOf(err)}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		receipt.ErrorParams = appErr.ErrorParams()
	}
	return receipt
}

func (s *ChangePlanApplicationService) Apply(ctx context.Context, plan changeplanmodel.BusinessSystemChangePlan, confirmation string, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (BusinessChangePlanApplyResult, error) {
	if err := changePlanAuthorizeCommand(principal); err != nil {
		return BusinessChangePlanApplyResult{}, err
	}
	validation, err := s.Validate(ctx, plan, snapshot, graph, principal)
	if err != nil {
		s.auditBusinessChangePlanResult(ctx, plan, principal, "failed", ErrorCodeOf(err), nil)
		return BusinessChangePlanApplyResult{}, err
	}
	if !validation.ApplyAllowed {
		s.auditBusinessChangePlanResult(ctx, plan, principal, "failed", "backend.change_plan.apply_not_allowed", &validation)
		return BusinessChangePlanApplyResult{}, conflict("backend.change_plan.apply_not_allowed", "plan_id", plan.PlanID, "issue_count", fmt.Sprint(len(validation.Issues)))
	}
	if strings.TrimSpace(confirmation) != plan.PlanID {
		s.auditBusinessChangePlanResult(ctx, plan, principal, "failed", "backend.change_plan.confirmation_invalid", &validation)
		return BusinessChangePlanApplyResult{}, badRequest("backend.change_plan.confirmation_invalid", "expected", plan.PlanID)
	}
	mutations, audits, err := s.metadataChangePlanMutations(ctx, plan, principal)
	if err != nil {
		s.auditBusinessChangePlanResult(ctx, plan, principal, "failed", ErrorCodeOf(err), &validation)
		return BusinessChangePlanApplyResult{}, err
	}
	publication := &changeplanmodel.BusinessChangePlanPublication{WorkspaceID: principal.WorkspaceID, PlanID: plan.PlanID, ExpectedRevision: plan.DraftRevision, UpdatedBy: principal.UserID, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	definitions, err := s.metadata.ApplyDefinitionMutations(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "apply approved metadata definition mutations"), mutations, audits, publication)
	if err != nil {
		if businessChangePlanDraftConflict(err) {
			s.auditBusinessChangePlanResult(ctx, plan, principal, "failed", "backend.change_plan.draft_version_conflict", &validation)
			return BusinessChangePlanApplyResult{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", plan.PlanID)
		}
		s.auditBusinessChangePlanResult(ctx, plan, principal, "failed", ErrorCodeOf(wrapMetadataError(err)), &validation)
		return BusinessChangePlanApplyResult{}, wrapMetadataError(err)
	}
	schemaHash, err := s.runtime.ReloadApplicationSchema(ctx, principal)
	if err != nil {
		s.auditBusinessChangePlanResult(ctx, plan, principal, "failed", ErrorCodeOf(err), &validation)
		return BusinessChangePlanApplyResult{}, err
	}
	if plan.DraftRevision > 0 {
		finalized, published, err := s.repository.PublishDraft(ctx, principal.WorkspaceID, plan.PlanID, plan.DraftRevision+1, principal.UserID, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return BusinessChangePlanApplyResult{}, internalError("finalize domain change plan publication", err)
		}
		if !published || finalized.Status != "published" {
			return BusinessChangePlanApplyResult{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", plan.PlanID)
		}
	}
	s.auditBusinessChangePlanResult(ctx, plan, principal, "published", "", &validation)
	return BusinessChangePlanApplyResult{PlanID: plan.PlanID, Status: "applied", AppliedDefinitions: definitions, SchemaHash: schemaHash, Validation: validation}, nil
}

func (s *ChangePlanApplicationService) Validate(_ context.Context, plan changeplanmodel.BusinessSystemChangePlan, snapshot changeplancontract.SnapshotSource, graph changeplancontract.ReferenceGraphSource, principal principalmodel.Principal) (changeplanmodel.BusinessChangePlanValidation, error) {
	if err := changePlanAuthorizeQuery(principal); err != nil {
		return changeplanmodel.BusinessChangePlanValidation{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return changeplanmodel.BusinessChangePlanValidation{}, forbidden("auth.permission_denied")
	}
	snapshotValue, graphValue := snapshot.ChangePlanSnapshot(), graph.ChangePlanReferenceGraph()
	result := changeplanvalidation.ValidateBusinessSystemChangePlan(plan, snapshotValue, graphValue)
	for _, item := range plan.Items {
		result.Diffs = append(result.Diffs, changeplanprojection.BuildChangePlanBusinessDiff(item, snapshotValue, graphValue))
	}
	return result, nil
}

func (s *ChangePlanApplicationService) auditBusinessChangePlanResult(ctx context.Context, plan changeplanmodel.BusinessSystemChangePlan, principal principalmodel.Principal, result, errorCode string, validation *changeplanmodel.BusinessChangePlanValidation) {
	metadata := map[string]any{
		"result": result, "error_code": errorCode, "business_reason": plan.BusinessReason,
		"builder_task_id": plan.BuilderTaskID, "change_plan_id": plan.PlanID, "snapshot_hash": plan.SnapshotHash,
		"contract_version": plan.AuthoringContractVersion, "runtime_version": plan.RuntimeVersion,
		"frontend_manifest_version": plan.FrontendManifestVersion,
	}
	if validation != nil {
		metadata["validation_result"] = map[string]any{"valid": validation.Valid, "apply_allowed": validation.ApplyAllowed, "issues": validation.Issues}
	}
	event := buildAuditEvent("business_change_plan."+result, "business_change_plan", plan.PlanID, principal, "Business change plan "+result, nil, nil, metadata)
	_ = s.audit.InsertAuditEvent(ctx, event.WorkspaceID, event)
}

func (s *ChangePlanApplicationService) ExportPackage(ctx context.Context, planID string, expectedRevision int, principal principalmodel.Principal) (changeplanmodel.BusinessSystemPackage, error) {
	draft, err := s.Draft(ctx, planID, principal)
	if err != nil {
		return changeplanmodel.BusinessSystemPackage{}, err
	}
	if expectedRevision <= 0 || draft.Revision != expectedRevision || (draft.Status != "approved" && draft.Status != "published") {
		return changeplanmodel.BusinessSystemPackage{}, conflict("backend.change_plan.draft_version_conflict", "plan_id", strings.TrimSpace(planID), "expected_status", "approved|published")
	}
	var plan changeplanmodel.BusinessSystemChangePlan
	if err := json.Unmarshal(draft.Payload, &plan); err != nil {
		return changeplanmodel.BusinessSystemPackage{}, badRequest("backend.change_plan.draft_invalid")
	}
	result := changeplanmodel.BusinessSystemPackage{
		PackageVersion: changeplanmodel.BusinessSystemPackageVersion, SourcePlanID: draft.PlanID, SourceRevision: draft.Revision,
		RuntimeVersion: plan.RuntimeVersion, AuthoringContractVersion: plan.AuthoringContractVersion, AuthoringContractHash: plan.AuthoringContractHash,
		Resources: []changeplanmodel.BusinessSystemPackageResource{}, Dependencies: []changeplanmodel.BusinessSystemPackageDependency{},
		EmptyWorkspaceApplyPlan: changeplanmodel.BusinessSystemPackageApplyPlan{Target: "empty_workspace", RequiresSnapshotBinding: true, RequiresReferenceBinding: true, Operations: []changeplanmodel.BusinessSystemPackageApplyOperation{}},
		AcceptanceScenarios:     cloneChangePlanScenarios(plan.AcceptanceScenarios),
	}
	for _, item := range plan.Items {
		if item.Operation != "create" && item.Operation != "update" {
			continue
		}
		// The draft was decoded above, so every RawMessage nested in the plan is
		// already valid JSON and canonicalization cannot fail here.
		payload, hash, _ := canonicalPackagePayload(item.After)
		result.Resources = append(result.Resources, changeplanmodel.BusinessSystemPackageResource{ResourceType: item.ResourceType, ResourceKey: item.ResourceKey, ResourceHash: hash, Payload: payload})
		result.EmptyWorkspaceApplyPlan.Operations = append(result.EmptyWorkspaceApplyPlan.Operations, changeplanmodel.BusinessSystemPackageApplyOperation{Operation: "create", ResourceType: item.ResourceType, ResourceKey: item.ResourceKey, Payload: append(json.RawMessage(nil), payload...)})
		for _, dependency := range item.Dependencies {
			result.Dependencies = append(result.Dependencies, changeplanmodel.BusinessSystemPackageDependency{FromResourceType: item.ResourceType, FromResourceKey: item.ResourceKey, ToResourceType: dependency.ResourceType, ToResourceKey: dependency.ResourceKey, Reason: dependency.Reason})
		}
	}
	sort.Slice(result.Resources, func(i, j int) bool {
		return result.Resources[i].ResourceType+"\x00"+result.Resources[i].ResourceKey < result.Resources[j].ResourceType+"\x00"+result.Resources[j].ResourceKey
	})
	sort.Slice(result.EmptyWorkspaceApplyPlan.Operations, func(i, j int) bool {
		return result.EmptyWorkspaceApplyPlan.Operations[i].ResourceType+"\x00"+result.EmptyWorkspaceApplyPlan.Operations[i].ResourceKey < result.EmptyWorkspaceApplyPlan.Operations[j].ResourceType+"\x00"+result.EmptyWorkspaceApplyPlan.Operations[j].ResourceKey
	})
	sort.Slice(result.Dependencies, func(i, j int) bool {
		left := result.Dependencies[i].FromResourceType + "\x00" + result.Dependencies[i].FromResourceKey + "\x00" + result.Dependencies[i].ToResourceType + "\x00" + result.Dependencies[i].ToResourceKey
		right := result.Dependencies[j].FromResourceType + "\x00" + result.Dependencies[j].FromResourceKey + "\x00" + result.Dependencies[j].ToResourceType + "\x00" + result.Dependencies[j].ToResourceKey
		return left < right
	})
	raw, _ := json.Marshal(result)
	sum := sha256.Sum256(raw)
	result.PackageHash = hex.EncodeToString(sum[:])
	return result, nil
}

func canonicalPackagePayload(raw json.RawMessage) (json.RawMessage, string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, "", err
	}
	// Generic values decoded by encoding/json are always marshalable.
	canonical, _ := json.Marshal(value)
	sum := sha256.Sum256(canonical)
	return canonical, hex.EncodeToString(sum[:]), nil
}

func cloneChangePlanScenarios(values []changeplanmodel.BusinessAcceptanceScenario) []changeplanmodel.BusinessAcceptanceScenario {
	if len(values) == 0 {
		return []changeplanmodel.BusinessAcceptanceScenario{}
	}
	raw, _ := json.Marshal(values)
	var result []changeplanmodel.BusinessAcceptanceScenario
	_ = json.Unmarshal(raw, &result)
	return result
}
