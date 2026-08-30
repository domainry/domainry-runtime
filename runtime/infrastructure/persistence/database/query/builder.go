package query

import (
	"fmt"
	"sort"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
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

// BuildTenantPredicate is the only supported predicate builder for tenant-owned
// record tables. It rejects an absent workspace and always owns the workspace
// filter, so caller-provided filters cannot remove or replace that boundary.
func BuildTenantPredicate(s Store, workspace string, query recordmodel.RecordListQuery) (ormbuilder.Predicate, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(workspace)
	if err != nil {
		return nil, fmt.Errorf("tenant query workspace: %w", err)
	}
	workspace = workspaceID.String()
	query.PrincipalWorkspaceID = workspace
	predicates := []ormbuilder.Predicate{ormbuilder.Equal("workspace_id", workspace)}
	appendPredicate := func(predicate ormbuilder.Predicate) { predicates = append(predicates, predicate) }
	scope := strings.TrimSpace(query.Scope)
	if scope == "" {
		scope = "all_records"
	}
	if !map[string]bool{"all_records": true, "owned_records": true, "team": true, "department": true, "department_and_children": true, "subordinates": true, "custom": true, "none": true}[scope] {
		return nil, fmt.Errorf("unsupported data scope %q", scope)
	}
	if scope == "none" {
		appendPredicate(ormbuilder.AlwaysFalse())
	}
	if scope == "owned_records" && strings.TrimSpace(query.OwnerField) != "" {
		appendPredicate(ormbuilder.Equal(query.OwnerField, query.PrincipalUserID))
	}
	if scope == "subordinates" && strings.TrimSpace(query.OwnerField) != "" {
		values := nonBlankStrings(query.PrincipalReportingUserIDs)
		if len(values) == 0 {
			appendPredicate(ormbuilder.AlwaysFalse())
		} else {
			appendPredicate(ormbuilder.In(query.OwnerField, stringsToAny(values)...))
		}
	}
	if scope == "department" && strings.TrimSpace(query.DepartmentPathField) != "" && strings.TrimSpace(query.PrincipalDepartmentPath) != "" {
		appendPredicate(ormbuilder.Equal(query.DepartmentPathField, query.PrincipalDepartmentPath))
	}
	if scope == "department_and_children" && strings.TrimSpace(query.DepartmentPathField) != "" && strings.TrimSpace(query.PrincipalDepartmentPath) != "" {
		departmentPath := strings.TrimRight(query.PrincipalDepartmentPath, "/")
		appendPredicate(ormbuilder.Or(
			ormbuilder.Equal(query.DepartmentPathField, departmentPath),
			ormbuilder.LikeEscaped(query.DepartmentPathField, escapeLikePattern(departmentPath)+"/%"),
		))
	}
	if scope == "team" && strings.TrimSpace(query.TeamField) != "" && len(query.PrincipalTeamIDs) > 0 {
		values := nonBlankStrings(query.PrincipalTeamIDs)
		if len(values) == 0 {
			appendPredicate(ormbuilder.AlwaysFalse())
		} else {
			appendPredicate(ormbuilder.In(query.TeamField, stringsToAny(values)...))
		}
	}
	if scope == "custom" {
		if query.ScopeExpression == nil || strings.TrimSpace(query.RootObjectKey) == "" {
			return nil, fmt.Errorf("custom scope requires compiled expression")
		}
		compiled, err := scopeExpressionPredicate(s, query.RootObjectKey, *query.ScopeExpression, 0)
		if err != nil {
			return nil, err
		}
		appendPredicate(compiled)
	}
	if strings.TrimSpace(query.Search) != "" && len(query.SearchFields) > 0 {
		searchPredicates := make([]ormbuilder.Predicate, 0, len(query.SearchFields))
		searchValue := "%" + strings.ToLower(strings.TrimSpace(query.Search)) + "%"
		for _, field := range query.SearchFields {
			searchPredicates = append(searchPredicates, ormbuilder.LikeValue(ormbuilder.Lower(ormbuilder.Column(field)), searchValue))
		}
		appendPredicate(ormbuilder.Or(searchPredicates...))
	}
	filterKeys := make([]string, 0, len(query.Filters))
	for key := range query.Filters {
		filterKeys = append(filterKeys, key)
	}
	sort.Strings(filterKeys)
	for _, key := range filterKeys {
		if key == "workspace_id" {
			continue
		}
		value := query.Filters[key]
		if recordvalidation.RecordIsEmptyValue(value) {
			continue
		}
		if baseKey, operator, ok := splitFilterKey(key); ok {
			if operator == "in" {
				values := nonEmptyValues(value)
				if len(values) == 0 {
					appendPredicate(ormbuilder.AlwaysFalse())
				} else {
					appendPredicate(ormbuilder.In(baseKey, values...))
				}
				continue
			}
			predicate := ormbuilder.Equal(baseKey, dbValue(value))
			if operator == "gte" {
				predicate = ormbuilder.GreaterThanOrEqual(baseKey, dbValue(value))
			} else if operator == "lte" {
				predicate = ormbuilder.LessThanOrEqual(baseKey, dbValue(value))
			}
			appendPredicate(predicate)
		} else {
			appendPredicate(ormbuilder.Equal(key, dbValue(value)))
		}
	}
	if query.FilterExpression != nil {
		predicate, err := recordFilterPredicate(*query.FilterExpression, 0)
		if err != nil {
			return nil, err
		}
		appendPredicate(predicate)
	}
	return ormbuilder.And(predicates...), nil
}

// BuildTenantWhere is retained as a compatibility seam for callers that have
// not yet moved to SelectBuilder. New repositories should use the predicate.
func BuildTenantWhere(s Store, workspace string, query recordmodel.RecordListQuery) (string, []any, error) {
	predicate, err := BuildTenantPredicate(s, workspace, query)
	if err != nil {
		return "", nil, err
	}
	prepared, args, err := ormbuilder.PreparePredicate(storeRenderer{s}, predicate, 0)
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
	prepared, values, err := ormbuilder.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return "", err
	}
	*args = append(*args, values...)
	return prepared, nil
}

func recordFilterPredicate(expression recordmodel.RecordFilterExpression, depth int) (ormbuilder.Predicate, error) {
	if depth > 16 {
		return nil, fmt.Errorf("record filter exceeds maximum depth")
	}
	switch expression.Operator {
	case "and", "or":
		if len(expression.Children) < 2 {
			return nil, fmt.Errorf("record filter %s requires at least two children", expression.Operator)
		}
		predicates := make([]ormbuilder.Predicate, 0, len(expression.Children))
		for _, child := range expression.Children {
			predicate, err := recordFilterPredicate(child, depth+1)
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, predicate)
		}
		if expression.Operator == "and" {
			return ormbuilder.And(predicates...), nil
		}
		return ormbuilder.Or(predicates...), nil
	case "not":
		if len(expression.Children) != 1 {
			return nil, fmt.Errorf("record filter not requires exactly one child")
		}
		predicate, err := recordFilterPredicate(expression.Children[0], depth+1)
		if err != nil {
			return nil, err
		}
		return ormbuilder.Not(predicate), nil
	case "eq", "ne", "gt", "gte", "lt", "lte":
		value := dbValue(expression.Value)
		switch expression.Operator {
		case "eq":
			return ormbuilder.Equal(expression.Field, value), nil
		case "ne":
			return ormbuilder.NotEqual(expression.Field, value), nil
		case "gt":
			return ormbuilder.GreaterThan(expression.Field, value), nil
		case "gte":
			return ormbuilder.GreaterThanOrEqual(expression.Field, value), nil
		case "lt":
			return ormbuilder.LessThan(expression.Field, value), nil
		default:
			return ormbuilder.LessThanOrEqual(expression.Field, value), nil
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
			return ormbuilder.NotIn(expression.Field, values...), nil
		}
		return ormbuilder.In(expression.Field, values...), nil
	case "is_null":
		return ormbuilder.IsNull(expression.Field), nil
	case "is_not_null":
		return ormbuilder.IsNotNull(expression.Field), nil
	default:
		return nil, fmt.Errorf("unsupported record filter operator %q", expression.Operator)
	}
}

func buildScopeExpression(s Store, rootObjectKey string, expression recordmodel.RecordScopeExpression, args *[]any, depth int) (string, error) {
	predicate, err := scopeExpressionPredicate(s, rootObjectKey, expression, depth)
	if err != nil {
		return "", err
	}
	prepared, values, err := ormbuilder.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return "", err
	}
	*args = append(*args, values...)
	return prepared, nil
}

func scopeExpressionPredicate(s Store, rootObjectKey string, expression recordmodel.RecordScopeExpression, depth int) (ormbuilder.Predicate, error) {
	if depth > 16 {
		return nil, fmt.Errorf("scope expression exceeds maximum depth")
	}
	switch expression.Operator {
	case "and", "or":
		if len(expression.Children) < 2 {
			return nil, fmt.Errorf("scope %s requires at least two children", expression.Operator)
		}
		predicates := make([]ormbuilder.Predicate, 0, len(expression.Children))
		for _, child := range expression.Children {
			predicate, err := scopeExpressionPredicate(s, rootObjectKey, child, depth+1)
			if err != nil {
				return nil, err
			}
			predicates = append(predicates, predicate)
		}
		if expression.Operator == "and" {
			return ormbuilder.And(predicates...), nil
		}
		return ormbuilder.Or(predicates...), nil
	case "not":
		if len(expression.Children) != 1 {
			return nil, fmt.Errorf("scope not requires one child")
		}
		predicate, err := scopeExpressionPredicate(s, rootObjectKey, expression.Children[0], depth+1)
		if err != nil {
			return nil, err
		}
		return ormbuilder.Not(predicate), nil
	case "eq", "in", "prefix", "starts_with", "exists", "not_exists":
		if len(expression.Path) != 0 {
			if !expression.RelationExists {
				return nil, fmt.Errorf("scope relation path must be resolved to permission IDs before building record query")
			}
			return scopeRelationExistsPredicate(s, rootObjectKey, expression)
		}
		return scopeComparisonPredicate(ormbuilder.TableColumn(rootObjectKey, expression.FieldKey), expression.Operator, expression.Values), nil
	default:
		return nil, fmt.Errorf("unsupported compiled scope operator %q", expression.Operator)
	}
}

func buildScopeComparison(s Store, reference, fieldKey, operator string, values []string, args *[]any) string {
	prepared, err := prepareScopeComparison(s, ormbuilder.QualifiedColumn(reference, fieldKey), operator, values, args)
	if err != nil {
		return ""
	}
	return prepared
}

func prepareScopeComparison(s Store, column ormbuilder.Expression, operator string, values []string, args *[]any) (string, error) {
	predicate := scopeComparisonPredicate(column, operator, values)
	prepared, bound, err := ormbuilder.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return "", err
	}
	*args = append(*args, bound...)
	return prepared, nil
}

func scopeComparisonPredicate(column ormbuilder.Expression, operator string, values []string) ormbuilder.Predicate {
	if operator == "exists" {
		return ormbuilder.IsNotNullExpression(column)
	}
	if operator == "not_exists" {
		return ormbuilder.IsNullExpression(column)
	}
	if len(values) == 0 {
		return ormbuilder.AlwaysFalse()
	}
	if operator == "starts_with" {
		return ormbuilder.LikeValueEscaped(column, escapeLikePattern(values[0])+"%")
	}
	if operator == "prefix" {
		prefix := strings.TrimRight(strings.TrimSpace(values[0]), "/")
		if prefix == "" {
			return ormbuilder.AlwaysFalse()
		}
		return ormbuilder.Or(
			ormbuilder.EqualValue(column, prefix),
			ormbuilder.LikeValueEscaped(column, escapeLikePattern(prefix)+"/%"),
		)
	}
	if operator == "eq" {
		return ormbuilder.EqualValue(column, values[0])
	}
	const permissionIDChunkSize = ScopeMembershipINThreshold
	chunks := make([]ormbuilder.Predicate, 0, (len(values)+permissionIDChunkSize-1)/permissionIDChunkSize)
	for offset := 0; offset < len(values); offset += permissionIDChunkSize {
		end := offset + permissionIDChunkSize
		if end > len(values) {
			end = len(values)
		}
		chunks = append(chunks, ormbuilder.InExpression(column, stringsToAny(values[offset:end])...))
	}
	if len(chunks) == 1 {
		return chunks[0]
	}
	return ormbuilder.Or(chunks...)
}

func inAnyClause(s Store, field string, raw any, args *[]any) string {
	values := nonEmptyValues(raw)
	predicate := ormbuilder.Predicate(ormbuilder.AlwaysFalse())
	if len(values) > 0 {
		predicate = ormbuilder.In(field, values...)
	}
	prepared, bound, err := ormbuilder.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return ""
	}
	*args = append(*args, bound...)
	return prepared
}

func inClause(s Store, field string, values []string, args *[]any) string {
	values = nonBlankStrings(values)
	predicate := ormbuilder.Predicate(ormbuilder.AlwaysFalse())
	if len(values) > 0 {
		predicate = ormbuilder.In(field, stringsToAny(values)...)
	}
	prepared, bound, err := ormbuilder.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return ""
	}
	*args = append(*args, bound...)
	return prepared
}

func BuildOrder(s Store, query recordmodel.RecordListQuery) string {
	orders := make([]ormbuilder.Order, 0, len(query.Sort)+1)
	hasStableID := false
	for _, rule := range query.Sort {
		direction := strings.ToUpper(strings.TrimSpace(rule.Direction))
		if direction == "DESC" {
			orders = append(orders, ormbuilder.Descending(rule.Field))
		} else {
			orders = append(orders, ormbuilder.Ascending(rule.Field))
		}
		hasStableID = hasStableID || strings.TrimSpace(rule.Field) == "id"
	}
	if len(orders) == 0 {
		orders = append(orders, ormbuilder.Ascending("id"))
	} else if !hasStableID {
		orders = append(orders, ormbuilder.Ascending("id"))
	}
	prepared, err := ormbuilder.PrepareOrderBy(storeRenderer{s}, orders...)
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
