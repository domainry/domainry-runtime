package query

import (
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	"sort"
	"strings"

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

// BuildTenantWhere is the only supported WHERE builder for tenant-owned record
// tables. It rejects an absent workspace and always owns the workspace filter,
// so caller-provided filters cannot remove or replace the tenant boundary.
func BuildTenantWhere(s Store, workspace string, query recordmodel.RecordListQuery) (string, []any, error) {
	workspaceID, err := principalmodel.NewWorkspaceID(workspace)
	if err != nil {
		return "", nil, fmt.Errorf("tenant query workspace: %w", err)
	}
	workspace = workspaceID.String()
	query.PrincipalWorkspaceID = workspace
	clauses := []string{s.Identifier("workspace_id") + " = " + s.Placeholder(1)}
	args := []any{workspace}
	scope := strings.TrimSpace(query.Scope)
	if scope == "" {
		scope = "all_records"
	}
	if !map[string]bool{"all_records": true, "owned_records": true, "team": true, "department": true, "department_and_children": true, "subordinates": true, "custom": true, "none": true}[scope] {
		return "", nil, fmt.Errorf("unsupported data scope %q", scope)
	}
	if scope == "none" {
		clauses = append(clauses, "1 = 0")
	}
	if scope == "owned_records" && strings.TrimSpace(query.OwnerField) != "" {
		args = append(args, query.PrincipalUserID)
		clauses = append(clauses, s.Identifier(query.OwnerField)+" = "+s.Placeholder(len(args)))
	}
	if scope == "subordinates" && strings.TrimSpace(query.OwnerField) != "" {
		clauses = append(clauses, inClause(s, query.OwnerField, query.PrincipalReportingUserIDs, &args))
	}
	if scope == "department" && strings.TrimSpace(query.DepartmentPathField) != "" && strings.TrimSpace(query.PrincipalDepartmentPath) != "" {
		args = append(args, query.PrincipalDepartmentPath)
		clauses = append(clauses, s.Identifier(query.DepartmentPathField)+" = "+s.Placeholder(len(args)))
	}
	if scope == "department_and_children" && strings.TrimSpace(query.DepartmentPathField) != "" && strings.TrimSpace(query.PrincipalDepartmentPath) != "" {
		departmentPath := strings.TrimRight(query.PrincipalDepartmentPath, "/")
		args = append(args, departmentPath)
		exactPlaceholder := s.Placeholder(len(args))
		args = append(args, escapeLikePattern(departmentPath)+"/%")
		childPlaceholder := s.Placeholder(len(args))
		clauses = append(clauses, "("+s.Identifier(query.DepartmentPathField)+" = "+exactPlaceholder+" OR "+s.Identifier(query.DepartmentPathField)+" LIKE "+childPlaceholder+" ESCAPE '~')")
	}
	if scope == "team" && strings.TrimSpace(query.TeamField) != "" && len(query.PrincipalTeamIDs) > 0 {
		clauses = append(clauses, inClause(s, query.TeamField, query.PrincipalTeamIDs, &args))
	}
	if scope == "custom" {
		if query.ScopeExpression == nil || strings.TrimSpace(query.RootObjectKey) == "" {
			return "", nil, fmt.Errorf("custom scope requires compiled expression")
		}
		compiled, err := buildScopeExpression(s, query.RootObjectKey, *query.ScopeExpression, &args, 0)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, compiled)
	}
	if strings.TrimSpace(query.Search) != "" && len(query.SearchFields) > 0 {
		searchParts := []string{}
		searchValue := "%" + strings.ToLower(strings.TrimSpace(query.Search)) + "%"
		for _, field := range query.SearchFields {
			args = append(args, searchValue)
			searchParts = append(searchParts, "LOWER("+s.Identifier(field)+") LIKE "+s.Placeholder(len(args)))
		}
		clauses = append(clauses, "("+strings.Join(searchParts, " OR ")+")")
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
				clauses = append(clauses, inAnyClause(s, baseKey, value, &args))
				continue
			}
			args = append(args, dbValue(value))
			comparator := "="
			if operator == "gte" {
				comparator = ">="
			}
			if operator == "lte" {
				comparator = "<="
			}
			clauses = append(clauses, s.Identifier(baseKey)+" "+comparator+" "+s.Placeholder(len(args)))
		} else {
			args = append(args, dbValue(value))
			clauses = append(clauses, s.Identifier(key)+" = "+s.Placeholder(len(args)))
		}
	}
	if query.FilterExpression != nil {
		compiled, err := buildRecordFilterExpression(s, *query.FilterExpression, &args, 0)
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, compiled)
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}

func buildRecordFilterExpression(s Store, expression recordmodel.RecordFilterExpression, args *[]any, depth int) (string, error) {
	if depth > 16 {
		return "", fmt.Errorf("record filter exceeds maximum depth")
	}
	switch expression.Operator {
	case "and", "or":
		if len(expression.Children) < 2 {
			return "", fmt.Errorf("record filter %s requires at least two children", expression.Operator)
		}
		parts := make([]string, 0, len(expression.Children))
		for _, child := range expression.Children {
			part, err := buildRecordFilterExpression(s, child, args, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		return "(" + strings.Join(parts, " "+strings.ToUpper(expression.Operator)+" ") + ")", nil
	case "not":
		if len(expression.Children) != 1 {
			return "", fmt.Errorf("record filter not requires exactly one child")
		}
		part, err := buildRecordFilterExpression(s, expression.Children[0], args, depth+1)
		if err != nil {
			return "", err
		}
		return "NOT (" + part + ")", nil
	case "eq", "ne", "gt", "gte", "lt", "lte":
		operator := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[expression.Operator]
		*args = append(*args, dbValue(expression.Value))
		return s.Identifier(expression.Field) + " " + operator + " " + s.Placeholder(len(*args)), nil
	case "in", "not_in":
		placeholders := make([]string, 0, len(expression.Values))
		for _, value := range expression.Values {
			*args = append(*args, dbValue(value))
			placeholders = append(placeholders, s.Placeholder(len(*args)))
		}
		if len(placeholders) == 0 {
			return "", fmt.Errorf("record filter %s requires values", expression.Operator)
		}
		operator := "IN"
		if expression.Operator == "not_in" {
			operator = "NOT IN"
		}
		return s.Identifier(expression.Field) + " " + operator + " (" + strings.Join(placeholders, ", ") + ")", nil
	case "is_null":
		return s.Identifier(expression.Field) + " IS NULL", nil
	case "is_not_null":
		return s.Identifier(expression.Field) + " IS NOT NULL", nil
	default:
		return "", fmt.Errorf("unsupported record filter operator %q", expression.Operator)
	}
}

func buildScopeExpression(s Store, rootObjectKey string, expression recordmodel.RecordScopeExpression, args *[]any, depth int) (string, error) {
	if depth > 16 {
		return "", fmt.Errorf("scope expression exceeds maximum depth")
	}
	switch expression.Operator {
	case "and", "or":
		if len(expression.Children) < 2 {
			return "", fmt.Errorf("scope %s requires at least two children", expression.Operator)
		}
		parts := make([]string, 0, len(expression.Children))
		for _, child := range expression.Children {
			part, err := buildScopeExpression(s, rootObjectKey, child, args, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, part)
		}
		return "(" + strings.Join(parts, " "+strings.ToUpper(expression.Operator)+" ") + ")", nil
	case "not":
		if len(expression.Children) != 1 {
			return "", fmt.Errorf("scope not requires one child")
		}
		part, err := buildScopeExpression(s, rootObjectKey, expression.Children[0], args, depth+1)
		if err != nil {
			return "", err
		}
		return "NOT (" + part + ")", nil
	case "eq", "in", "prefix", "starts_with", "exists", "not_exists":
		if len(expression.Path) != 0 {
			if !expression.RelationExists {
				return "", fmt.Errorf("scope relation path must be resolved to permission IDs before building record query")
			}
			return buildScopeRelationExists(s, s.TableIdentifier(rootObjectKey), expression, args)
		}
		rootReference := s.TableIdentifier(rootObjectKey)
		return buildScopeComparison(s, rootReference, expression.FieldKey, expression.Operator, expression.Values, args), nil
	default:
		return "", fmt.Errorf("unsupported compiled scope operator %q", expression.Operator)
	}
}

func buildScopeComparison(s Store, reference, fieldKey, operator string, values []string, args *[]any) string {
	column := reference + "." + s.Identifier(fieldKey)
	if operator == "exists" {
		return column + " IS NOT NULL"
	}
	if operator == "not_exists" {
		return column + " IS NULL"
	}
	if len(values) == 0 {
		return "1 = 0"
	}
	if operator == "starts_with" {
		*args = append(*args, escapeLikePattern(values[0])+"%")
		return column + " LIKE " + s.Placeholder(len(*args)) + " ESCAPE '~'"
	}
	if operator == "prefix" {
		prefix := strings.TrimRight(strings.TrimSpace(values[0]), "/")
		if prefix == "" {
			return "1 = 0"
		}
		*args = append(*args, prefix)
		exact := s.Placeholder(len(*args))
		*args = append(*args, escapeLikePattern(prefix)+"/%")
		children := s.Placeholder(len(*args))
		return "(" + column + " = " + exact + " OR " + column + " LIKE " + children + " ESCAPE '~')"
	}
	if operator == "eq" {
		*args = append(*args, values[0])
		return column + " = " + s.Placeholder(len(*args))
	}
	const permissionIDChunkSize = ScopeMembershipINThreshold
	chunks := make([]string, 0, (len(values)+permissionIDChunkSize-1)/permissionIDChunkSize)
	for offset := 0; offset < len(values); offset += permissionIDChunkSize {
		end := offset + permissionIDChunkSize
		if end > len(values) {
			end = len(values)
		}
		placeholders := make([]string, 0, end-offset)
		for _, value := range values[offset:end] {
			*args = append(*args, value)
			placeholders = append(placeholders, s.Placeholder(len(*args)))
		}
		chunks = append(chunks, column+" IN ("+strings.Join(placeholders, ", ")+")")
	}
	if len(chunks) == 1 {
		return chunks[0]
	}
	return "(" + strings.Join(chunks, " OR ") + ")"
}

func inAnyClause(s Store, field string, raw any, args *[]any) string {
	values := []any{}
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []string:
		for _, value := range typed {
			values = append(values, value)
		}
	}
	placeholders := make([]string, 0, len(values))
	for _, value := range values {
		if recordvalidation.RecordIsEmptyValue(value) {
			continue
		}
		*args = append(*args, dbValue(value))
		placeholders = append(placeholders, s.Placeholder(len(*args)))
	}
	if len(placeholders) == 0 {
		return "1 = 0"
	}
	return s.Identifier(field) + " IN (" + strings.Join(placeholders, ", ") + ")"
}

func inClause(s Store, field string, values []string, args *[]any) string {
	placeholders := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		*args = append(*args, value)
		placeholders = append(placeholders, s.Placeholder(len(*args)))
	}
	if len(placeholders) == 0 {
		return "1 = 0"
	}
	return s.Identifier(field) + " IN (" + strings.Join(placeholders, ", ") + ")"
}

func BuildOrder(s Store, query recordmodel.RecordListQuery) string {
	parts := []string{}
	hasStableID := false
	for _, rule := range query.Sort {
		direction := strings.ToUpper(strings.TrimSpace(rule.Direction))
		if direction != "DESC" {
			direction = "ASC"
		}
		parts = append(parts, s.Identifier(rule.Field)+" "+direction)
		hasStableID = hasStableID || strings.TrimSpace(rule.Field) == "id"
	}
	if len(parts) == 0 {
		parts = append(parts, s.Identifier("id")+" ASC")
	} else if !hasStableID {
		parts = append(parts, s.Identifier("id")+" ASC")
	}
	return " ORDER BY " + strings.Join(parts, ", ")
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
