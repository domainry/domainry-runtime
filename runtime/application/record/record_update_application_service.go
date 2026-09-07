package record

import (
	"context"
	"errors"
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type RecordUpdateDeniedObserver func(context.Context, string, string, principalmodel.Principal, error, string, map[string]any)

type RecordUpdateDependencies struct {
	Repository            recordrepository.RecordRepository
	MutationKernel        *recordmutation.MutationKernelApplicationService
	ObjectForAction       func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	CanAccess             func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	CanWrite              func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool
	CanAccessScope        func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record, bool) (bool, error)
	ScopeForAction        RecordMutationScopeResolver
	LoadTargetForAction   RecordMutationTargetLoader
	Denied                RecordUpdateDeniedObserver
	ValidatePipeline      func(context.Context, definitionmodel.ObjectSchema, string, map[string]any, principalmodel.Principal) error
	ApplyPipelineDefaults func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal, bool) error
	RunBefore             func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error
	ValidateRelations     func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error
	ValidatePolicies      func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error
	ApplySelfEffects      func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, principalmodel.Principal) (bool, error)
	ValidateUnique        func(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error
	ValidateDuplicate     func(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error
	AfterOutbox           func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []publicationmodel.Message
	UpdatedTriggers       func(string, map[string]any, map[string]any) []string
	PrepareWorkflow       func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error)
	ExecuteWorkflow       func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)
	BuildAudit            func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) auditmodel.AuditEvent
	ExecutionRuntime      *recordruntime.RecordMutationExecutionRuntime
	Now                   func() time.Time
}

// RecordUpdateApplicationService coordinates the complete Record update use case.
type RecordUpdateApplicationService struct {
	dependencies RecordUpdateDependencies
}

func NewRecordUpdateApplicationService(dependencies RecordUpdateDependencies) *RecordUpdateApplicationService {
	if dependencies.MutationKernel == nil {
		dependencies.MutationKernel = recordmutation.NewMutationKernelApplicationService(dependencies.Repository, nil)
	}
	return &RecordUpdateApplicationService{dependencies: dependencies}
}

func (s *RecordUpdateApplicationService) Update(ctx context.Context, objectKey, recordID string, patch map[string]any, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.update(ctx, objectKey, recordID, patch, nil, "", principal)
}

func (s *RecordUpdateApplicationService) UpdateIdempotent(ctx context.Context, objectKey, recordID string, patch map[string]any, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.update(ctx, objectKey, recordID, patch, nil, idempotencyKey, principal)
}

func (s *RecordUpdateApplicationService) UpdateLocalized(ctx context.Context, objectKey, recordID string, patch map[string]any, translations recordmodel.RecordTranslations, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.update(ctx, objectKey, recordID, patch, translations, "", principal)
}

func (s *RecordUpdateApplicationService) UpdateLocalizedIdempotent(ctx context.Context, objectKey, recordID string, patch map[string]any, translations recordmodel.RecordTranslations, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.update(ctx, objectKey, recordID, patch, translations, idempotencyKey, principal)
}

func (s *RecordUpdateApplicationService) update(ctx context.Context, objectKey, recordID string, patch map[string]any, translations recordmodel.RecordTranslations, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := ctx.Err(); err != nil {
		return recordmodel.Record{}, err
	}
	patch = recordvalidation.RecordCloneData(patch)
	authorizationPrincipal := recordEffectAuthorizationPrincipal(ctx, principal, objectKey, "update")
	object, err := s.dependencies.ObjectForAction(authorizationPrincipal, objectKey, "update")
	if err != nil {
		s.denied(ctx, objectKey, recordID, principal, err, "object_permission", patch)
		return recordmodel.Record{}, err
	}
	if err := recordpolicy.RecordValidateRuntimeOwnedCRUD(object, "update"); err != nil {
		s.denied(ctx, objectKey, recordID, principal, err, "runtime_owned", patch)
		return recordmodel.Record{}, err
	}
	localizedValues, err := recordmodel.RecordNormalizeTranslations(object, translations)
	if err != nil {
		return recordmodel.Record{}, recordUpdateError(apperror.KindBadRequest, "backend.record.localized_values_invalid", err, "detail", err.Error())
	}
	var claim recordmodel.RecordMutationClaimResult
	if strings.TrimSpace(idempotencyKey) != "" {
		if s.dependencies.ExecutionRuntime == nil {
			return recordmodel.Record{}, recordUpdateError(apperror.KindInternal, "backend.idempotency.receipt_unavailable", nil)
		}
		fingerprintInput := recordvalidation.RecordCloneData(patch)
		if len(localizedValues) > 0 {
			fingerprintInput["__localized_values"] = localizedValues
		}
		replay, acquired, found, claimErr := s.dependencies.ExecutionRuntime.BeginUpdate(ctx, objectKey, recordID, idempotencyKey, fingerprintInput, principal)
		if claimErr != nil {
			return recordmodel.Record{}, claimErr
		}
		if found {
			return recordpolicy.RecordFilterReadable(principal, object, replay), nil
		}
		claim = acquired
	}
	authorizationScope, err := resolveRecordMutationScope(s.dependencies.ScopeForAction, authorizationPrincipal, object, "update")
	if err != nil {
		s.denied(ctx, objectKey, recordID, principal, err, "data_scope", patch)
		return recordmodel.Record{}, err
	}
	record, found, err := loadRecordMutationTarget(ctx, s.dependencies.LoadTargetForAction, s.dependencies.Repository, principal.WorkspaceID, object, recordID, authorizationScope)
	if err != nil {
		operation := "get record"
		if s.dependencies.LoadTargetForAction != nil {
			operation = "get scoped mutation target"
		}
		return recordmodel.Record{}, recordUpdateError(apperror.KindInternal, "backend.internal", err, "operation", operation)
	}
	if !found {
		if s.dependencies.LoadTargetForAction == nil {
			return recordmodel.Record{}, recordUpdateError(apperror.KindNotFound, "backend.record.not_found", nil)
		}
		err := recordUpdateError(apperror.KindForbidden, "backend.record.outside_scope", nil)
		s.denied(ctx, objectKey, recordID, principal, err, "data_scope", patch)
		return recordmodel.Record{}, err
	}
	allowed, err := s.canAccessScope(ctx, authorizationPrincipal, object, record, false)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if !allowed {
		err := recordUpdateError(apperror.KindForbidden, "backend.record.outside_scope", nil)
		s.denied(ctx, objectKey, recordID, principal, err, "data_scope", patch)
		return recordmodel.Record{}, err
	}
	var auditMetadata map[string]any
	if strings.TrimSpace(claim.Execution.ID) != "" {
		auditMetadata = idempotency.AuditMetadata(claimAuditFacts(claim, "record.update", "succeeded"))
	}
	planned, err := s.planUpdate(ctx, objectKey, object, record, patch, localizedValues, auditMetadata, principal, authorizationPrincipal, authorizationScope)
	if err != nil {
		return recordmodel.Record{}, err
	}
	var receipt recordmutation.MutationReceiptCommit
	if strings.TrimSpace(claim.Execution.ID) != "" {
		receipt = func(ctx context.Context, canonical transactionmodel.RecordMutationCommit) error {
			return s.dependencies.ExecutionRuntime.Commit(ctx, claim, canonical)
		}
	}
	if err := s.dependencies.MutationKernel.CommitPlan(ctx, planned.plan, receipt); err != nil {
		return recordmodel.Record{}, recordUpdateCommitError(err)
	}
	if err := ctx.Err(); err != nil {
		return recordpolicy.RecordFilterReadable(principal, object, planned.record), err
	}
	if s.dependencies.ExecuteWorkflow != nil {
		s.dependencies.ExecuteWorkflow(ctx, planned.commit.WorkflowIntents, principal)
	}
	return recordpolicy.RecordFilterReadable(principal, object, planned.record), nil
}

func recordUpdateCommitError(err error) error {
	var applicationError *apperror.AppError
	if errors.As(err, &applicationError) {
		return err
	}
	var businessConflict *mutation.PolicyConflictError
	if errors.As(err, &businessConflict) {
		return recordUpdateError(apperror.KindConflict, businessConflict.Code, err, "object", businessConflict.Resource, "record_id", businessConflict.Identifier, "policy", businessConflict.Field)
	}
	if mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
		return recordUpdateError(apperror.KindConflict, "backend.record.version_conflict", err)
	}
	var conflict *mutation.MutationConflictError
	if errors.As(err, &conflict) {
		return recordUpdateError(apperror.KindConflict, mutation.StableConflictCode(conflict.Kind), err, "resource", conflict.Resource, "identifier", conflict.Identifier)
	}
	return recordUpdateError(apperror.KindInternal, "backend.internal", err, "operation", "commit record update")
}

type recordUpdatePlannedMutation struct {
	plan   transactionmodel.MutationPlan
	commit transactionmodel.RecordMutationCommit
	record recordmodel.Record
	before map[string]any
}

func (s *RecordUpdateApplicationService) planUpdate(ctx context.Context, objectKey string, object definitionmodel.ObjectSchema, record recordmodel.Record, patch map[string]any, localizedValues []recordmodel.RecordLocalizedValueMutation, auditMetadata map[string]any, principal, authorizationPrincipal principalmodel.Principal, authorizationScope *recordmodel.RecordScopeExpression) (recordUpdatePlannedMutation, error) {
	optimistic := transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: record.UpdatedAt}
	if expectedRaw, hasExpected := patch["expected_updated_at"]; hasExpected {
		delete(patch, "expected_updated_at")
		expected := strings.TrimSpace(fmt.Sprint(expectedRaw))
		if expected != "" && expected != record.UpdatedAt {
			return recordUpdatePlannedMutation{}, recordUpdateError(apperror.KindConflict, "backend.record.version_conflict", nil, "expected", expected, "actual", record.UpdatedAt)
		}
	}
	if expectedRaw, hasExpected := patch["expected_version"]; hasExpected {
		delete(patch, "expected_version")
		expected, valid := recordUpdateInt(expectedRaw)
		actual, actualValid := recordUpdateInt(record.Data["version"])
		if !valid {
			return recordUpdatePlannedMutation{}, recordUpdateError(apperror.KindBadRequest, "backend.record.expected_version_invalid", nil)
		}
		if !actualValid || expected != actual {
			return recordUpdatePlannedMutation{}, recordUpdateError(apperror.KindConflict, "backend.record.version_conflict", nil, "expected", fmt.Sprint(expected), "actual", fmt.Sprint(record.Data["version"]))
		}
		expectedVersion := int64(expected)
		optimistic.ExpectedVersion = &expectedVersion
	}
	inputPatch := recordvalidation.RecordCloneData(patch)
	normalized, err := recordvalidation.RecordNormalizeData(object, patch, true)
	if err != nil {
		return recordUpdatePlannedMutation{}, recordUpdateErrorFrom(apperror.KindBadRequest, err)
	}
	patch = normalized
	// Write authorization is the approved object/Action and row scope, not CLS.
	beforeData := recordvalidation.RecordCloneData(record.Data)
	nextData := recordvalidation.RecordCloneData(record.Data)
	for key, value := range patch {
		nextData[key] = value
	}
	_, pipelineStagePatched := patch["current_stage"]
	if object.Key == "pipeline_item" && pipelineStagePatched {
		nextData["last_transition_at"] = s.now().UTC().Format(time.RFC3339)
		currentVersion, ok := recordUpdateInt(beforeData["version"])
		if !ok {
			currentVersion = 1
		}
		nextData["version"] = currentVersion + 1
	}
	recordpolicy.RecordApplyFieldDefaults(object, nextData)
	if err := s.applyDerivedAndPipeline(ctx, object, record.ID, nextData, principal, pipelineStagePatched); err != nil {
		return recordUpdatePlannedMutation{}, err
	}
	if s.dependencies.RunBefore != nil {
		if err := s.dependencies.RunBefore(ctx, objectKey, "update", record.ID, inputPatch, beforeData, nextData, principal); err != nil {
			return recordUpdatePlannedMutation{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return recordUpdatePlannedMutation{}, err
	}
	if recordpolicy.RecordAutomationTransitionCandidate(beforeData, nextData) && s.dependencies.RunBefore != nil {
		if err := s.dependencies.RunBefore(ctx, objectKey, "transition", record.ID, inputPatch, beforeData, nextData, principal); err != nil {
			return recordUpdatePlannedMutation{}, err
		}
	}
	nextData, err = recordvalidation.RecordNormalizeData(object, nextData, false)
	if err != nil {
		return recordUpdatePlannedMutation{}, recordUpdateErrorFrom(apperror.KindBadRequest, err)
	}
	if err := s.validateCandidate(ctx, objectKey, object, record, beforeData, nextData, patch, principal, authorizationPrincipal, true); err != nil {
		return recordUpdatePlannedMutation{}, err
	}
	if s.dependencies.ApplySelfEffects != nil {
		changed, err := s.dependencies.ApplySelfEffects(ctx, object, beforeData, nextData, record.ID, principal)
		if err != nil {
			return recordUpdatePlannedMutation{}, err
		}
		if changed {
			recordpolicy.RecordApplyFieldDefaults(object, nextData)
			if err := s.applyDerivedAndPipeline(ctx, object, record.ID, nextData, principal, pipelineStagePatched); err != nil {
				return recordUpdatePlannedMutation{}, err
			}
			if err := s.validateCandidate(ctx, objectKey, object, record, beforeData, nextData, patch, principal, authorizationPrincipal, false); err != nil {
				return recordUpdatePlannedMutation{}, err
			}
		}
	}
	if s.dependencies.ValidateUnique != nil {
		if err := s.dependencies.ValidateUnique(ctx, principal.WorkspaceID, objectKey, object, record.ID, nextData); err != nil {
			return recordUpdatePlannedMutation{}, err
		}
	}
	if s.dependencies.ValidateDuplicate != nil {
		if err := s.dependencies.ValidateDuplicate(ctx, principal.WorkspaceID, object, record.ID, nextData); err != nil {
			return recordUpdatePlannedMutation{}, err
		}
	}
	record.Data = nextData
	record.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	record.UpdateBy = principal.UserID
	commit := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: record, Optimistic: optimistic, AuthorizationScope: authorizationScope, LocalizedValues: localizedValues}
	commit.Predicates = recordmutation.MutationPredicatesFromContext(ctx)
	if s.dependencies.AfterOutbox != nil {
		commit.Outbox = s.dependencies.AfterOutbox(objectKey, "update", beforeData, record, principal)
		if recordpolicy.RecordAutomationTransitionCandidate(beforeData, record.Data) {
			commit.Outbox = append(commit.Outbox, s.dependencies.AfterOutbox(objectKey, "transition", beforeData, record, principal)...)
		}
	}
	if s.dependencies.BuildAudit != nil {
		audit := s.dependencies.BuildAudit(ctx, "record_updated", objectKey, record.ID, principal, "Updated "+objectKey+" record", beforeData, record.Data, recordLocalizationAuditMetadata(auditMetadata, localizedValues))
		commit.Audit = &audit
	}
	if s.dependencies.UpdatedTriggers != nil && s.dependencies.PrepareWorkflow != nil {
		triggers := s.dependencies.UpdatedTriggers(objectKey, beforeData, record.Data)
		if invocation, ok := recordmutation.MutationInvocationFromContext(ctx); ok {
			triggers = append(triggers, invocation.WorkflowTriggers...)
		}
		seenTriggers := map[string]bool{}
		for _, trigger := range triggers {
			trigger = strings.TrimSpace(trigger)
			if trigger == "" || seenTriggers[trigger] {
				continue
			}
			seenTriggers[trigger] = true
			intents, err := s.dependencies.PrepareWorkflow(ctx, objectKey, record, beforeData, principal, trigger)
			if err != nil {
				return recordUpdatePlannedMutation{}, err
			}
			commit.WorkflowIntents = append(commit.WorkflowIntents, intents...)
		}
	}
	plan, err := s.dependencies.MutationKernel.Plan(ctx, principal, commit, beforeData)
	if err != nil {
		return recordUpdatePlannedMutation{}, recordMutationPlanApplicationError(err)
	}
	return recordUpdatePlannedMutation{plan: plan, commit: commit, record: record, before: beforeData}, nil
}

func (s *RecordUpdateApplicationService) applyDerivedAndPipeline(ctx context.Context, object definitionmodel.ObjectSchema, recordID string, data map[string]any, principal principalmodel.Principal, stagePatched bool) error {
	if s.dependencies.ValidatePipeline != nil {
		if err := s.dependencies.ValidatePipeline(ctx, object, recordID, data, principal); err != nil {
			return err
		}
	}
	if s.dependencies.ApplyPipelineDefaults != nil {
		if err := s.dependencies.ApplyPipelineDefaults(ctx, object, data, principal, stagePatched); err != nil {
			return err
		}
	}
	return nil
}

func (s *RecordUpdateApplicationService) validateCandidate(ctx context.Context, objectKey string, object definitionmodel.ObjectSchema, record recordmodel.Record, beforeData, nextData, patch map[string]any, principal, authorizationPrincipal principalmodel.Principal, includePolicies bool) error {
	if err := recordvalidation.RecordValidateDataWithPrev(object, nextData, beforeData, false); err != nil {
		return recordUpdateErrorFrom(apperror.KindBadRequest, err)
	}
	candidate := record
	candidate.Data = nextData
	allowed, err := s.canAccessScope(ctx, authorizationPrincipal, object, candidate, true)
	if err != nil {
		return err
	}
	if !allowed {
		err := recordUpdateError(apperror.KindForbidden, "backend.record.owner_write_denied", nil)
		s.denied(ctx, objectKey, record.ID, principal, err, "owner_scope", patch)
		return err
	}
	if s.dependencies.ValidateRelations != nil {
		if err := s.dependencies.ValidateRelations(ctx, object, changedRelationData(object, beforeData, nextData), principal); err != nil {
			return err
		}
	}
	if includePolicies && s.dependencies.ValidatePolicies != nil {
		if err := s.dependencies.ValidatePolicies(ctx, object, beforeData, nextData, record.ID, "update", principal); err != nil {
			return err
		}
	}
	return nil
}

func (s *RecordUpdateApplicationService) canAccessScope(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, write bool) (bool, error) {
	if s.dependencies.CanAccessScope != nil {
		return s.dependencies.CanAccessScope(ctx, principal, object, record, write)
	}
	if write {
		return s.dependencies.CanWrite == nil || s.dependencies.CanWrite(principal, object, recordpolicy.RecordDataWithOwnerFacts(record)), nil
	}
	return s.dependencies.CanAccess == nil || s.dependencies.CanAccess(principal, object, record), nil
}

func (s *RecordUpdateApplicationService) denied(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal, err error, reason string, patch map[string]any) {
	if s.dependencies.Denied != nil {
		s.dependencies.Denied(ctx, objectKey, recordID, principal, err, reason, patch)
	}
}
