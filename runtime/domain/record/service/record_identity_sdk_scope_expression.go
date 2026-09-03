package service

import (
	"fmt"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
)

func RecordCompileSDKDataScopeExpression(object definitionmodel.ObjectSchema, objects []definitionmodel.ObjectSchema, principal principalmodel.Principal, action string) (*recordmodel.RecordScopeExpression, error, bool) {
	if principal.AccessBundle == nil {
		return nil, nil, false
	}
	filter, err := identityevaluator.CompileRecordFilter(
		*principal.AccessBundle,
		identitysdk.ResourceType(strings.TrimSpace(object.Key)),
		identitysdk.Action(normalizeSDKScopeAction(action)),
		time.Now().UTC(),
	)
	if err != nil {
		return nil, err, true
	}
	objectMap := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, candidate := range objects {
		objectMap[candidate.Key] = candidate
	}
	evaluation := recordpolicy.RecordSDKEvaluationContext(principal)
	deny, err := compileSDKPredicateGroup(object, objectMap, evaluation, filter.Deny, "or")
	if err != nil {
		return nil, err, true
	}
	if filter.Unrestricted {
		if deny == nil {
			// Canonical `all` is the absence of an additional data-scope
			// predicate. Workspace and caller filters remain mandatory.
			return nil, nil, true
		}
		return &recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{*deny}}, nil, true
	}
	allow, err := compileSDKPredicateGroup(object, objectMap, evaluation, filter.Allow, "or")
	if err != nil {
		return nil, err, true
	}
	if allow == nil {
		return denyAllRecordScopeExpression(), nil, true
	}
	if deny == nil {
		return allow, nil, true
	}
	return &recordmodel.RecordScopeExpression{Operator: "and", Children: []recordmodel.RecordScopeExpression{
		*allow,
		{Operator: "not", Children: []recordmodel.RecordScopeExpression{*deny}},
	}}, nil, true
}

// RecordCompileSDKMutationScopeExpression preserves relation paths as
// correlated EXISTS predicates. Mutation storage uses this expression in the
// same UPDATE/DELETE statement as the candidate record ID.
func RecordCompileSDKMutationScopeExpression(object definitionmodel.ObjectSchema, objects []definitionmodel.ObjectSchema, principal principalmodel.Principal, action string) (*recordmodel.RecordScopeExpression, error, bool) {
	expression, err, handled := RecordCompileSDKDataScopeExpression(object, objects, principal, action)
	if err != nil || !handled {
		return nil, err, handled
	}
	return recordMutationScopeExpression(expression), nil, true
}

func recordMutationScopeExpression(expression *recordmodel.RecordScopeExpression) *recordmodel.RecordScopeExpression {
	if expression == nil {
		return nil
	}
	result := *expression
	result.Values = append([]string(nil), expression.Values...)
	result.Path = append([]recordmodel.RecordScopePathSegment(nil), expression.Path...)
	result.Children = make([]recordmodel.RecordScopeExpression, len(expression.Children))
	for index := range expression.Children {
		child := recordMutationScopeExpression(&expression.Children[index])
		result.Children[index] = *child
	}
	if len(result.Path) > 0 {
		result.RelationExists = true
	}
	return &result
}

func compileSDKPredicateGroup(object definitionmodel.ObjectSchema, objects map[string]definitionmodel.ObjectSchema, evaluation identityevaluator.EvaluationContext, predicates []identitysdk.Predicate, operator string) (*recordmodel.RecordScopeExpression, error) {
	children := make([]recordmodel.RecordScopeExpression, 0, len(predicates))
	for _, predicate := range predicates {
		compiled, err := compileSDKPredicate(object, objects, evaluation, predicate, 0)
		if err != nil {
			return nil, err
		}
		children = append(children, compiled)
	}
	switch len(children) {
	case 0:
		return nil, nil
	case 1:
		return &children[0], nil
	default:
		return &recordmodel.RecordScopeExpression{Operator: operator, Children: children}, nil
	}
}

func compileSDKPredicate(object definitionmodel.ObjectSchema, objects map[string]definitionmodel.ObjectSchema, evaluation identityevaluator.EvaluationContext, predicate identitysdk.Predicate, depth int) (recordmodel.RecordScopeExpression, error) {
	if depth > 16 {
		return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK policy expression exceeds maximum depth")
	}
	if err := predicate.Validate(); err != nil {
		return recordmodel.RecordScopeExpression{}, err
	}
	if len(predicate.All) > 0 {
		return compileSDKPredicateChildren(object, objects, evaluation, predicate.All, "and", depth)
	}
	if len(predicate.Any) > 0 {
		return compileSDKPredicateChildren(object, objects, evaluation, predicate.Any, "or", depth)
	}
	if predicate.Not != nil {
		child, err := compileSDKPredicate(object, objects, evaluation, *predicate.Not, depth+1)
		return recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{child}}, err
	}
	current := object
	path := make([]recordmodel.RecordScopePathSegment, 0, len(predicate.Path))
	visited := map[string]bool{object.Key: true}
	for _, segment := range predicate.Path {
		target, exists := objects[string(segment.TargetResource)]
		if !exists || visited[target.Key] {
			return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK policy relation target %q is invalid", segment.TargetResource)
		}
		switch segment.Direction {
		case identitysdk.RelationForward:
			field, found := sdkScopeObjectField(current, segment.Reference)
			if !found || field.Type != "relation" || sdkScopeRelationTarget(field) != target.Key {
				return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK policy forward relation %s.%s is invalid", current.Key, segment.Reference)
			}
		case identitysdk.RelationReverse:
			field, found := sdkScopeObjectField(target, segment.Reference)
			if !found || field.Type != "relation" || sdkScopeRelationTarget(field) != current.Key {
				return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK policy reverse relation %s.%s is invalid", target.Key, segment.Reference)
			}
		default:
			return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK policy relation direction %q is invalid", segment.Direction)
		}
		path = append(path, recordmodel.RecordScopePathSegment{SourceObjectKey: current.Key, Direction: string(segment.Direction), RelationFieldKey: segment.Reference, TargetObjectKey: target.Key})
		visited[target.Key] = true
		current = target
	}
	fieldKey, ok := sdkPolicyFactField(current, predicate.Fact)
	if !ok {
		return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK policy fact %q is not declared by object %q", predicate.Fact, current.Key)
	}
	value, err := identityevaluator.ResolveValue(predicate.Value, evaluation)
	if err != nil {
		return recordmodel.RecordScopeExpression{}, err
	}
	if identityevaluator.IsMissingValue(value) {
		return recordmodel.RecordScopeExpression{Operator: "in", FieldKey: "id"}, nil
	}
	switch predicate.Operator {
	case identitysdk.OperatorEqual:
		return recordmodel.RecordScopeExpression{Operator: "eq", FieldKey: fieldKey, Path: path, Values: []string{fmt.Sprint(value)}}, nil
	case identitysdk.OperatorNotEqual:
		return recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{{Operator: "eq", FieldKey: fieldKey, Path: path, Values: []string{fmt.Sprint(value)}}}}, nil
	case identitysdk.OperatorIn, identitysdk.OperatorNotIn:
		values, ok := sdkPolicyStrings(value)
		if !ok {
			return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK policy %s value is not a list", predicate.Operator)
		}
		// An Identity-issued collection claim may legitimately be empty when the
		// subject has no assignment in that organization scope. This is a normal
		// authorization miss, not a malformed policy. Compile it to a bounded
		// fail-closed expression so list endpoints return an empty page instead
		// of surfacing an internal error.
		if len(values) == 0 {
			if predicate.Operator == identitysdk.OperatorNotIn {
				return recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{*denyAllRecordScopeExpression()}}, nil
			}
			return *denyAllRecordScopeExpression(), nil
		}
		expression := recordmodel.RecordScopeExpression{Operator: "in", FieldKey: fieldKey, Path: path, Values: values}
		if predicate.Operator == identitysdk.OperatorNotIn {
			return recordmodel.RecordScopeExpression{Operator: "not", Children: []recordmodel.RecordScopeExpression{expression}}, nil
		}
		return expression, nil
	case identitysdk.OperatorExists:
		want, ok := value.(bool)
		if !ok {
			return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK exists policy value must be boolean")
		}
		operator := "exists"
		if !want {
			operator = "not_exists"
		}
		return recordmodel.RecordScopeExpression{Operator: operator, FieldKey: fieldKey, Path: path}, nil
	case identitysdk.OperatorPrefix:
		value, ok := value.(string)
		if !ok {
			return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK prefix policy value must be a string")
		}
		return recordmodel.RecordScopeExpression{Operator: "starts_with", FieldKey: fieldKey, Path: path, Values: []string{value}}, nil
	default:
		return recordmodel.RecordScopeExpression{}, fmt.Errorf("SDK policy operator %q cannot be translated to a bounded Runtime query", predicate.Operator)
	}
}

func compileSDKPredicateChildren(object definitionmodel.ObjectSchema, objects map[string]definitionmodel.ObjectSchema, evaluation identityevaluator.EvaluationContext, predicates []identitysdk.Predicate, operator string, depth int) (recordmodel.RecordScopeExpression, error) {
	children := make([]recordmodel.RecordScopeExpression, 0, len(predicates))
	for _, child := range predicates {
		compiled, err := compileSDKPredicate(object, objects, evaluation, child, depth+1)
		if err != nil {
			return recordmodel.RecordScopeExpression{}, err
		}
		children = append(children, compiled)
	}
	return recordmodel.RecordScopeExpression{Operator: operator, Children: children}, nil
}

func sdkPolicyFactField(object definitionmodel.ObjectSchema, fact string) (string, bool) {
	fact = strings.TrimSpace(fact)
	aliases := map[string]string{
		"id":            "id",
		"owner_user_id": recordpolicy.RecordOwnerFieldKey(object),
		"owner_org_id":  recordpolicy.RecordOwnerOrgIDFieldKey(object),
	}
	if field, exists := aliases[fact]; exists {
		return strings.TrimSpace(field), strings.TrimSpace(field) != ""
	}
	for _, field := range object.Fields {
		if field.Key == fact && strings.TrimSpace(field.DisabledAt) == "" {
			return fact, true
		}
	}
	return "", false
}

func sdkPolicyStrings(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...), true
	case []identitysdk.SubjectID:
		result := make([]string, len(values))
		for index, value := range values {
			result[index] = string(value)
		}
		return result, true
	case []any:
		result := make([]string, len(values))
		for index, value := range values {
			result[index] = fmt.Sprint(value)
		}
		return result, true
	default:
		return nil, false
	}
}

func sdkScopeObjectField(object definitionmodel.ObjectSchema, key string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if field.Key == strings.TrimSpace(key) && strings.TrimSpace(field.DisabledAt) == "" {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func sdkScopeRelationTarget(field definitionmodel.FieldSchema) string {
	if value, present := field.Config["object_key"]; present && value != nil {
		if target := strings.TrimSpace(fmt.Sprint(value)); target != "" {
			return target
		}
	}
	return strings.TrimSpace(field.Validation.Target)
}

func denyAllRecordScopeExpression() *recordmodel.RecordScopeExpression {
	return &recordmodel.RecordScopeExpression{Operator: "in", FieldKey: "id"}
}

func normalizeSDKScopeAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "view", "list", "search":
		return "read"
	case "edit":
		return "update"
	default:
		return strings.TrimSpace(action)
	}
}

func sdkScopeExpressionHasRelation(expression recordmodel.RecordScopeExpression) bool {
	if len(expression.Path) > 0 {
		return true
	}
	for _, child := range expression.Children {
		if sdkScopeExpressionHasRelation(child) {
			return true
		}
	}
	return false
}

func directSDKScopeExpressionMatches(expression recordmodel.RecordScopeExpression, record recordmodel.Record) bool {
	switch expression.Operator {
	case "and":
		for _, child := range expression.Children {
			if !directSDKScopeExpressionMatches(child, record) {
				return false
			}
		}
		return true
	case "or":
		for _, child := range expression.Children {
			if directSDKScopeExpressionMatches(child, record) {
				return true
			}
		}
		return false
	case "not":
		return len(expression.Children) == 1 && !directSDKScopeExpressionMatches(expression.Children[0], record)
	case "eq", "in", "prefix", "starts_with":
		value, _ := directRecordScopeValue(record, expression.FieldKey)
		if expression.Operator == "prefix" || expression.Operator == "starts_with" {
			if len(expression.Values) != 1 {
				return false
			}
			return strings.HasPrefix(value, strings.TrimSpace(expression.Values[0]))
		}
		for _, expected := range expression.Values {
			if value == strings.TrimSpace(expected) {
				return true
			}
		}
		return false
	case "exists", "not_exists":
		_, exists := directRecordScopeValue(record, expression.FieldKey)
		if expression.Operator == "not_exists" {
			return !exists
		}
		return exists
	default:
		return false
	}
}

func directRecordScopeValue(record recordmodel.Record, fieldKey string) (string, bool) {
	switch strings.TrimSpace(fieldKey) {
	case "id":
		return record.ID, true
	case recordpolicy.RecordOwnerUserIDSystemField:
		return strings.TrimSpace(record.OwnerUserID), true
	case recordpolicy.RecordOwnerOrgIDSystemField:
		return strings.TrimSpace(record.OwnerOrgID), true
	default:
		value, exists := record.Data[fieldKey]
		return strings.TrimSpace(fmt.Sprint(value)), exists
	}
}
