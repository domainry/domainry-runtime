package query

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const ScopeMembershipINThreshold = 1000

type ScopeMembershipLookup func(statement string, args ...any) ([]string, error)

type CandidateScopeLookup func(statement string, args ...any) (bool, error)

func ResolveScopeMembership(s Store, workspace string, expression recordmodel.RecordScopeExpression, inThreshold int, lookup ScopeMembershipLookup) (recordmodel.RecordScopeExpression, error) {
	if inThreshold < 1 {
		return recordmodel.RecordScopeExpression{}, fmt.Errorf("scope membership IN threshold must be positive")
	}
	return resolveScopeMembership(s, workspace, expression, inThreshold, lookup)
}

func resolveScopeMembership(s Store, workspace string, expression recordmodel.RecordScopeExpression, inThreshold int, lookup ScopeMembershipLookup) (recordmodel.RecordScopeExpression, error) {
	switch expression.Operator {
	case "and", "or", "not":
		children := make([]recordmodel.RecordScopeExpression, 0, len(expression.Children))
		for _, child := range expression.Children {
			resolved, err := resolveScopeMembership(s, workspace, child, inThreshold, lookup)
			if err != nil {
				return recordmodel.RecordScopeExpression{}, err
			}
			children = append(children, resolved)
		}
		expression.Children = children
		return expression, nil
	case "eq", "in", "prefix", "starts_with", "exists", "not_exists":
		if len(expression.Path) == 0 {
			return expression, nil
		}
		if len(expression.Values) == 0 {
			return resolvedMembershipLeaf(expression, nil), nil
		}
		statement, args, err := buildScopeMembershipLookup(s, workspace, expression, inThreshold+1)
		if err != nil {
			return recordmodel.RecordScopeExpression{}, err
		}
		values, err := lookup(statement, args...)
		if err != nil {
			return recordmodel.RecordScopeExpression{}, fmt.Errorf("resolve scope membership: %w", err)
		}
		if len(values) > inThreshold {
			expression.RelationExists = true
			return expression, nil
		}
		return resolvedMembershipLeaf(expression, values), nil
	default:
		return recordmodel.RecordScopeExpression{}, fmt.Errorf("unsupported compiled scope operator %q", expression.Operator)
	}
}

func ScopeExpressionHasRelation(expression recordmodel.RecordScopeExpression) bool {
	if len(expression.Path) > 0 {
		return true
	}
	for _, child := range expression.Children {
		if ScopeExpressionHasRelation(child) {
			return true
		}
	}
	return false
}

// PreserveScopeRelations returns a deep copy whose relation-path leaves are
// evaluated as correlated EXISTS predicates. Cross-Workspace reads cannot
// resolve membership once against the caller's Workspace and reuse it for a
// different Workspace.
func PreserveScopeRelations(expression recordmodel.RecordScopeExpression) recordmodel.RecordScopeExpression {
	result := expression
	result.Values = append([]string(nil), expression.Values...)
	result.Path = append([]recordmodel.RecordScopePathSegment(nil), expression.Path...)
	result.Children = make([]recordmodel.RecordScopeExpression, len(expression.Children))
	for index, child := range expression.Children {
		result.Children[index] = PreserveScopeRelations(child)
	}
	if len(result.Path) > 0 {
		result.RelationExists = true
	}
	return result
}

func CandidateScopeMatches(s Store, workspace string, candidate recordmodel.Record, expression recordmodel.RecordScopeExpression, lookup CandidateScopeLookup) (bool, error) {
	switch expression.Operator {
	case "and":
		if len(expression.Children) < 2 {
			return false, fmt.Errorf("candidate scope and requires at least two children")
		}
		for _, child := range expression.Children {
			matched, err := CandidateScopeMatches(s, workspace, candidate, child, lookup)
			if err != nil || !matched {
				return matched, err
			}
		}
		return true, nil
	case "or":
		if len(expression.Children) < 2 {
			return false, fmt.Errorf("candidate scope or requires at least two children")
		}
		for _, child := range expression.Children {
			matched, err := CandidateScopeMatches(s, workspace, candidate, child, lookup)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	case "not":
		if len(expression.Children) != 1 {
			return false, fmt.Errorf("candidate scope not requires exactly one child")
		}
		matched, err := CandidateScopeMatches(s, workspace, candidate, expression.Children[0], lookup)
		return !matched, err
	case "eq", "in", "prefix", "starts_with", "exists", "not_exists":
		if len(expression.Path) == 0 {
			return candidateScopeDirectMatch(candidate, expression), nil
		}
		return candidateRelationScopeMatches(s, workspace, candidate, expression, lookup)
	default:
		return false, fmt.Errorf("unsupported compiled scope operator %q", expression.Operator)
	}
}

func candidateRelationScopeMatches(s Store, workspace string, candidate recordmodel.Record, expression recordmodel.RecordScopeExpression, lookup CandidateScopeLookup) (bool, error) {
	if len(expression.Values) == 0 {
		return false, nil
	}
	first := expression.Path[0]
	anchor := candidate.ID
	anchorField := first.RelationFieldKey
	if first.Direction == "forward" {
		relationID, exists := candidate.Data[first.RelationFieldKey]
		if !exists || relationID == nil {
			return false, nil
		}
		anchorField = "id"
		anchor = strings.TrimSpace(fmt.Sprint(relationID))
	} else if first.Direction != "reverse" {
		return false, fmt.Errorf("unsupported compiled relation direction %q", first.Direction)
	}
	if strings.TrimSpace(anchor) == "" {
		return false, nil
	}
	statement, args, err := buildScopeMembershipLookupWithAnchor(s, workspace, expression, 1, anchorField, anchor)
	if err != nil {
		return false, err
	}
	matched, err := lookup(statement, args...)
	if err != nil {
		return false, fmt.Errorf("evaluate candidate scope membership: %w", err)
	}
	return matched, nil
}

func candidateScopeDirectMatch(candidate recordmodel.Record, expression recordmodel.RecordScopeExpression) bool {
	var raw any = candidate.ID
	exists := strings.TrimSpace(candidate.ID) != ""
	if expression.FieldKey != "id" {
		raw, exists = candidate.Data[expression.FieldKey]
	}
	if expression.Operator == "exists" {
		return exists && raw != nil
	}
	if expression.Operator == "not_exists" {
		return !exists || raw == nil
	}
	value := strings.TrimSpace(fmt.Sprint(raw))
	if expression.Operator == "starts_with" {
		return len(expression.Values) == 1 && strings.HasPrefix(value, expression.Values[0])
	}
	if expression.Operator == "prefix" {
		return len(expression.Values) == 1 && (value == strings.TrimRight(expression.Values[0], "/") || strings.HasPrefix(value, strings.TrimRight(expression.Values[0], "/")+"/"))
	}
	for _, allowed := range expression.Values {
		if value == strings.TrimSpace(allowed) {
			return true
		}
	}
	return false
}

func resolvedMembershipLeaf(expression recordmodel.RecordScopeExpression, values []string) recordmodel.RecordScopeExpression {
	first := expression.Path[0]
	fieldKey := "id"
	if first.Direction == "forward" {
		fieldKey = first.RelationFieldKey
	}
	return recordmodel.RecordScopeExpression{Operator: "in", FieldKey: fieldKey, Values: uniqueScopeIDs(values)}
}

func buildScopeMembershipLookup(s Store, workspace string, expression recordmodel.RecordScopeExpression, limit int) (string, []any, error) {
	return buildScopeMembershipLookupWithAnchor(s, workspace, expression, limit, "", nil)
}

func buildScopeMembershipLookupWithAnchor(s Store, workspace string, expression recordmodel.RecordScopeExpression, limit int, candidateAnchorField string, candidateAnchor any) (string, []any, error) {
	first := expression.Path[0]
	firstAlias := "permission_0"
	anchorColumn := query.QualifiedColumn(firstAlias, "id")
	if first.Direction == "reverse" {
		anchorColumn = query.QualifiedColumn(firstAlias, first.RelationFieldKey)
	} else if first.Direction != "forward" {
		return "", nil, fmt.Errorf("unsupported compiled relation direction %q", first.Direction)
	}
	lastIndex := len(expression.Path) - 1
	lastAlias := fmt.Sprintf("permission_%d", lastIndex)
	joins := make([]query.Join, 0, len(expression.Path)-1)
	relationConditions := make([]query.Predicate, 0, 2*(len(expression.Path)-1))
	currentReference := lastAlias
	for pathIndex := lastIndex; pathIndex > 0; pathIndex-- {
		segment := expression.Path[pathIndex]
		previousAlias := fmt.Sprintf("permission_%d", pathIndex-1)
		previousColumn := func(field string) query.Expression { return query.QualifiedColumn(previousAlias, field) }
		currentColumn := func(field string) query.Expression { return query.QualifiedColumn(currentReference, field) }
		var relation query.Predicate
		switch segment.Direction {
		case "forward":
			relation = query.EqualExpressions(previousColumn(segment.RelationFieldKey), currentColumn("id"))
		case "reverse":
			relation = query.EqualExpressions(previousColumn("id"), currentColumn(segment.RelationFieldKey))
		default:
			return "", nil, fmt.Errorf("unsupported compiled relation direction %q", segment.Direction)
		}

		joins = append(joins, query.CrossJoin(expression.Path[pathIndex-1].TargetObjectKey, previousAlias))
		relationConditions = append(relationConditions,
			query.EqualExpressions(previousColumn("workspace_id"), currentColumn("workspace_id")), relation,
		)
		currentReference = previousAlias
	}
	conditions := []query.Predicate{
		query.EqualValue(query.QualifiedColumn(lastAlias, "workspace_id"), workspace),
		query.IsNotNullExpression(anchorColumn),
		scopeComparisonPredicate(query.QualifiedColumn(lastAlias, expression.FieldKey), expression.Operator, expression.Values),
	}
	conditions = append(conditions, relationConditions...)
	if strings.TrimSpace(candidateAnchorField) != "" {
		conditions = append(conditions, query.EqualValue(query.QualifiedColumn(firstAlias, candidateAnchorField), candidateAnchor))
	}
	return query.NewSelectBuilder(storeRenderer{s}, expression.Path[lastIndex].TargetObjectKey).Alias(lastAlias).
		Distinct().Projections(query.Project(anchorColumn)).Join(joins...).Where(query.And(conditions...)).Limit(limit).Build()
}

func uniqueScopeIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func buildScopeRelationExists(s Store, rootTable string, expression recordmodel.RecordScopeExpression, args *[]any) (string, error) {
	predicate, err := scopeRelationExistsPredicate(s, rootTable, expression)
	if err != nil {
		return "", err
	}
	prepared, bound, err := query.PreparePredicate(storeRenderer{s}, predicate, len(*args))
	if err != nil {
		return "", err
	}
	*args = append(*args, bound...)
	return prepared, nil
}

func scopeRelationExistsPredicate(s Store, rootTable string, expression recordmodel.RecordScopeExpression) (query.Predicate, error) {
	if len(expression.Path) == 0 {
		return nil, fmt.Errorf("relation EXISTS requires a relation path")
	}
	first := expression.Path[0]
	firstAlias := "permission_0"
	anchorColumn := query.QualifiedColumn(firstAlias, "id")
	rootAnchor := query.TableColumn(rootTable, "id")
	if first.Direction == "forward" {
		rootAnchor = query.TableColumn(rootTable, first.RelationFieldKey)
	} else if first.Direction == "reverse" {
		anchorColumn = query.QualifiedColumn(firstAlias, first.RelationFieldKey)
	} else {
		return nil, fmt.Errorf("unsupported compiled relation direction %q", first.Direction)
	}
	lastIndex := len(expression.Path) - 1
	lastAlias := fmt.Sprintf("permission_%d", lastIndex)
	joins := make([]query.Join, 0, len(expression.Path)-1)
	relationConditions := make([]query.Predicate, 0, 2*len(expression.Path))
	currentReference := lastAlias
	for pathIndex := lastIndex; pathIndex > 0; pathIndex-- {
		segment := expression.Path[pathIndex]
		previousAlias := fmt.Sprintf("permission_%d", pathIndex-1)
		previousColumn := func(field string) query.Expression { return query.QualifiedColumn(previousAlias, field) }
		currentColumn := func(field string) query.Expression { return query.QualifiedColumn(currentReference, field) }
		var relation query.Predicate
		switch segment.Direction {
		case "forward":
			relation = query.EqualExpressions(previousColumn(segment.RelationFieldKey), currentColumn("id"))
		case "reverse":
			relation = query.EqualExpressions(previousColumn("id"), currentColumn(segment.RelationFieldKey))
		default:
			return nil, fmt.Errorf("unsupported compiled relation direction %q", segment.Direction)
		}
		joins = append(joins, query.CrossJoin(expression.Path[pathIndex-1].TargetObjectKey, previousAlias))
		relationConditions = append(relationConditions,
			query.EqualExpressions(previousColumn("workspace_id"), currentColumn("workspace_id")), relation,
		)
		currentReference = previousAlias
	}
	conditions := []query.Predicate{
		query.EqualExpressions(query.QualifiedColumn(lastAlias, "workspace_id"), query.TableColumn(rootTable, "workspace_id")),
		query.EqualExpressions(anchorColumn, rootAnchor),
		scopeComparisonPredicate(query.QualifiedColumn(lastAlias, expression.FieldKey), expression.Operator, expression.Values),
	}
	conditions = append(conditions, relationConditions...)
	subquery := query.NewSelectBuilder(storeRenderer{s}, expression.Path[lastIndex].TargetObjectKey).Alias(lastAlias).
		Projections(query.Project(query.AllColumns())).Join(joins...).Where(query.And(conditions...))
	return query.ExistsSubquery(subquery), nil
}
