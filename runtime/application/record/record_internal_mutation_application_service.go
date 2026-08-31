package record

import (
	"fmt"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	"math"
	"reflect"
	"strconv"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type RecordInternalMutationPolicy string

const (
	RecordInternalMutationOwnerPathRebuild RecordInternalMutationPolicy = "owner_department_path_rebuild"
)

type RecordInternalMutationOperation string

const (
	RecordInternalMutationCreate RecordInternalMutationOperation = "create"
	RecordInternalMutationUpdate RecordInternalMutationOperation = "update"
)

type RecordInternalMutationDependencies struct {
	Repository recordrepository.RecordRepository
	Audit      func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
}

func (s *RecordApplicationService) PlanConditionalUpdateMutation(ctx context.Context, objectKey, recordID string, input transactionmodel.ConditionalUpdateInput, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
	return s.update.PlanConditionalUpdateMutation(ctx, objectKey, recordID, input, principal)
}

func (s *RecordUpdateApplicationService) ConditionalUpdate(ctx context.Context, objectKey, recordID string, input transactionmodel.ConditionalUpdateInput, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := ctx.Err(); err != nil {
		return recordmodel.Record{}, err
	}
	plan, record, err := s.PlanConditionalUpdateMutation(ctx, objectKey, recordID, input, principal)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.dependencies.MutationKernel.CommitPlan(ctx, plan, nil); err != nil {
		return recordmodel.Record{}, recordUpdateCommitError(err)
	}
	commit := plan.CanonicalCommit()
	if err := ctx.Err(); err != nil {
		return recordpolicy.RecordFilterReadable(principal, commit.Object, record), err
	}
	if s.dependencies.ExecuteWorkflow != nil {
		s.dependencies.ExecuteWorkflow(ctx, commit.WorkflowIntents, principal)
	}
	return recordpolicy.RecordFilterReadable(principal, commit.Object, record), nil
}

func (s *RecordApplicationService) PlanDeleteMutation(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, principal principalmodel.Principal) ([]transactionmodel.MutationPlan, error) {
	return s.delete.PlanDeleteMutation(ctx, objectKey, recordID, expectedUpdatedAt, principal)
}

func (s *RecordApplicationService) PlanRestoreMutation(ctx context.Context, objectKey, recordID, expectedUpdatedAt string, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
	return s.restore.PlanRestoreMutation(ctx, objectKey, recordID, expectedUpdatedAt, principal)
}

func (s *RecordUpdateApplicationService) PlanConditionalUpdateMutation(ctx context.Context, objectKey, recordID string, input transactionmodel.ConditionalUpdateInput, principal principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	authorizationPrincipal := recordEffectAuthorizationPrincipal(ctx, principal, objectKey, "update")
	object, err := s.dependencies.ObjectForAction(authorizationPrincipal, objectKey, "update")
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if err := recordpolicy.RecordValidateRuntimeOwnedCRUD(object, "update"); err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	record, found, err := s.dependencies.Repository.GetRecord(ctx, principal.WorkspaceID, object, recordID)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordUpdateError(apperror.KindInternal, "backend.internal", err, "operation", "get conditional record")
	}
	if !found {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordUpdateError(apperror.KindNotFound, "backend.record.not_found", nil)
	}
	allowed, err := s.canAccessScope(ctx, authorizationPrincipal, object, record, false)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	if !allowed {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordUpdateError(apperror.KindForbidden, "backend.record.outside_scope", nil)
	}
	predicates, err := recordCanonicalPredicates(object, record, input.Predicates)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	patch := recordvalidation.RecordCloneData(input.Patch)
	for _, arithmetic := range input.Arithmetic {
		field, ok := recordConditionalField(object, arithmetic.Field)
		if !ok {
			return transactionmodel.MutationPlan{}, recordmodel.Record{}, recordUpdateError(apperror.KindBadRequest, "backend.mutation.arithmetic_field_invalid", nil, "field", arithmetic.Field)
		}
		value, err := recordConditionalArithmetic(field, record.Data[field.Key], arithmetic)
		if err != nil {
			return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
		}
		patch[field.Key] = value
		predicates = append(predicates, transactionmodel.MutationPredicate{Field: field.Key, Operator: "eq", Value: record.Data[field.Key], ErrorCode: "backend.mutation.concurrent_change"})
	}
	ctx = recordmutation.WithMutationPredicates(ctx, predicates)
	planned, err := s.planUpdate(ctx, objectKey, object, record, patch, nil, nil, principal, authorizationPrincipal)
	if err != nil {
		return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
	}
	return planned.plan, planned.record, nil
}

func recordCanonicalPredicates(object definitionmodel.ObjectSchema, record recordmodel.Record, predicates []transactionmodel.MutationPredicate) ([]transactionmodel.MutationPredicate, error) {
	result := make([]transactionmodel.MutationPredicate, 0, len(predicates))
	for _, predicate := range predicates {
		predicate.Field = strings.TrimSpace(predicate.Field)
		predicate.Operator = strings.TrimSpace(predicate.Operator)
		predicate.ErrorCode = strings.TrimSpace(predicate.ErrorCode)
		actual := any(record.UpdatedAt)
		matched := false
		var matchErr error
		if predicate.Field != "updated_at" {
			field, ok := recordConditionalField(object, predicate.Field)
			if !ok {
				return nil, recordUpdateError(apperror.KindBadRequest, "backend.mutation.predicate_invalid", nil, "field", predicate.Field)
			}
			normalized, err := recordvalidation.RecordNormalizeFieldValue(field, predicate.Value)
			if err != nil {
				return nil, err
			}
			predicate.Value = normalized
			actual, err = recordvalidation.RecordNormalizeFieldValue(field, record.Data[field.Key])
			if err != nil {
				return nil, err
			}
			matched, matchErr = recordPredicateMatchesField(field, actual, predicate.Value, predicate.Operator)
		} else {
			matched, matchErr = recordPredicateMatches(actual, predicate.Value, predicate.Operator)
		}
		if matchErr != nil {
			return nil, recordUpdateError(apperror.KindBadRequest, "backend.mutation.predicate_invalid", matchErr, "field", predicate.Field)
		}
		if !matched {
			code := predicate.ErrorCode
			if code == "" {
				code = "backend.mutation.predicate_failed"
			}
			return nil, recordUpdateError(apperror.KindConflict, code, nil, "field", predicate.Field)
		}
		result = append(result, predicate)
	}
	return result, nil
}

func recordPredicateMatchesField(field definitionmodel.FieldSchema, actual, expected any, operator string) (bool, error) {
	if operator == "eq" || operator == "ne" {
		return recordPredicateMatches(actual, expected, operator)
	}
	comparison := 0
	switch field.Type {
	case "integer":
		left, leftOK := actual.(int64)
		right, rightOK := expected.(int64)
		if !leftOK || !rightOK {
			return false, fmt.Errorf("ordered integer predicate requires integer operands")
		}
		if left < right {
			comparison = -1
		} else if left > right {
			comparison = 1
		}
	case "currency":
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return false, err
		}
		comparison, err = recordmodel.RecordCompareDecimals(actual, expected, config)
		if err != nil {
			return false, err
		}
	default:
		return recordPredicateMatches(actual, expected, operator)
	}
	switch operator {
	case "lt":
		return comparison < 0, nil
	case "lte":
		return comparison <= 0, nil
	case "gt":
		return comparison > 0, nil
	case "gte":
		return comparison >= 0, nil
	default:
		return false, fmt.Errorf("unsupported predicate operator %q", operator)
	}
}

func recordConditionalField(object definitionmodel.ObjectSchema, key string) (definitionmodel.FieldSchema, bool) {
	key = strings.TrimSpace(key)
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == key {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func recordConditionalArithmetic(field definitionmodel.FieldSchema, current any, arithmetic transactionmodel.MutationArithmetic) (any, error) {
	operation := strings.TrimSpace(arithmetic.Operation)
	if operation != "increment" && operation != "decrement" {
		return nil, recordUpdateError(apperror.KindBadRequest, "backend.mutation.arithmetic_operation_invalid", nil, "operation", operation)
	}
	if field.Type == "currency" {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return nil, err
		}
		operand, err := recordmodel.RecordNormalizeDecimal(arithmetic.Operand, config)
		if err != nil {
			return nil, err
		}
		if operation == "increment" {
			return recordmodel.RecordAddDecimals(fmt.Sprint(current), operand, config)
		}
		return recordmodel.RecordSubtractDecimals(fmt.Sprint(current), operand, config)
	}
	if field.Type == "integer" {
		leftValue, leftErr := recordvalidation.RecordNormalizeFieldValue(field, current)
		rightValue, rightErr := recordvalidation.RecordNormalizeFieldValue(field, arithmetic.Operand)
		if leftErr != nil || rightErr != nil {
			return nil, recordUpdateError(apperror.KindBadRequest, "backend.mutation.arithmetic_operand_invalid", nil, "field", field.Key)
		}
		left, leftOK := leftValue.(int64)
		right, rightOK := rightValue.(int64)
		if !leftOK || !rightOK || recordIntegerArithmeticOverflows(left, right, operation) {
			return nil, recordUpdateError(apperror.KindBadRequest, "backend.mutation.arithmetic_overflow", nil, "field", field.Key)
		}
		if operation == "decrement" {
			return left - right, nil
		}
		return left + right, nil
	}
	if field.Type != "number" && field.Type != "percent" {
		return nil, recordUpdateError(apperror.KindBadRequest, "backend.mutation.arithmetic_field_invalid", nil, "field", field.Key)
	}
	left, leftErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(current)), 64)
	right, rightErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(arithmetic.Operand)), 64)
	if leftErr != nil || rightErr != nil {
		return nil, recordUpdateError(apperror.KindBadRequest, "backend.mutation.arithmetic_operand_invalid", nil, "field", field.Key)
	}
	if operation == "decrement" {
		right = -right
	}
	return left + right, nil
}

func recordIntegerArithmeticOverflows(left, right int64, operation string) bool {
	if operation == "decrement" {
		return (right > 0 && left < math.MinInt64+right) || (right < 0 && left > math.MaxInt64+right)
	}
	return (right > 0 && left > math.MaxInt64-right) || (right < 0 && left < math.MinInt64-right)
}

func recordPredicateMatches(actual, expected any, operator string) (bool, error) {
	if operator == "eq" {
		return reflect.DeepEqual(actual, expected) || fmt.Sprint(actual) == fmt.Sprint(expected), nil
	}
	if operator == "ne" {
		matched, _ := recordPredicateMatches(actual, expected, "eq")
		return !matched, nil
	}
	left, leftErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(actual)), 64)
	right, rightErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(expected)), 64)
	if leftErr != nil || rightErr != nil {
		return false, fmt.Errorf("ordered predicate requires numeric operands")
	}
	switch operator {
	case "lt":
		return left < right, nil
	case "lte":
		return left <= right, nil
	case "gt":
		return left > right, nil
	case "gte":
		return left >= right, nil
	default:
		return false, fmt.Errorf("unsupported predicate operator %q", operator)
	}
}

type RecordInternalUpdater func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, string) error

// RecordInternalMutationApplicationService applies audited internal Record mutations.
type RecordInternalMutationApplicationService struct {
	repository recordrepository.RecordRepository
	audit      func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
}

func NewRecordInternalMutationApplicationService(dependencies RecordInternalMutationDependencies) *RecordInternalMutationApplicationService {
	return &RecordInternalMutationApplicationService{repository: dependencies.Repository, audit: dependencies.Audit}
}

func (s *RecordInternalMutationApplicationService) Insert(ctx context.Context, workspaceID string, policy RecordInternalMutationPolicy, object definitionmodel.ObjectSchema, record recordmodel.Record, reason string) error {
	if err := RecordValidateInternalMutationPolicy(policy, RecordInternalMutationCreate, object); err != nil {
		return err
	}
	if err := s.repository.InsertRecord(ctx, workspaceID, object, record); err != nil {
		return recordInternalMutationError(apperror.KindInternal, "backend.internal", err, "operation", "internal record insert")
	}
	s.appendAudit(ctx, policy, RecordInternalMutationCreate, object, record, reason)
	return nil
}

func (s *RecordInternalMutationApplicationService) Update(ctx context.Context, workspaceID string, policy RecordInternalMutationPolicy, object definitionmodel.ObjectSchema, record recordmodel.Record, reason string) error {
	if err := RecordValidateInternalMutationPolicy(policy, RecordInternalMutationUpdate, object); err != nil {
		return err
	}
	if err := s.repository.UpdateRecord(ctx, workspaceID, object, record); err != nil {
		return recordInternalMutationError(apperror.KindInternal, "backend.internal", err, "operation", "internal record update")
	}
	s.appendAudit(ctx, policy, RecordInternalMutationUpdate, object, record, reason)
	return nil
}

func RecordValidateInternalMutationPolicy(policy RecordInternalMutationPolicy, operation RecordInternalMutationOperation, object definitionmodel.ObjectSchema) error {
	switch policy {
	case RecordInternalMutationOwnerPathRebuild:
		if operation == RecordInternalMutationUpdate && recordpolicy.RecordOwnerDepartmentIDFieldKey(object) != "" && recordpolicy.RecordOwnerDepartmentPathFieldKey(object) != "" {
			return nil
		}
	}
	return recordInternalMutationError(apperror.KindForbidden, "backend.record.internal_mutation_policy_denied", nil, "policy", string(policy), "operation", string(operation), "object", object.Key)
}

func recordInternalMutationError(kind apperror.ErrorKind, code string, err error, params ...string) error {
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

func (s *RecordInternalMutationApplicationService) appendAudit(ctx context.Context, policy RecordInternalMutationPolicy, operation RecordInternalMutationOperation, object definitionmodel.ObjectSchema, record recordmodel.Record, reason string) {
	if s.audit == nil {
		return
	}
	principal := principalmodel.NewSystemPrincipal("system", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "internal_record_mutation_audit"))
	s.audit(ctx, "internal_record_mutation", object.Key, record.ID, principal, "Internal record mutation", nil, nil, map[string]any{
		"policy": string(policy), "operation": string(operation), "reason": strings.TrimSpace(reason),
	})
}
