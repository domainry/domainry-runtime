package query

import (
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-orm/query"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func dbValue(value any) any {
	switch typed := value.(type) {
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return value
	}
}

func nonBlankStrings(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func stringsToAny(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}

func nonEmptyValues(raw any) []any {
	values := make([]any, 0)
	switch typed := raw.(type) {
	case []any:
		values = append(values, typed...)
	case []string:
		values = stringsToAny(typed)
	}
	result := make([]any, 0, len(values))
	for _, value := range values {
		if !recordvalidation.RecordIsEmptyValue(value) {
			result = append(result, dbValue(value))
		}
	}
	return result
}

func BuildTenantPredicate(s Store, workspace string, queryValue recordmodel.RecordListQuery) (query.Predicate, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(workspace)
	if err != nil {
		return nil, fmt.Errorf("tenant query workspace: %w", err)
	}
	workspace = workspaceID.String()
	predicates := []query.Predicate{query.Equal("workspace_id", workspace)}
	appendPredicate := func(predicate query.Predicate) { predicates = append(predicates, predicate) }
	authorizationMode := queryValue.AuthorizationMode
	if authorizationMode != recordmodel.RecordQueryAuthorizationUnrestricted && authorizationMode != recordmodel.RecordQueryAuthorizationPredicate && authorizationMode != recordmodel.RecordQueryAuthorizationDeny {
		return nil, fmt.Errorf("unsupported record query authorization mode %q", authorizationMode)
	}
	if authorizationMode == recordmodel.RecordQueryAuthorizationDeny {
		appendPredicate(query.AlwaysFalse())
	}
	if authorizationMode == recordmodel.RecordQueryAuthorizationPredicate {
		if queryValue.ScopeExpression == nil || strings.TrimSpace(queryValue.RootObjectKey) == "" {
			return nil, fmt.Errorf("predicate authorization requires compiled expression")
		}
		compiled, err := scopeExpressionPredicate(s, queryValue.RootObjectKey, *queryValue.ScopeExpression, 0)
		if err != nil {
			return nil, err
		}
		appendPredicate(compiled)
	}
	if strings.TrimSpace(queryValue.Search) != "" && len(queryValue.SearchFields) > 0 {
		searchPredicates := make([]query.Predicate, 0, len(queryValue.SearchFields))
		searchValue := "%" + escapeLikePattern(strings.ToLower(strings.TrimSpace(queryValue.Search))) + "%"
		for _, field := range queryValue.SearchFields {
			searchPredicates = append(searchPredicates, query.LikeValueEscaped(query.Lower(query.Column(field)), searchValue))
		}
		appendPredicate(query.Or(searchPredicates...))
	}
	filterKeys := make([]string, 0, len(queryValue.Filters))
	for key := range queryValue.Filters {
		filterKeys = append(filterKeys, key)
	}
	sort.Strings(filterKeys)
	for _, key := range filterKeys {
		if key == "workspace_id" {
			continue
		}
		value := queryValue.Filters[key]
		if recordvalidation.RecordIsEmptyValue(value) {
			continue
		}
		if baseKey, operator, ok := splitFilterKey(key); ok {
			if operator == "in" {
				values := nonEmptyValues(value)
				if len(values) == 0 {
					appendPredicate(query.AlwaysFalse())
				} else {
					appendPredicate(query.In(baseKey, values...))
				}
				continue
			}
			predicate := query.Equal(baseKey, dbValue(value))
			if operator == "gte" {
				predicate = query.GreaterThanOrEqual(baseKey, dbValue(value))
			} else if operator == "lte" {
				predicate = query.LessThanOrEqual(baseKey, dbValue(value))
			}
			appendPredicate(predicate)
		} else {
			appendPredicate(query.Equal(key, dbValue(value)))
		}
	}
	if queryValue.FilterExpression != nil {
		predicate, err := recordFilterPredicate(*queryValue.FilterExpression, 0)
		if err != nil {
			return nil, err
		}
		appendPredicate(predicate)
	}
	return query.And(predicates...), nil
}

func BuildTenantWhere(s Store, workspace string, queryValue recordmodel.RecordListQuery) (string, []any, error) {
	predicate, err := BuildTenantPredicate(s, workspace, queryValue)
	if err != nil {
		return "", nil, err
	}
	prepared, args, err := query.PreparePredicate(storeRenderer{s}, predicate, 0)
	if err != nil {
		return "", nil, err
	}
	return " WHERE " + prepared, args, nil
}

func buildRecordFilterExpression(s Store, expression recordmodel.RecordFilterExpression, args *[]any, depth int) (string, error) {
	predicate, err := recordFilterPredicate(expression, depth)
	if err != nil {
		return "", err
	}
	prepared, values, err := query.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return "", err
	}
	*args = append(*args, values...)
	return prepared, nil
}

func recordFilterPredicate(expression recordmodel.RecordFilterExpression, depth int) (query.Predicate, error) {
	if depth > 16 {
		return nil, fmt.Errorf("record filter exceeds maximum depth")
	}
	switch expression.Operator {
	case "and", "or":
		if len(expression.Children) < 2 {
			return nil, fmt.Errorf("record filter %s requires at least two children", expression.Operator)
		}
		predicates := make([]query.Predicate, 0, len(expression.Children))
		for _, child := range expression.Children {
			predicate, err := recordFilterPredicate(child, depth+1)
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, predicate)
		}
		if expression.Operator == "and" {
			return query.And(predicates...), nil
		}
		return query.Or(predicates...), nil
	case "not":
		if len(expression.Children) != 1 {
			return nil, fmt.Errorf("record filter not requires exactly one child")
		}
		predicate, err := recordFilterPredicate(expression.Children[0], depth+1)
		if err != nil {
			return nil, err
		}
		return query.Not(predicate), nil
	case "eq", "ne", "gt", "gte", "lt", "lte":
		value := dbValue(expression.Value)
		switch expression.Operator {
		case "eq":
			return query.Equal(expression.Field, value), nil
		case "ne":
			return query.NotEqual(expression.Field, value), nil
		case "gt":
			return query.GreaterThan(expression.Field, value), nil
		case "gte":
			return query.GreaterThanOrEqual(expression.Field, value), nil
		case "lt":
			return query.LessThan(expression.Field, value), nil
		default:
			return query.LessThanOrEqual(expression.Field, value), nil
		}
	case "in", "not_in":
		values := make([]any, 0, len(expression.Values))
		for _, value := range expression.Values {
			values = append(values, dbValue(value))
		}
		if len(values) == 0 {
			return nil, fmt.Errorf("record filter %s requires values", expression.Operator)
		}
		if expression.Operator == "not_in" {
			return query.NotIn(expression.Field, values...), nil
		}
		return query.In(expression.Field, values...), nil
	case "is_null":
		return query.IsNull(expression.Field), nil
	case "is_not_null":
		return query.IsNotNull(expression.Field), nil
	default:
		return nil, fmt.Errorf("unsupported record filter operator %q", expression.Operator)
	}
}

func buildScopeExpression(s Store, rootObjectKey string, expression recordmodel.RecordScopeExpression, args *[]any, depth int) (string, error) {
	predicate, err := scopeExpressionPredicate(s, rootObjectKey, expression, depth)
	if err != nil {
		return "", err
	}
	prepared, values, err := query.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return "", err
	}
	*args = append(*args, values...)
	return prepared, nil
}

func scopeExpressionPredicate(s Store, rootObjectKey string, expression recordmodel.RecordScopeExpression, depth int) (query.Predicate, error) {
	if depth > 16 {
		return nil, fmt.Errorf("scope expression exceeds maximum depth")
	}
	switch expression.Operator {
	case "and", "or":
		if len(expression.Children) < 2 {
			return nil, fmt.Errorf("scope %s requires at least two children", expression.Operator)
		}
		predicates := make([]query.Predicate, 0, len(expression.Children))
		for _, child := range expression.Children {
			predicate, err := scopeExpressionPredicate(s, rootObjectKey, child, depth+1)
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, predicate)
		}
		if expression.Operator == "and" {
			return query.And(predicates...), nil
		}
		return query.Or(predicates...), nil
	case "not":
		if len(expression.Children) != 1 {
			return nil, fmt.Errorf("scope not requires one child")
		}
		predicate, err := scopeExpressionPredicate(s, rootObjectKey, expression.Children[0], depth+1)
		if err != nil {
			return nil, err
		}
		return query.Not(predicate), nil
	case "eq", "in", "prefix", "starts_with", "exists", "not_exists":
		if len(expression.Path) != 0 {
			if !expression.RelationExists {
				return nil, fmt.Errorf("scope relation path must be resolved to permission IDs before building record query")
			}
			return scopeRelationExistsPredicate(s, rootObjectKey, expression)
		}
		return scopeComparisonPredicate(query.TableColumn(rootObjectKey, expression.FieldKey), expression.Operator, expression.Values), nil
	default:
		return nil, fmt.Errorf("unsupported compiled scope operator %q", expression.Operator)
	}
}

func buildScopeComparison(s Store, reference, fieldKey, operator string, values []string, args *[]any) string {
	prepared, err := prepareScopeComparison(s, query.QualifiedColumn(reference, fieldKey), operator, values, args)
	if err != nil {
		return ""
	}
	return prepared
}

func prepareScopeComparison(s Store, column query.Expression, operator string, values []string, args *[]any) (string, error) {
	predicate := scopeComparisonPredicate(column, operator, values)
	prepared, bound, err := query.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return "", err
	}
	*args = append(*args, bound...)
	return prepared, nil
}

func scopeComparisonPredicate(column query.Expression, operator string, values []string) query.Predicate {
	if operator == "exists" {
		return query.IsNotNullExpression(column)
	}
	if operator == "not_exists" {
		return query.IsNullExpression(column)
	}
	if len(values) == 0 {
		return query.AlwaysFalse()
	}
	if operator == "starts_with" {
		return query.LikeValueEscaped(column, escapeLikePattern(values[0])+"%")
	}
	if operator == "prefix" {
		prefix := strings.TrimRight(strings.TrimSpace(values[0]), "/")
		if prefix == "" {
			return query.AlwaysFalse()
		}
		return query.Or(
			query.EqualValue(column, prefix),
			query.LikeValueEscaped(column, escapeLikePattern(prefix)+"/%"),
		)
	}
	if operator == "eq" {
		return query.EqualValue(column, values[0])
	}
	const permissionIDChunkSize = ScopeMembershipINThreshold
	chunks := make([]query.Predicate, 0, (len(values)+permissionIDChunkSize-1)/permissionIDChunkSize)
	for offset := 0; offset < len(values); offset += permissionIDChunkSize {
		end := offset + permissionIDChunkSize
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, query.InExpression(column, stringsToAny(values[offset:end])...))
	}
	if len(chunks) == 1 {
		return chunks[0]
	}
	return query.Or(chunks...)
}

func inAnyClause(s Store, field string, raw any, args *[]any) string {
	values := nonEmptyValues(raw)
	predicate := query.Predicate(query.AlwaysFalse())
	if len(values) > 0 {
		predicate = query.In(field, values...)
	}
	prepared, bound, err := query.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return ""
	}
	*args = append(*args, bound...)
	return prepared
}

func inClause(s Store, field string, values []string, args *[]any) string {
	values = nonBlankStrings(values)
	predicate := query.Predicate(query.AlwaysFalse())
	if len(values) > 0 {
		predicate = query.In(field, stringsToAny(values)...)
	}
	prepared, bound, err := query.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return ""
	}
	*args = append(*args, bound...)
	return prepared
}

func BuildOrder(s Store, queryValue recordmodel.RecordListQuery) string {
	orders := make([]query.Order, 0, len(queryValue.Sort)+1)
	hasStableID := false
	for _, rule := range queryValue.Sort {
		direction := strings.ToUpper(strings.TrimSpace(rule.Direction))
		if direction == "DESC" {
			orders = append(orders, query.Descending(rule.Field))
		} else {
			orders = append(orders, query.Ascending(rule.Field))
		}
		hasStableID = hasStableID || strings.TrimSpace(rule.Field) == "id"
	}
	if len(orders) == 0 {
		orders = append(orders, query.Ascending("id"))
	} else if !hasStableID {
		orders = append(orders, query.Ascending("id"))
	}
	prepared, err := query.PrepareOrderBy(storeRenderer{s}, orders...)
	if err != nil {
		return ""
	}
	return " " + prepared
}

func splitFilterKey(key string) (string, string, bool) {
	for _, operator := range []string{"gte", "lte", "in"} {
		suffix := "__" + operator
		if strings.HasSuffix(key, suffix) {
			return strings.TrimSuffix(key, suffix), operator, true
		}
	}
	return key, "", false
}

func escapeLikePattern(value string) string {
	value = strings.ReplaceAll(value, "~", "~~")
	value = strings.ReplaceAll(value, "%", "~%")
	value = strings.ReplaceAll(value, "_", "~_")
	return value
}
