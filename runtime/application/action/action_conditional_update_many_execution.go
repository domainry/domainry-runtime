package action

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	recordmutation "github.com/domainry/domainry-runtime/runtime/application/recordmutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (e *businessActionExecution) ConditionalUpdateMany(ctx context.Context, request runtimeext.ConditionalUpdateManyRequest) (runtimeext.ConditionalUpdateManyResult, error) {
	if !request.Valid() {
		return runtimeext.ConditionalUpdateManyResult{}, apperror.New(apperror.KindBadRequest, "backend.action.conditional_update_many_invalid", nil, nil)
	}
	if !e.hasObjectOperation(request.ObjectKey, "conditional_update_many") || !actionEffectAllows(e.action.EffectSet, request.ObjectKey, false) || !actionEffectAllows(e.action.EffectSet, request.ObjectKey, true) {
		return runtimeext.ConditionalUpdateManyResult{}, apperror.New(apperror.KindForbidden, "backend.action.conditional_update_many_grant_denied", nil, map[string]string{"object": request.ObjectKey})
	}
	if e.targetGrant != nil && !e.targetResolved {
		return runtimeext.ConditionalUpdateManyResult{}, apperror.New(apperror.KindBadRequest, "backend.action.target_organization_unresolved", nil, nil)
	}
	if e.dependencies.ListRecords == nil || e.dependencies.PlanConditionalUpdateLockedRecord == nil || e.dependencies.ObjectForKey == nil {
		return runtimeext.ConditionalUpdateManyResult{}, missingExecutorPort("conditional_update_many")
	}
	object, ok := e.dependencies.ObjectForKey(request.ObjectKey)
	if !ok || strings.TrimSpace(object.Key) != strings.TrimSpace(request.ObjectKey) {
		return runtimeext.ConditionalUpdateManyResult{}, apperror.New(apperror.KindBadRequest, "backend.action.conditional_update_many_coverage_invalid", nil, map[string]string{"object": request.ObjectKey})
	}
	normalizedCoverage, err := normalizeConditionalUpdateManyCoverage(object, request.ExactCoverage, request.ExpectedCount)
	if err != nil {
		return runtimeext.ConditionalUpdateManyResult{}, err
	}
	request.ExactCoverage = normalizedCoverage
	txCtx, err := e.unitOfWork.beginWriting(ctx)
	if err != nil {
		return runtimeext.ConditionalUpdateManyResult{}, err
	}
	filterExpression, err := actionRecordFilterExpression(request.Filters)
	if err != nil {
		return runtimeext.ConditionalUpdateManyResult{}, err
	}
	readPrincipal := actionReadEffectAuthorizationPrincipal(e.invocation.Principal, e.action.EffectSet, e.action, request.ObjectKey)
	page, err := e.dependencies.ListRecords(txCtx, request.ObjectKey, recordmodel.RecordListQuery{
		Page: 1, PageSize: request.ExpectedCount, SkipTotal: true,
		FilterExpression:         filterExpression,
		Sort:                     []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}},
		LockIntent:               recordmodel.RecordQueryLockForUpdate,
		OwnerOrganizationScopeID: e.targetOrganization.ID,
	}, readPrincipal)
	if err != nil {
		return runtimeext.ConditionalUpdateManyResult{}, err
	}
	if page.HasNext || len(page.Items) != request.ExpectedCount {
		return runtimeext.ConditionalUpdateManyResult{}, apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_cardinality_mismatch", nil, map[string]string{
			"expected": fmt.Sprint(request.ExpectedCount), "actual": fmt.Sprint(len(page.Items)),
		})
	}
	if err := validateConditionalUpdateManyExactCoverage(object, normalizedCoverage, page.Items); err != nil {
		return runtimeext.ConditionalUpdateManyResult{}, err
	}
	commit, ids, err := e.planConditionalUpdateMany(txCtx, request, filterExpression, page.Items)
	if err != nil {
		return runtimeext.ConditionalUpdateManyResult{}, err
	}
	if e.targetGrant != nil && strings.TrimSpace(commit.Record.OwnerOrgID) != strings.TrimSpace(e.targetOrganization.ID) {
		return runtimeext.ConditionalUpdateManyResult{}, apperror.New(apperror.KindForbidden, "backend.action.mixed_target_organization", nil, map[string]string{"object": request.ObjectKey})
	}
	e.setCommits = append(e.setCommits, commit)
	for _, id := range ids {
		e.trackRecordMutation(runtimeext.MutationConditionalUpdate, request.ObjectKey, id)
	}
	return runtimeext.ConditionalUpdateManyResult{RecordIDs: ids, AffectedCount: len(ids)}, nil
}

func (e *businessActionExecution) hasObjectOperation(objectKey, operation string) bool {
	for _, capability := range e.objectGrants {
		if strings.TrimSpace(capability.ObjectKey) != strings.TrimSpace(objectKey) {
			continue
		}
		for _, granted := range capability.Operations {
			if strings.TrimSpace(granted) == operation {
				return true
			}
		}
	}
	return false
}

func (e *businessActionExecution) planConditionalUpdateMany(ctx context.Context, request runtimeext.ConditionalUpdateManyRequest, filter *recordmodel.RecordFilterExpression, records []recordmodel.Record) (transactionmodel.RecordMutationCommit, []string, error) {
	var result transactionmodel.RecordMutationCommit
	ids := make([]string, 0, len(records))
	seen := map[string]bool{}
	var commonPatch map[string]any
	for index, before := range records {
		if strings.TrimSpace(before.ID) == "" || seen[before.ID] {
			return result, nil, apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_record_set_invalid", nil, nil)
		}
		seen[before.ID] = true
		e.observeRecord(request.ObjectKey, before)
		planCtx := recordmutation.WithMutationInvocation(ctx, recordmutation.MutationInvocation{
			Source: transactionmodel.MutationSourceAction, ActionKey: e.action.Key, IdempotencyKey: e.invocation.IdempotencyKey,
			EffectAuthority: actionEffectAuthority(e.action.EffectSet), TargetOrganizationID: e.targetOrganization.ID,
		})
		plan, _, err := e.dependencies.PlanConditionalUpdateLockedRecord(planCtx, request.ObjectKey, before, transactionmodel.ConditionalUpdateInput{Patch: request.Fields}, e.actionMutationPrincipal())
		if err != nil {
			return result, nil, err
		}
		member := plan.CanonicalCommit()
		if member.Operation != "update" || member.Object.Key != request.ObjectKey || member.Record.ID != before.ID || len(member.LocalizedValues) != 0 || len(member.Predicates) != 0 {
			return result, nil, apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_plan_invalid", nil, nil)
		}
		patch := changedRecordFields(member.Object, before.Data, member.Record.Data)
		if len(patch) == 0 || index > 0 && !reflect.DeepEqual(commonPatch, patch) {
			return result, nil, apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_non_uniform", nil, nil)
		}
		if index == 0 {
			commonPatch = patch
			result = transactionmodel.RecordMutationCommit{
				Operation: "conditional_update_many", Object: member.Object,
				Record:              recordmodel.Record{ID: before.ID, OwnerOrgID: before.OwnerOrgID, UpdateBy: member.Record.UpdateBy, UpdatedAt: member.Record.UpdatedAt},
				AuthorizationScope:  member.AuthorizationScope,
				SetFilterExpression: filter, SetExpectedAffected: request.ExpectedCount,
				SetOwnerOrganizationScope: e.targetOrganization.ID,
				SetExactCoverageField:     request.ExactCoverage.Field,
				SetExactCoverageValues:    append([]any(nil), request.ExactCoverage.ExpectedValues...),
			}
		} else if before.OwnerOrgID != result.Record.OwnerOrgID || !reflect.DeepEqual(member.AuthorizationScope, result.AuthorizationScope) {
			return result, nil, apperror.New(apperror.KindForbidden, "backend.action.conditional_update_many_scope_mismatch", nil, nil)
		}
		ids = append(ids, before.ID)
		result.SetRecordIDs = append(result.SetRecordIDs, before.ID)
		if member.Audit != nil {
			result.Audits = append(result.Audits, *member.Audit)
		}
		result.Audits = append(result.Audits, member.Audits...)
		result.Outbox = append(result.Outbox, member.Outbox...)
		result.WorkflowIntents = append(result.WorkflowIntents, member.WorkflowIntents...)
		result.NotificationEvents = append(result.NotificationEvents, member.NotificationEvents...)
	}
	result.Record.Data = commonPatch
	sort.Strings(ids)
	sort.Strings(result.SetRecordIDs)
	return result, ids, nil
}

func normalizeConditionalUpdateManyCoverage(object definitionmodel.ObjectSchema, coverage runtimeext.ConditionalUpdateManyExactCoverage, expectedCount int) (runtimeext.ConditionalUpdateManyExactCoverage, error) {
	field, ok := conditionalUpdateManyCoverageField(object, coverage.Field)
	if !ok || len(coverage.ExpectedValues) != expectedCount {
		return runtimeext.ConditionalUpdateManyExactCoverage{}, apperror.New(apperror.KindBadRequest, "backend.action.conditional_update_many_coverage_invalid", nil, map[string]string{"object": object.Key, "field": strings.TrimSpace(coverage.Field)})
	}
	normalized := runtimeext.ConditionalUpdateManyExactCoverage{Field: field.Key, ExpectedValues: make([]any, len(coverage.ExpectedValues))}
	seen := map[string]bool{}
	for index, value := range coverage.ExpectedValues {
		current, err := conditionalUpdateManyCoverageValue(field, value)
		if err != nil || current == nil {
			return runtimeext.ConditionalUpdateManyExactCoverage{}, apperror.New(apperror.KindBadRequest, "backend.action.conditional_update_many_coverage_invalid", err, map[string]string{"object": object.Key, "field": field.Key})
		}
		key, err := conditionalUpdateManyCoverageValueKey(current)
		if err != nil || seen[key] {
			return runtimeext.ConditionalUpdateManyExactCoverage{}, apperror.New(apperror.KindBadRequest, "backend.action.conditional_update_many_coverage_invalid", err, map[string]string{"object": object.Key, "field": field.Key})
		}
		seen[key] = true
		normalized.ExpectedValues[index] = current
	}
	return normalized, nil
}

func validateConditionalUpdateManyExactCoverage(object definitionmodel.ObjectSchema, coverage runtimeext.ConditionalUpdateManyExactCoverage, records []recordmodel.Record) error {
	field, ok := conditionalUpdateManyCoverageField(object, coverage.Field)
	if !ok {
		return apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_coverage_mismatch", nil, map[string]string{"object": object.Key, "field": coverage.Field})
	}
	expected := map[string]bool{}
	for _, value := range coverage.ExpectedValues {
		key, err := conditionalUpdateManyCoverageValueKey(value)
		if err != nil || expected[key] {
			return apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_coverage_mismatch", err, map[string]string{"object": object.Key, "field": field.Key})
		}
		expected[key] = true
	}
	actual := map[string]bool{}
	for _, record := range records {
		value := record.Data[field.Key]
		if field.Key == "id" {
			value = record.ID
		}
		normalized, err := conditionalUpdateManyCoverageValue(field, value)
		if err != nil || normalized == nil {
			return apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_coverage_mismatch", err, map[string]string{"object": object.Key, "field": field.Key})
		}
		key, err := conditionalUpdateManyCoverageValueKey(normalized)
		if err != nil || !expected[key] || actual[key] {
			return apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_coverage_mismatch", err, map[string]string{"object": object.Key, "field": field.Key})
		}
		actual[key] = true
	}
	if len(actual) != len(expected) {
		return apperror.New(apperror.KindConflict, "backend.action.conditional_update_many_coverage_mismatch", nil, map[string]string{"object": object.Key, "field": field.Key})
	}
	return nil
}

func conditionalUpdateManyCoverageField(object definitionmodel.ObjectSchema, raw string) (definitionmodel.FieldSchema, bool) {
	key := strings.TrimSpace(raw)
	if key == "id" {
		return definitionmodel.FieldSchema{Key: "id", Type: "text"}, true
	}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == key {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func conditionalUpdateManyCoverageValue(field definitionmodel.FieldSchema, value any) (any, error) {
	if field.Key == "id" {
		text := strings.TrimSpace(fmt.Sprint(value))
		if value == nil || text == "" {
			return nil, fmt.Errorf("coverage identity is empty")
		}
		return text, nil
	}
	return recordvalidation.RecordNormalizeFieldValue(field, value)
}

func conditionalUpdateManyCoverageValueKey(value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%T:%s", value, payload), nil
}

func changedRecordFields(object definitionmodel.ObjectSchema, before, after map[string]any) map[string]any {
	result := map[string]any{}
	for _, field := range object.Fields {
		if !reflect.DeepEqual(before[field.Key], after[field.Key]) {
			result[field.Key] = after[field.Key]
		}
	}
	return result
}
