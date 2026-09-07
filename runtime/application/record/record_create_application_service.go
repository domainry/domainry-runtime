package record

import (
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"errors"
	"fmt"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/mutation"
)

type RecordCreateDependencies struct {
	Repository            recordrepository.RecordRepository
	MutationKernel        *recordmutation.MutationKernelApplicationService
	ObjectForAction       func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	CanWrite              func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool
	CanWriteCandidate     func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error)
	ValidatePipeline      func(context.Context, definitionmodel.ObjectSchema, string, map[string]any, principalmodel.Principal) error
	ApplyPipelineDefaults func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal, bool) error
	FindReplay            func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) (recordmodel.Record, bool, error)
	RunBefore             func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error
	ValidateRelations     func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error
	ValidatePolicies      func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error
	ValidateUnique        func(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error
	ValidateDuplicate     func(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error
	AfterOutbox           func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []publicationmodel.Message
	PrepareWorkflow       func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error)
	ExecuteWorkflow       func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)
	Audit                 func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	BuildAudit            func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) auditmodel.AuditEvent
	NewRecordID           func(string) string
	Now                   func() time.Time
	ExecutionRuntime      *recordruntime.RecordMutationExecutionRuntime
}

// RecordCreateApplicationService coordinates the complete Record create use case.
type RecordCreateApplicationService struct {
	dependencies RecordCreateDependencies
}

func NewRecordCreateApplicationService(dependencies RecordCreateDependencies) *RecordCreateApplicationService {
	if dependencies.MutationKernel == nil {
		dependencies.MutationKernel = recordmutation.NewMutationKernelApplicationService(dependencies.Repository, nil)
	}
	return &RecordCreateApplicationService{dependencies: dependencies}
}

func (s *RecordCreateApplicationService) Create(ctx context.Context, objectKey string, data map[string]any, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.create(ctx, objectKey, data, nil, "", nil, principal)
}

func (s *RecordCreateApplicationService) CreateIdempotent(ctx context.Context, objectKey string, data map[string]any, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.create(ctx, objectKey, data, nil, idempotencyKey, nil, principal)
}

func (s *RecordCreateApplicationService) CreateClaimed(ctx context.Context, objectKey string, data map[string]any, claim recordmodel.RecordMutationClaimResult, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.create(ctx, objectKey, data, nil, "", &claim, principal)
}

func (s *RecordCreateApplicationService) CreateClaimedLocalized(ctx context.Context, objectKey string, data map[string]any, translations recordmodel.RecordTranslations, claim recordmodel.RecordMutationClaimResult, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.create(ctx, objectKey, data, translations, "", &claim, principal)
}

func (s *RecordCreateApplicationService) PlanCreateMutation(ctx context.Context, objectKey string, data map[string]any, recordID string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	authorizationPrincipal := recordEffectAuthorizationPrincipal(ctx, principal, objectKey, "create")
	object, err := s.dependencies.ObjectForAction(authorizationPrincipal, objectKey, "create")
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if err := recordpolicy.RecordValidateRuntimeOwnedCRUD(object, "create"); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if data == nil {
		data = map[string]any{}
	}
	input := recordvalidation.RecordCloneData(data)
	if strings.TrimSpace(recordID) == "" {
		recordID = s.newRecordID(objectKey)
	}
	planned, err := s.planCreate(ctx, objectKey, object, data, input, recordID, nil, recordmodel.RecordMutationClaimResult{}, principal, authorizationPrincipal)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if planned.replayed {
		return transactionmodel.MutationPlan{}, planned.record, recordCreateError(apperror.KindConflict, "backend.record.create_replayed", nil)
	}
	return planned.plan, planned.record, nil
}

func (s *RecordCreateApplicationService) create(ctx context.Context, objectKey string, data map[string]any, translations recordmodel.RecordTranslations, idempotencyKey string, preclaimed *recordmodel.RecordMutationClaimResult, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if err := ctx.Err(); err != nil {
		return recordmodel.Record{}, err
	}
	object, err := s.dependencies.ObjectForAction(principal, objectKey, "create")
	if err != nil {
		return recordmodel.Record{}, err
	}
	if err := recordpolicy.RecordValidateRuntimeOwnedCRUD(object, "create"); err != nil {
		return recordmodel.Record{}, err
	}
	localizedValues, err := recordmodel.RecordNormalizeTranslations(object, translations)
	if err != nil {
		return recordmodel.Record{}, recordCreateError(apperror.KindBadRequest, "backend.record.localized_values_invalid", err, "detail", err.Error())
	}
	if data == nil {
		data = map[string]any{}
	}
	inputData := recordvalidation.RecordCloneData(data)
	var claim recordmodel.RecordMutationClaimResult
	if preclaimed != nil {
		claim = *preclaimed
	} else if strings.TrimSpace(idempotencyKey) != "" {
		if s.dependencies.ExecutionRuntime == nil {
			return recordmodel.Record{}, recordCreateError(apperror.KindInternal, "backend.idempotency.receipt_unavailable", nil)
		}
		replay, acquired, found, err := s.dependencies.ExecutionRuntime.BeginCreate(ctx, objectKey, idempotencyKey, inputData, principal)
		if err != nil {
			if strings.TrimSpace(acquired.Execution.ID) != "" {
				s.auditIdempotency(ctx, "record_create_idempotency_"+string(acquired.Decision), objectKey, "", principal, claimAuditFacts(acquired, "record.create", string(acquired.Decision)))
			}
			return recordmodel.Record{}, err
		}
		if found {
			s.auditIdempotency(ctx, "record_create_idempotent_replayed", objectKey, replay.ID, principal, claimAuditFacts(acquired, "record.create", "replayed"))
			return recordpolicy.RecordFilterReadable(principal, object, replay), nil
		}
		claim = acquired
	}
	planned, err := s.planCreate(ctx, objectKey, object, data, inputData, s.newRecordID(objectKey), localizedValues, claim, principal, principal)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if planned.replayed {
		return recordpolicy.RecordFilterReadable(principal, object, planned.record), nil
	}
	var receipt recordmutation.MutationReceiptCommit
	if strings.TrimSpace(claim.Execution.ID) != "" {
		receipt = func(ctx context.Context, canonical transactionmodel.RecordMutationCommit) error {
			return s.dependencies.ExecutionRuntime.Commit(ctx, claim, canonical)
		}
	}
	if err := s.dependencies.MutationKernel.CommitPlan(ctx, planned.plan, receipt); err != nil {
		return recordmodel.Record{}, recordCreateCommitError(err)
	}
	if strings.TrimSpace(claim.Execution.ID) != "" {
		logging.LogIdempotency(ctx, claimAuditFacts(claim, "record.create", "succeeded"), principal.RequestID)
	}
	if err := ctx.Err(); err != nil {
		return recordpolicy.RecordFilterReadable(principal, object, planned.record), err
	}
	if s.dependencies.ExecuteWorkflow != nil {
		s.dependencies.ExecuteWorkflow(ctx, planned.commit.WorkflowIntents, principal)
	}
	return planned.record, nil
}

type recordCreatePlannedMutation struct {
	plan     transactionmodel.MutationPlan
	commit   transactionmodel.RecordMutationCommit
	record   recordmodel.Record
	replayed bool
}

func (s *RecordCreateApplicationService) planCreate(ctx context.Context, objectKey string, object definitionmodel.ObjectSchema, data, inputData map[string]any, recordID string, localizedValues []recordmodel.RecordLocalizedValueMutation, claim recordmodel.RecordMutationClaimResult, principal, authorizationPrincipal principalmodel.Principal) (recordCreatePlannedMutation, error) {
	recordpolicy.RecordApplyFieldDefaults(object, data)
	recordpolicy.RecordApplyAutoCodeDefaults(object, data, recordID)
	normalized, err := recordvalidation.RecordNormalizeData(object, data, false)
	if err != nil {
		return recordCreatePlannedMutation{}, recordCreateErrorFrom(apperror.KindBadRequest, err)
	}
	data = normalized
	if s.dependencies.ValidatePipeline != nil {
		if err := s.dependencies.ValidatePipeline(ctx, object, "", data, principal); err != nil {
			return recordCreatePlannedMutation{}, err
		}
	}
	if s.dependencies.ApplyPipelineDefaults != nil {
		if err := s.dependencies.ApplyPipelineDefaults(ctx, object, data, principal, false); err != nil {
			return recordCreatePlannedMutation{}, err
		}
	}
	candidate := recordmodel.Record{ID: recordID, Data: data}
	recordpolicy.RecordApplyOwnerDefault(&candidate, principal)
	if invocation, ok := recordmutation.MutationInvocationFromContext(ctx); ok && invocation.Source == transactionmodel.MutationSourceAction {
		if targetOrganizationID := strings.TrimSpace(invocation.TargetOrganizationID); targetOrganizationID != "" {
			candidate.OwnerOrgID = targetOrganizationID
		}
	}
	if s.dependencies.CanWriteCandidate != nil {
		allowed, authorizeErr := s.dependencies.CanWriteCandidate(ctx, authorizationPrincipal, object, candidate)
		if authorizeErr != nil {
			return recordCreatePlannedMutation{}, authorizeErr
		}
		if !allowed {
			return recordCreatePlannedMutation{}, recordCreateError(apperror.KindForbidden, "backend.record.owner_write_denied", nil)
		}
	} else if s.dependencies.CanWrite != nil && !s.dependencies.CanWrite(authorizationPrincipal, object, recordpolicy.RecordDataWithOwnerFacts(candidate)) {
		return recordCreatePlannedMutation{}, recordCreateError(apperror.KindForbidden, "backend.record.owner_write_denied", nil)
	}
	// Object/Action and row scope authorize this write; field permissions only
	// affect read-side projection. Schema and business validation follow below.
	if s.dependencies.FindReplay != nil {
		replay, found, err := s.dependencies.FindReplay(ctx, object, inputData, principal)
		if err != nil {
			return recordCreatePlannedMutation{}, err
		}
		if found {
			if s.dependencies.Audit != nil {
				s.dependencies.Audit(ctx, "automation_create_idempotent_replay", objectKey, replay.ID, principal, "Replayed idempotent create for "+objectKey, nil, nil, idempotency.MergeAuditMetadata(map[string]any{"operation": "create"}, idempotency.AuditFacts{Scope: "record.create", Status: "replayed"}))
			}
			return recordCreatePlannedMutation{record: replay, replayed: true}, nil
		}
	}
	if s.dependencies.RunBefore != nil {
		if err := s.dependencies.RunBefore(ctx, objectKey, "create", recordID, inputData, nil, data, principal); err != nil {
			return recordCreatePlannedMutation{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return recordCreatePlannedMutation{}, err
	}
	data, err = recordvalidation.RecordNormalizeData(object, data, false)
	if err != nil {
		return recordCreatePlannedMutation{}, recordCreateErrorFrom(apperror.KindBadRequest, err)
	}
	if err := recordvalidation.RecordValidateData(object, data, false); err != nil {
		return recordCreatePlannedMutation{}, recordCreateErrorFrom(apperror.KindBadRequest, err)
	}
	if s.dependencies.ValidateRelations != nil {
		if err := s.dependencies.ValidateRelations(ctx, object, data, principal); err != nil {
			return recordCreatePlannedMutation{}, err
		}
	}
	if s.dependencies.ValidatePolicies != nil {
		if err := s.dependencies.ValidatePolicies(ctx, object, nil, data, "", "create", principal); err != nil {
			return recordCreatePlannedMutation{}, err
		}
	}
	if s.dependencies.ValidateUnique != nil {
		if err := s.dependencies.ValidateUnique(ctx, principal.WorkspaceID, objectKey, object, "", data); err != nil {
			return recordCreatePlannedMutation{}, err
		}
	}
	if s.dependencies.ValidateDuplicate != nil {
		if err := s.dependencies.ValidateDuplicate(ctx, principal.WorkspaceID, object, "", data); err != nil {
			return recordCreatePlannedMutation{}, err
		}
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	record := recordmodel.Record{
		WorkspaceID: principal.WorkspaceID,
		ID:          recordID,
		Data:        recordvalidation.RecordCloneData(data),
		CreatedAt:   now,
		UpdatedAt:   now,
		CreateBy:    principal.UserID,
		UpdateBy:    principal.UserID,
	}
	// Persist the exact ownership candidate that was authorized above. Action
	// creates may target an authorized store different from the actor's own
	// Organization; re-defaulting from the principal here would silently move
	// the canonical commit back to the actor Organization.
	record.OwnerUserID = candidate.OwnerUserID
	record.OwnerOrgID = candidate.OwnerOrgID
	commit := transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: record, LocalizedValues: localizedValues}
	if s.dependencies.BuildAudit != nil {
		var metadata map[string]any
		if strings.TrimSpace(claim.Execution.ID) != "" {
			metadata = idempotency.AuditMetadata(claimAuditFacts(claim, "record.create", "succeeded"))
		}
		metadata = recordLocalizationAuditMetadata(metadata, localizedValues)
		audit := s.dependencies.BuildAudit(ctx, "record_created", objectKey, record.ID, principal, "Created "+objectKey+" record", nil, record.Data, metadata)
		commit.Audit = &audit
	}
	if s.dependencies.AfterOutbox != nil {
		commit.Outbox = s.dependencies.AfterOutbox(objectKey, "create", nil, record, principal)
	}
	if s.dependencies.PrepareWorkflow != nil {
		intents, err := s.dependencies.PrepareWorkflow(ctx, objectKey, record, nil, principal, "record_created:"+objectKey)
		if err != nil {
			return recordCreatePlannedMutation{}, err
		}
		commit.WorkflowIntents = append(commit.WorkflowIntents, intents...)
	}
	plan, err := s.dependencies.MutationKernel.Plan(ctx, principal, commit, nil)
	if err != nil {
		return recordCreatePlannedMutation{}, recordMutationPlanApplicationError(err)
	}
	return recordCreatePlannedMutation{plan: plan, commit: commit, record: record}, nil
}

func recordCreateCommitError(err error) error {
	var applicationError *apperror.AppError
	if errors.As(err, &applicationError) {
		return err
	}
	var businessConflict *mutation.PolicyConflictError
	if errors.As(err, &businessConflict) {
		return recordCreateError(apperror.KindConflict, businessConflict.Code, err, "object", businessConflict.Resource, "record_id", businessConflict.Identifier, "policy", businessConflict.Field)
	}
	var conflict *mutation.MutationConflictError
	if errors.As(err, &conflict) {
		return recordCreateError(apperror.KindConflict, mutation.StableConflictCode(conflict.Kind), err, "resource", conflict.Resource, "identifier", conflict.Identifier)
	}
	return recordCreateError(apperror.KindInternal, "backend.internal", err, "operation", "commit record create")
}

func (s *RecordCreateApplicationService) auditIdempotency(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, facts idempotency.AuditFacts) {
	logging.LogIdempotency(ctx, facts, principal.RequestID)
	if s.dependencies.Audit != nil {
		s.dependencies.Audit(ctx, event, objectKey, recordID, principal, "Observed idempotent record create", nil, nil, idempotency.AuditMetadata(facts))
	}
}

func claimAuditFacts(claim recordmodel.RecordMutationClaimResult, scope, status string) idempotency.AuditFacts {
	return idempotency.AuditFacts{
		WorkspaceID: claim.Execution.WorkspaceID, Scope: scope, Key: claim.Execution.IdempotencyKey,
		RequestFingerprint: claim.Execution.RequestFingerprint, Status: status, FencingToken: claim.Execution.FencingToken,
	}
}

func (s *RecordCreateApplicationService) newRecordID(objectKey string) string {
	if s.dependencies.NewRecordID != nil {
		return s.dependencies.NewRecordID(objectKey)
	}
	return fmt.Sprintf("%s_%d", objectKey, time.Now().UnixNano())
}

func (s *RecordCreateApplicationService) now() time.Time {
	if s.dependencies.Now != nil {
		return s.dependencies.Now()
	}
	return time.Now()
}

func recordCreateErrorFrom(kind apperror.ErrorKind, err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return err
	}
	var coded interface {
		ErrorCode() string
		ErrorParams() map[string]string
	}
	if errors.As(err, &coded) && strings.TrimSpace(coded.ErrorCode()) != "" {
		return &apperror.AppError{Kind: kind, Code: strings.TrimSpace(coded.ErrorCode()), Params: coded.ErrorParams(), Err: err}
	}
	code := "backend.bad_request"
	if kind == apperror.KindForbidden {
		code = "backend.forbidden"
	}
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}

func recordCreateError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}
