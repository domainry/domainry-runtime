package record

import (
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"encoding/json"
	"errors"
	"fmt"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"

	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type RecordDeleteDependencies struct {
	Repository          recordrepository.RecordRepository
	MutationKernel      *recordmutation.MutationKernelApplicationService
	Relations           *recordservice.RecordDeleteRelationDomainService
	ObjectForAction     func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	CanAccess           func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	CanAccessScope      func(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record, bool) (bool, error)
	CanWrite            func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool
	RunBefore           func(context.Context, string, string, string, map[string]any, map[string]any, map[string]any, principalmodel.Principal) error
	ValidatePolicies    func(context.Context, definitionmodel.ObjectSchema, map[string]any, map[string]any, string, string, principalmodel.Principal) error
	AfterOutbox         func(string, string, map[string]any, recordmodel.Record, principalmodel.Principal) []integrationmodel.IntegrationOutboxMessage
	UpdatedTriggers     func(string, map[string]any, map[string]any) []string
	PrepareWorkflow     func(context.Context, string, recordmodel.Record, map[string]any, principalmodel.Principal, string) ([]workflowmodel.WorkflowExecution, error)
	ExecuteWorkflow     func(context.Context, []workflowmodel.WorkflowExecution, principalmodel.Principal)
	PlanUpdateReference func(context.Context, recordservice.RecordDeleteReference, principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error)
	BuildAudit          func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) auditmodel.AuditEvent
	Now                 func() time.Time
}

// RecordDeleteApplicationService coordinates the complete Record delete use case.
type RecordDeleteApplicationService struct {
	dependencies RecordDeleteDependencies
}

func NewRecordDeleteApplicationService(dependencies RecordDeleteDependencies) *RecordDeleteApplicationService {
	if dependencies.MutationKernel == nil {
		dependencies.MutationKernel = recordmutation.NewMutationKernelApplicationService(dependencies.Repository, nil)
	}
	return &RecordDeleteApplicationService{dependencies: dependencies}
}

func (s *RecordDeleteApplicationService) Delete(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) error {
	return s.DeleteExpected(ctx, objectKey, recordID, "", principal)
}

func (s *RecordDeleteApplicationService) DeleteExpected(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, principal principalmodel.Principal) error {
	group, err := s.planDeleteMutation(ctx, objectKey, recordID, expectedUpdatedAt, principal)
	if err != nil {
		return err
	}
	var commitErr error
	if len(group.plans) == 1 {
		commitErr = s.dependencies.MutationKernel.CommitPlan(ctx, group.plans[0], nil)
	} else {
		commitErr = s.dependencies.MutationKernel.CommitBatch(ctx, group.plans, nil)
	}
	if commitErr != nil {
		return recordDeleteInternalError("commit delete mutation batch", commitErr)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, effect := range group.effects {
		if s.dependencies.ExecuteWorkflow != nil {
			s.dependencies.ExecuteWorkflow(ctx, effect.commit.WorkflowIntents, principal)
		}
	}
	return nil
}

// PlanDeleteMutation prepares the complete delete mutation batch, including
// relation set-null and cascade work, without committing it. Callers own the
// atomic commit boundary for the returned plans.
func (s *RecordDeleteApplicationService) PlanDeleteMutation(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, principal principalmodel.Principal) ([]transactionmodel.MutationPlan, error) {
	group, err := s.planDeleteMutation(ctx, objectKey, recordID, expectedUpdatedAt, principal)
	if err != nil {
		return nil, err
	}
	return append([]transactionmodel.MutationPlan(nil), group.plans...), nil
}

func (s *RecordDeleteApplicationService) planDeleteMutation(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, principal principalmodel.Principal) (*recordDeletePlanGroup, error) {
	group := &recordDeletePlanGroup{}
	if err := s.planDelete(ctx, objectKey, recordID, strings.TrimSpace(expectedUpdatedAt), principal, map[string]bool{}, false, group); err != nil {
		return nil, err
	}
	return group, nil
}

type recordDeletePlanGroup struct {
	plans   []transactionmodel.MutationPlan
	effects []recordDeletePlannedEffect
}

type recordDeletePlannedEffect struct {
	commit transactionmodel.RecordMutationCommit
}

func (s *RecordDeleteApplicationService) planDelete(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, principal principalmodel.Principal, visited map[string]bool, skipBeforeAutomation bool, group *recordDeletePlanGroup) error {
	if err := recordAuthorizeCommand(principal); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	visitKey := objectKey + ":" + recordID
	if visited[visitKey] {
		return nil
	}
	visited[visitKey] = true
	object, err := s.dependencies.ObjectForAction(principal, objectKey, "delete")
	if err != nil {
		return err
	}
	if err := recordpolicy.RecordValidateSchedulerOperationalCRUD(object, "delete"); err != nil {
		return err
	}
	record, found, err := s.dependencies.Repository.GetRecord(ctx, principal.WorkspaceID, object, recordID)
	if err != nil {
		return recordDeleteInternalError("get record", err)
	}
	if !found {
		return recordDeleteError(apperror.KindNotFound, "backend.record.not_found", nil)
	}
	allowed, err := s.canAccessScope(ctx, principal, object, record)
	if err != nil {
		return err
	}
	if !allowed {
		return recordDeleteError(apperror.KindForbidden, "backend.record.outside_scope", nil)
	}
	if expectedUpdatedAt != "" && expectedUpdatedAt != record.UpdatedAt {
		return recordDeleteError(apperror.KindConflict, "backend.record.version_conflict", nil, "expected", expectedUpdatedAt, "actual", record.UpdatedAt)
	}
	beforeData := recordvalidation.RecordCloneData(record.Data)
	if recordpolicy.RecordRequiresSoftDelete(object) && !recordpolicy.RecordUsesSoftDelete(object) {
		return recordDeleteError(apperror.KindInternal, "backend.record.lifecycle_soft_delete_fields_required", nil, "object", object.Key)
	}
	if !skipBeforeAutomation && s.dependencies.RunBefore != nil {
		if err := s.dependencies.RunBefore(ctx, objectKey, "delete", recordID, nil, beforeData, recordvalidation.RecordCloneData(beforeData), principal); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if recordpolicy.RecordUsesSoftDelete(object) {
		return s.planSoftDelete(ctx, objectKey, recordID, expectedUpdatedAt, object, record, beforeData, principal, group)
	}
	return s.planHardDelete(ctx, objectKey, recordID, expectedUpdatedAt, object, record, beforeData, principal, visited, group)
}

func (s *RecordDeleteApplicationService) canAccessScope(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) (bool, error) {
	if s.dependencies.CanAccessScope != nil {
		return s.dependencies.CanAccessScope(ctx, principal, object, record, false)
	}
	return s.dependencies.CanAccess == nil || s.dependencies.CanAccess(principal, object, record), nil
}

func (s *RecordDeleteApplicationService) planSoftDelete(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, object definitionmodel.ObjectSchema, record recordmodel.Record, beforeData map[string]any, principal principalmodel.Principal, group *recordDeletePlanGroup) error {
	readRevision := record.UpdatedAt
	if fmt.Sprint(beforeData["status"]) == "deleted" {
		return recordDeleteError(apperror.KindBadRequest, "backend.record.already_deleted", nil)
	}
	nextData := recordvalidation.RecordCloneData(record.Data)
	nextData["status"] = "deleted"
	nextData["deleted_at"] = s.now().UTC().Format(time.RFC3339)
	nextData["deleted_by"] = principal.UserID
	if recordvalidation.RecordFieldExists(object, "version") {
		version, ok := recordDeleteInt(beforeData["version"])
		if !ok {
			version = 1
		}
		nextData["version"] = version + 1
	}
	nextData, err := recordvalidation.RecordNormalizeData(object, nextData, false)
	if err != nil {
		return recordDeleteErrorFrom(apperror.KindBadRequest, err)
	}
	patch := map[string]any{"status": nextData["status"], "deleted_at": nextData["deleted_at"], "deleted_by": nextData["deleted_by"]}
	if recordvalidation.RecordFieldExists(object, "version") {
		patch["version"] = nextData["version"]
	}
	if err := recordpolicy.RecordValidateWritableFields(principal, object, patch); err != nil {
		return recordDeleteErrorFrom(apperror.KindForbidden, err)
	}
	if err := recordvalidation.RecordValidateDataWithPrev(object, nextData, beforeData, false); err != nil {
		return recordDeleteErrorFrom(apperror.KindBadRequest, err)
	}
	if s.dependencies.CanWrite != nil && !s.dependencies.CanWrite(principal, object, nextData) {
		return recordDeleteError(apperror.KindForbidden, "backend.record.owner_write_denied", nil)
	}
	if s.dependencies.ValidatePolicies != nil {
		if err := s.dependencies.ValidatePolicies(ctx, object, beforeData, nextData, recordID, "delete", principal); err != nil {
			return err
		}
	}
	record.Data = nextData
	record.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	record.Deleted = true
	record.UpdateBy = principal.UserID
	if expectedUpdatedAt == "" {
		expectedUpdatedAt = readRevision
	}
	commit := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: record, Optimistic: transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: expectedUpdatedAt}}
	if s.dependencies.BuildAudit != nil {
		audit := s.dependencies.BuildAudit(ctx, "record_deleted", objectKey, recordID, principal, "Soft-deleted "+objectKey+" record", beforeData, record.Data, map[string]any{"soft_delete": true})
		commit.Audit = &audit
	}
	if s.dependencies.AfterOutbox != nil {
		commit.Outbox = s.dependencies.AfterOutbox(objectKey, "delete", beforeData, record, principal)
	}
	if s.dependencies.UpdatedTriggers != nil && s.dependencies.PrepareWorkflow != nil {
		for _, trigger := range s.dependencies.UpdatedTriggers(objectKey, beforeData, record.Data) {
			intents, err := s.dependencies.PrepareWorkflow(ctx, objectKey, record, beforeData, principal, trigger)
			if err != nil {
				return err
			}
			commit.WorkflowIntents = append(commit.WorkflowIntents, intents...)
		}
	}
	plan, err := s.dependencies.MutationKernel.Plan(ctx, principal, commit, beforeData)
	if err != nil {
		return recordMutationPlanApplicationError(err)
	}
	group.plans = append(group.plans, plan)
	group.effects = append(group.effects, recordDeletePlannedEffect{commit: commit})
	return nil
}

func (s *RecordDeleteApplicationService) planHardDelete(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, object definitionmodel.ObjectSchema, record recordmodel.Record, beforeData map[string]any, principal principalmodel.Principal, visited map[string]bool, group *recordDeletePlanGroup) error {
	readRevision := record.UpdatedAt
	if s.dependencies.Relations != nil {
		if err := s.dependencies.Relations.Apply(ctx, principal.WorkspaceID, objectKey, recordID, recordservice.RecordDeleteRelationCallbacks{
			SetNull: func(ctx context.Context, reference recordservice.RecordDeleteReference) error {
				if s.dependencies.PlanUpdateReference == nil {
					return recordDeleteInternalError("plan set-null relation mutation", nil)
				}
				plan, _, err := s.dependencies.PlanUpdateReference(ctx, reference, principal)
				if err != nil {
					return err
				}
				commit := plan.CanonicalCommit()
				group.plans = append(group.plans, plan)
				group.effects = append(group.effects, recordDeletePlannedEffect{commit: commit})
				return nil
			},
			Cascade: func(ctx context.Context, reference recordservice.RecordDeleteReference) error {
				return s.planDelete(ctx, reference.Object.Key, reference.Record.ID, "", principal, visited, true, group)
			},
		}); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.dependencies.ValidatePolicies != nil {
		if err := s.dependencies.ValidatePolicies(ctx, object, beforeData, beforeData, recordID, "delete", principal); err != nil {
			return err
		}
	}
	record.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	if expectedUpdatedAt == "" {
		expectedUpdatedAt = readRevision
	}
	commit := transactionmodel.RecordMutationCommit{Operation: "delete", Object: object, Record: record, RecordID: recordID, Optimistic: transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: expectedUpdatedAt}}
	if s.dependencies.BuildAudit != nil {
		audit := s.dependencies.BuildAudit(ctx, "record_deleted", objectKey, recordID, principal, "Deleted "+objectKey+" record", beforeData, nil, nil)
		commit.Audit = &audit
	}
	if s.dependencies.AfterOutbox != nil {
		commit.Outbox = s.dependencies.AfterOutbox(objectKey, "delete", beforeData, record, principal)
	}
	plan, err := s.dependencies.MutationKernel.Plan(ctx, principal, commit, beforeData)
	if err != nil {
		return recordMutationPlanApplicationError(err)
	}
	group.plans = append(group.plans, plan)
	group.effects = append(group.effects, recordDeletePlannedEffect{commit: commit})
	return nil
}

func (s *RecordDeleteApplicationService) now() time.Time {
	if s.dependencies.Now != nil {
		return s.dependencies.Now()
	}
	return time.Now()
}

func recordDeleteInternalError(operation string, err error) error {
	var applicationError *apperror.AppError
	if errors.As(err, &applicationError) {
		return err
	}
	return recordDeleteError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func recordDeleteError(kind apperror.ErrorKind, code string, err error, params ...string) error {
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

func recordDeleteErrorFrom(kind apperror.ErrorKind, err error) error {
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

func recordDeleteInt(value any) (int, bool) {
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
