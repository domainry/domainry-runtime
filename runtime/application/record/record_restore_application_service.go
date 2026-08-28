package record

import (
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

type RecordRestoreDependencies struct {
	Repository        recordrepository.RecordRepository
	MutationKernel    *recordmutation.MutationKernelApplicationService
	ObjectForAction   func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	CanAccess         func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	CanWrite          func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool
	ValidateRelations func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error
	ValidatePolicies  func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error
	ValidateUnique    func(context.Context, string, string, definitionmodel.ObjectSchema, string, map[string]any) error
	ValidateDuplicate func(context.Context, string, definitionmodel.ObjectSchema, string, map[string]any) error
	UpdatedTriggers   func(string, map[string]any, map[string]any) []string
	PrepareWorkflow   func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error)
	ExecuteWorkflow   func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)
	BuildAudit        func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) auditmodel.AuditEvent
}

// RecordRestoreApplicationService coordinates the complete Record restore use case.
type RecordRestoreApplicationService struct {
	dependencies RecordRestoreDependencies
}

func NewRecordRestoreApplicationService(dependencies RecordRestoreDependencies) *RecordRestoreApplicationService {
	if dependencies.MutationKernel == nil {
		dependencies.MutationKernel = recordmutation.NewMutationKernelApplicationService(dependencies.Repository, nil)
	}
	return &RecordRestoreApplicationService{dependencies: dependencies}
}

func (s *RecordRestoreApplicationService) Restore(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	plan, record, err := s.PlanRestoreMutation(ctx, objectKey, recordID, "", principal)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.dependencies.MutationKernel.CommitPlan(ctx, plan, nil); err != nil {
		var businessConflict *mutation.BusinessConflictError
		if errors.As(err, &businessConflict) {
			return recordmodel.Record{}, recordRestoreError(apperror.KindConflict, businessConflict.Code, err, "object", businessConflict.Resource, "record_id", businessConflict.Identifier, "policy", businessConflict.Field)
		}
		return recordmodel.Record{}, recordRestoreInternalError("commit restore record", err)
	}
	if s.dependencies.ExecuteWorkflow != nil {
		s.dependencies.ExecuteWorkflow(ctx, plan.CanonicalCommit().WorkflowIntents, principal)
	}
	return record, nil
}

// PlanRestoreMutation prepares a restore without committing it. When the
// caller omits expectedUpdatedAt, the revision observed during planning is
// used as the optimistic precondition.
func (s *RecordRestoreApplicationService) PlanRestoreMutation(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	object, err := s.dependencies.ObjectForAction(principal, objectKey, "update")
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if err := recordpolicy.RecordValidateSchedulerOperationalCRUD(object, "restore"); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if !recordpolicy.RecordUsesSoftDelete(object) {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordRestoreError(apperror.KindBadRequest, "backend.record.restore_unsupported", nil)
	}
	record, found, err := s.dependencies.Repository.GetRecord(ctx, principal.WorkspaceID, object, recordID)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordRestoreInternalError("get record", err)
	}
	if !found {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordRestoreError(apperror.KindNotFound, "backend.record.not_found", nil)
	}
	if s.dependencies.CanAccess != nil && !s.dependencies.CanAccess(principal, object, record) {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordRestoreError(apperror.KindForbidden, "backend.record.outside_scope", nil)
	}
	expectedUpdatedAt = strings.TrimSpace(expectedUpdatedAt)
	if expectedUpdatedAt != "" && expectedUpdatedAt != record.UpdatedAt {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordRestoreError(apperror.KindConflict, "backend.record.version_conflict", nil, "expected", expectedUpdatedAt, "actual", record.UpdatedAt)
	}
	planned, err := s.planRestore(ctx, objectKey, object, record, expectedUpdatedAt, principal)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	return planned.plan, recordpolicy.RecordFilterReadable(principal, object, planned.record), nil
}

type recordRestorePlannedMutation struct {
	plan   transactionmodel.MutationPlan
	commit transactionmodel.RecordMutationCommit
	record recordmodel.Record
	before map[string]any
}

func (s *RecordRestoreApplicationService) planRestore(ctx context.Context, objectKey string, object definitionmodel.ObjectSchema, record recordmodel.Record, expectedUpdatedAt string, principal principalmodel.Principal) (recordRestorePlannedMutation, error) {
	readRevision := record.UpdatedAt
	beforeData := recordvalidation.RecordCloneData(record.Data)
	if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(beforeData["status"])), "deleted") {
		return recordRestorePlannedMutation{}, recordRestoreError(apperror.KindBadRequest, "backend.record.not_deleted", nil)
	}
	nextData := recordvalidation.RecordCloneData(record.Data)
	nextData["status"] = recordpolicy.RecordRestoreStatus(object)
	nextData["deleted_at"], nextData["deleted_by"] = "", ""
	if recordvalidation.RecordFieldExists(object, "version") {
		currentVersion, ok := restoreInt(beforeData["version"])
		if !ok {
			currentVersion = 1
		}
		nextData["version"] = currentVersion + 1
	}
	var err error
	nextData, err = recordvalidation.RecordNormalizeData(object, nextData, false)
	if err != nil {
		return recordRestorePlannedMutation{}, recordRestoreErrorFrom(apperror.KindBadRequest, err)
	}
	restorePatch := map[string]any{"status": nextData["status"], "deleted_at": nextData["deleted_at"], "deleted_by": nextData["deleted_by"]}
	if recordvalidation.RecordFieldExists(object, "version") {
		restorePatch["version"] = nextData["version"]
	}
	if err := recordpolicy.RecordValidateWritableFields(principal, object, restorePatch); err != nil {
		return recordRestorePlannedMutation{}, recordRestoreErrorFrom(apperror.KindForbidden, err)
	}
	if err := recordvalidation.RecordValidateDataWithPrev(object, nextData, beforeData, false); err != nil {
		return recordRestorePlannedMutation{}, recordRestoreErrorFrom(apperror.KindBadRequest, err)
	}
	if s.dependencies.ValidateRelations != nil {
		if err := s.dependencies.ValidateRelations(ctx, object, nextData, principal); err != nil {
			return recordRestorePlannedMutation{}, err
		}
	}
	if s.dependencies.CanWrite != nil && !s.dependencies.CanWrite(principal, object, nextData) {
		return recordRestorePlannedMutation{}, recordRestoreError(apperror.KindForbidden, "backend.record.owner_write_denied", nil)
	}
	if s.dependencies.ValidatePolicies != nil {
		if err := s.dependencies.ValidatePolicies(ctx, object, beforeData, nextData, record.ID, "restore", principal); err != nil {
			return recordRestorePlannedMutation{}, err
		}
	}
	if s.dependencies.ValidateUnique != nil {
		if err := s.dependencies.ValidateUnique(ctx, principal.WorkspaceID, objectKey, object, record.ID, nextData); err != nil {
			return recordRestorePlannedMutation{}, err
		}
	}
	if s.dependencies.ValidateDuplicate != nil {
		if err := s.dependencies.ValidateDuplicate(ctx, principal.WorkspaceID, object, record.ID, nextData); err != nil {
			return recordRestorePlannedMutation{}, err
		}
	}
	record.Data = nextData
	record.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if expectedUpdatedAt == "" {
		expectedUpdatedAt = readRevision
	}
	commit := transactionmodel.RecordMutationCommit{Operation: "restore", Object: object, Record: record, Optimistic: transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: expectedUpdatedAt}}
	if s.dependencies.BuildAudit != nil {
		audit := s.dependencies.BuildAudit(ctx, "record_restored", objectKey, record.ID, principal, "Restored "+objectKey+" record", beforeData, record.Data, map[string]any{"soft_delete": true})
		commit.Audit = &audit
	}
	if s.dependencies.UpdatedTriggers != nil && s.dependencies.PrepareWorkflow != nil {
		for _, trigger := range s.dependencies.UpdatedTriggers(objectKey, beforeData, record.Data) {
			intents, err := s.dependencies.PrepareWorkflow(ctx, objectKey, record, beforeData, principal, trigger)
			if err != nil {
				return recordRestorePlannedMutation{}, err
			}
			commit.WorkflowIntents = append(commit.WorkflowIntents, intents...)
		}
	}
	plan, err := s.dependencies.MutationKernel.Plan(ctx, principal, commit, beforeData)
	if err != nil {
		return recordRestorePlannedMutation{}, recordMutationPlanApplicationError(err)
	}
	return recordRestorePlannedMutation{plan: plan, commit: commit, record: record, before: beforeData}, nil
}

func recordRestoreErrorFrom(kind apperror.ErrorKind, err error) error {
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

func recordRestoreInternalError(operation string, err error) error {
	return recordRestoreError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func recordRestoreError(kind apperror.ErrorKind, code string, err error, params ...string) error {
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

func restoreInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}
