package record

import (
	ormbuilder "github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"github.com/domainry/domainry-foundation/mutation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"context"
	"database/sql"
	"errors"
	"fmt"

	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

func (r RecordStore) validateRelatedAggregateInvariantsTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit, operation string) error {
	policies, err := recordvalidation.RecordRelatedAggregateInvariants(commit.Object)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		applies, err := recordvalidation.RecordRelatedAggregateCandidate(policy, commit.Record.Data, operation)
		if err != nil {
			return err
		}
		if !applies {
			continue
		}
		relationID := strings.TrimSpace(fmt.Sprint(commit.Record.Data[policy.RelationField]))
		limitBuilder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, policy.TargetObjectKey, workspaceID).Columns(policy.LimitField).Where(ormbuilder.Equal("id", relationID)).Limit(1)
		if r.store.RuntimeEngine.Capabilities().RowLock {
			limitBuilder.ForUpdate()
		}
		limitQuery, limitArgs, buildErr := limitBuilder.Build()
		if buildErr != nil {
			return buildErr
		}
		var rawLimit any
		if err := tx.QueryRowContext(ctx, limitQuery, limitArgs...).Scan(&rawLimit); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return mutation.PolicyConflict("backend.policy.related_record_missing", commit.Object.Key, commit.Record.ID, policy.RelationField)
			}
			return fmt.Errorf("lock related aggregate limit %s: %w", policy.Key, err)
		}
		columns := []string{"id"}
		if policy.Aggregate == "sum" {
			columns = append(columns, policy.ValueField)
		}
		if policy.StatusField != "" {
			columns = append(columns, policy.StatusField)
		}
		predicates := []ormbuilder.Predicate{ormbuilder.Equal(policy.RelationField, relationID), ormbuilder.NotEqual("id", commit.Record.ID)}
		if policy.StatusField != "" && len(policy.IncludedStatuses) > 0 {
			statuses := make([]any, 0, len(policy.IncludedStatuses))
			for _, status := range policy.IncludedStatuses {
				statuses = append(statuses, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, policy.StatusField, status))
			}
			predicates = append(predicates, ormbuilder.In(policy.StatusField, statuses...))
		}
		aggregateBuilder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, commit.Object.Key, workspaceID).Columns(columns...).Where(ormbuilder.And(predicates...))
		if r.store.RuntimeEngine.Capabilities().RowLock {
			aggregateBuilder.ForUpdate()
		}
		query, args, buildErr := aggregateBuilder.Build()
		if buildErr != nil {
			return buildErr
		}
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("read related aggregate %s: %w", policy.Key, err)
		}
		valueField := recordAggregateValueField(commit.Object, policy)
		total, err := recordAggregateZero(valueField, policy.Aggregate)
		if err != nil {
			rows.Close()
			return err
		}
		for rows.Next() {
			var id string
			var rawValue, rawStatus any
			scans := []any{&id}
			if policy.Aggregate == "sum" {
				scans = append(scans, &rawValue)
			}
			if policy.StatusField != "" {
				scans = append(scans, &rawStatus)
			}
			if err := rows.Scan(scans...); err != nil {
				rows.Close()
				return fmt.Errorf("scan related aggregate %s: %w", policy.Key, err)
			}
			operand := any(1)
			if policy.Aggregate == "sum" {
				operand = normalizeDBValue(r.store.RuntimeEngine, valueField, rawValue)
			}
			total, err = recordAggregateAdd(total, operand, valueField, policy.Aggregate)
			if err != nil {
				rows.Close()
				return err
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate related aggregate %s: %w", policy.Key, err)
		}
		rows.Close()
		candidate := any(1)
		if policy.Aggregate == "sum" {
			candidate = commit.Record.Data[policy.ValueField]
		}
		total, err = recordAggregateAdd(total, candidate, valueField, policy.Aggregate)
		if err != nil {
			return err
		}
		limit := normalizeDBValue(r.store.RuntimeEngine, valueField, rawLimit)
		matched, err := recordAggregateCompare(total, limit, valueField, policy.Operator)
		if err != nil {
			return err
		}
		if !matched {
			return mutation.PolicyConflict(policy.ErrorCode, commit.Object.Key, commit.Record.ID, policy.Key)
		}
	}
	return nil
}

func recordAggregateValueField(object definitionmodel.ObjectSchema, policy recordvalidation.RecordRelatedAggregateInvariant) definitionmodel.FieldSchema {
	if policy.Aggregate == "count" {
		return definitionmodel.FieldSchema{Key: policy.LimitField, Type: "number"}
	}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == policy.ValueField {
			return field
		}
	}
	return definitionmodel.FieldSchema{Key: policy.ValueField, Type: "number"}
}

func recordAggregateZero(field definitionmodel.FieldSchema, aggregate string) (any, error) {
	if aggregate == "count" || field.Type != "currency" {
		return float64(0), nil
	}
	config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
	if err != nil {
		return nil, err
	}
	return recordmodel.RecordNormalizeDecimal("0", config)
}

func recordAggregateAdd(total, operand any, field definitionmodel.FieldSchema, aggregate string) (any, error) {
	if aggregate == "sum" && field.Type == "currency" {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return nil, err
		}
		left, err := recordmodel.RecordNormalizeDecimal(total, config)
		if err != nil {
			return nil, err
		}
		right, err := recordmodel.RecordNormalizeDecimal(operand, config)
		if err != nil {
			return nil, err
		}
		return recordmodel.RecordAddDecimals(left, right, config)
	}
	left, leftErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(total)), 64)
	right, rightErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(operand)), 64)
	if leftErr != nil || rightErr != nil {
		return nil, fmt.Errorf("related aggregate requires numeric values")
	}
	return left + right, nil
}

func recordAggregateCompare(total, limit any, field definitionmodel.FieldSchema, operator string) (bool, error) {
	if field.Type == "currency" {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return false, err
		}
		left, err := recordmodel.RecordNormalizeDecimal(total, config)
		if err != nil {
			return false, err
		}
		right, err := recordmodel.RecordNormalizeDecimal(limit, config)
		if err != nil {
			return false, err
		}
		// RecordNormalizeDecimal already guarantees canonical decimal strings.
		leftDecimal := decimal.RequireFromString(left)
		rightDecimal := decimal.RequireFromString(right)
		comparison := leftDecimal.Cmp(rightDecimal)
		switch operator {
		case "lt":
			return comparison < 0, nil
		case "lte":
			return comparison <= 0, nil
		case "eq":
			return comparison == 0, nil
		case "gte":
			return comparison >= 0, nil
		case "gt":
			return comparison > 0, nil
		default:
			return false, fmt.Errorf("unsupported related aggregate operator %q", operator)
		}
	}
	left, leftErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(total)), 64)
	right, rightErr := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(limit)), 64)
	if leftErr != nil || rightErr != nil {
		return false, fmt.Errorf("related aggregate limit requires numeric values")
	}
	return recordAggregateCompareFloat(left, right, operator)
}

func recordAggregateCompareFloat(left, right float64, operator string) (bool, error) {
	switch operator {
	case "lt":
		return left < right, nil
	case "lte":
		return left <= right, nil
	case "eq":
		return left == right, nil
	case "gte":
		return left >= right, nil
	case "gt":
		return left > right, nil
	default:
		return false, fmt.Errorf("unsupported related aggregate operator %q", operator)
	}
}

func (r RecordStore) validateTemporalExclusionTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit, operation string) error {
	policies, err := recordvalidation.RecordTemporalExclusionPolicies(commit.Object)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		applies, err := recordvalidation.RecordTemporalExclusionCandidate(policy, commit.Record.Data, operation)
		if err != nil {
			return err
		}
		if !applies {
			continue
		}
		predicates := []ormbuilder.Predicate{ormbuilder.NotEqual("id", commit.Record.ID)}
		for _, field := range policy.ScopeFields {
			predicates = append(predicates, ormbuilder.Equal(field, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, field, commit.Record.Data[field])))
		}
		predicates = append(predicates, ormbuilder.LessThan(policy.StartField, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, policy.StartField, commit.Record.Data[policy.EndField])))
		predicates = append(predicates, ormbuilder.GreaterThan(policy.EndField, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, policy.EndField, commit.Record.Data[policy.StartField])))
		if policy.StatusField != "" && len(policy.ExcludedStatuses) > 0 {
			statuses := make([]any, 0, len(policy.ExcludedStatuses))
			for _, status := range policy.ExcludedStatuses {
				statuses = append(statuses, recordConditionDBValue(r.store.RuntimeEngine, commit.Object, policy.StatusField, status))
			}
			predicates = append(predicates, ormbuilder.Or(ormbuilder.IsNull(policy.StatusField), ormbuilder.NotIn(policy.StatusField, statuses...)))
		}
		conflictBuilder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, commit.Object.Key, workspaceID).Columns("id").Where(ormbuilder.And(predicates...)).Limit(1)
		if r.store.RuntimeEngine.Capabilities().RowLock {
			conflictBuilder.ForUpdate()
		}
		query, args, buildErr := conflictBuilder.Build()
		if buildErr != nil {
			return buildErr
		}
		var conflictingID string
		err = tx.QueryRowContext(ctx, query, args...).Scan(&conflictingID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("validate temporal exclusion %s: %w", policy.Key, err)
		}
		return mutation.PolicyConflict(policy.ErrorCode, commit.Object.Key, commit.Record.ID, policy.Key)
	}
	return nil
}

func recordMutationPredicateSQL(store *database.RuntimeStore, object definitionmodel.ObjectSchema, predicate transactionmodel.MutationPredicate, placeholder int) (string, []any, error) {
	prepared, err := recordMutationPredicate(object, predicate, store.RuntimeEngine)
	if err != nil {
		return "", nil, err
	}
	return ormbuilder.PreparePredicate(store.SQLRenderer, prepared, placeholder-1)
}

func recordMutationPredicate(object definitionmodel.ObjectSchema, predicate transactionmodel.MutationPredicate, profile persistencedriver.EngineProfile) (ormbuilder.Predicate, error) {
	field := strings.TrimSpace(predicate.Field)
	allowed := field == "updated_at"
	for _, schemaField := range object.Fields {
		allowed = allowed || strings.TrimSpace(schemaField.Key) == field
	}
	operator := strings.TrimSpace(predicate.Operator)
	if !allowed {
		return nil, fmt.Errorf("invalid record mutation predicate field %q", field)
	}
	if _, ok := map[string]bool{"eq": true, "ne": true, "lt": true, "lte": true, "gt": true, "gte": true}[operator]; !ok {
		return nil, fmt.Errorf("invalid record mutation predicate operator %q", operator)
	}
	if predicate.Value == nil {
		switch operator {
		case "eq":
			return ormbuilder.IsNull(field), nil
		case "ne":
			return ormbuilder.IsNotNull(field), nil
		default:
			return nil, fmt.Errorf("nil predicate only supports eq/ne for field %q", field)
		}
	}
	value := recordConditionDBValue(profile, object, field, predicate.Value)
	switch operator {
	case "eq":
		return ormbuilder.Equal(field, value), nil
	case "ne":
		return ormbuilder.NotEqual(field, value), nil
	case "lt":
		return ormbuilder.LessThan(field, value), nil
	case "lte":
		return ormbuilder.LessThanOrEqual(field, value), nil
	case "gt":
		return ormbuilder.GreaterThan(field, value), nil
	case "gte":
		return ormbuilder.GreaterThanOrEqual(field, value), nil
	default:
		return nil, fmt.Errorf("invalid record mutation predicate operator %q", operator)
	}
}

func recordConditionDBValue(profile persistencedriver.EngineProfile, object definitionmodel.ObjectSchema, key string, value any) any {
	for _, field := range object.Fields {
		if field.Key == key {
			return dbFieldValue(profile, field, value)
		}
	}
	return dbValue(value)
}
