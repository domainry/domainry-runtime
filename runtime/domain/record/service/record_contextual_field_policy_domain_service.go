package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityevaluator "github.com/domainry/domainry-identity-sdk/authorization/evaluator"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type RecordFieldPolicyDecision struct {
	FieldKey     string
	Effect       string
	RuleKey      string
	Reason       string
	MaskStrategy *identitysdk.MaskStrategy
	AuditDenial  bool
}

// RecordContextualFieldPolicyDependencies contains only Runtime-owned record
// graph execution capabilities. Policy shape, priority and effects are owned
// and evaluated by domainry-identity-sdk.
type RecordContextualFieldPolicyDependencies struct {
	Repository recordrepository.RecordRepository
	Objects    func() []definitionmodel.ObjectSchema
}

type RecordContextualFieldPolicyDomainService struct {
	dependencies RecordContextualFieldPolicyDependencies
}

func NewRecordContextualFieldPolicyDomainService(dependencies RecordContextualFieldPolicyDependencies) *RecordContextualFieldPolicyDomainService {
	return &RecordContextualFieldPolicyDomainService{dependencies: dependencies}
}

func (s *RecordContextualFieldPolicyDomainService) Decide(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, fieldKey, action string) (RecordFieldPolicyDecision, error) {
	if principal.AccessBundle != nil {
		decision, err := identityevaluator.EvaluateField(*principal.AccessBundle, identityevaluator.FieldRequest{
			Resource: identitysdk.ResourceType(object.Key), Field: fieldKey, Action: identitysdk.Action(action),
		}, func(predicate identitysdk.Predicate) (bool, error) {
			return s.matchesSDKPredicate(ctx, principal, object, record, predicate)
		})
		if err != nil {
			return RecordFieldPolicyDecision{}, fieldPolicyExpressionError(err)
		}
		return runtimeFieldDecision(fieldKey, decision), nil
	}
	if principal.SystemScope.Valid() {
		permissionAction := "read"
		switch action {
		case "write", "update":
			permissionAction = "update"
		case "export":
			permissionAction = "export"
		}
		if principal.Allows(object.Key, permissionAction) {
			return RecordFieldPolicyDecision{FieldKey: fieldKey, Effect: "allow", RuleKey: "runtime_system_capability"}, nil
		}
	}
	return RecordFieldPolicyDecision{FieldKey: fieldKey, Effect: "hide", RuleKey: "identity_access_bundle_required"}, nil
}

func (s *RecordContextualFieldPolicyDomainService) ApplyRead(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, action string) (recordmodel.Record, []RecordFieldPolicyDecision, error) {
	records, denials, err := s.ApplyReadPage(ctx, principal, object, []recordmodel.Record{record}, action)
	if err != nil {
		return recordmodel.Record{}, nil, err
	}
	return records[0], denials[record.ID], nil
}

// ApplyReadPage resolves each unique SDK predicate once for the page. Query
// count depends on policy predicates, never records x fields.
func (s *RecordContextualFieldPolicyDomainService) ApplyReadPage(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, records []recordmodel.Record, action string) ([]recordmodel.Record, map[string][]RecordFieldPolicyDecision, error) {
	predicateMatches, err := s.resolvePagePredicateMatches(ctx, principal, object, records, action)
	if err != nil {
		return nil, nil, err
	}
	results := make([]recordmodel.Record, 0, len(records))
	denialsByRecord := make(map[string][]RecordFieldPolicyDecision, len(records))
	for _, record := range records {
		filtered, denials, err := s.applyResolvedRead(ctx, principal, object, record, action, predicateMatches)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, filtered)
		if len(denials) > 0 {
			denialsByRecord[record.ID] = denials
		}
	}
	return results, denialsByRecord, nil
}

func (s *RecordContextualFieldPolicyDomainService) applyResolvedRead(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, action string, predicateMatches map[string]map[string]bool) (recordmodel.Record, []RecordFieldPolicyDecision, error) {
	out := map[string]any{}
	denials := []RecordFieldPolicyDecision{}
	for _, field := range object.Fields {
		value, present := record.Data[field.Key]
		if !present {
			continue
		}
		decision, err := s.decideResolved(ctx, principal, object, record, field.Key, action, predicateMatches)
		if err != nil {
			return recordmodel.Record{}, nil, err
		}
		switch decision.Effect {
		case "allow":
			out[field.Key] = value
		case "mask":
			out[field.Key] = RecordApplyMaskStrategy(field, value, decision.MaskStrategy)
		case "deny", "hide":
			if decision.AuditDenial {
				denials = append(denials, decision)
			}
		default:
			return recordmodel.Record{}, nil, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.field_policy.invalid_effect", Params: map[string]string{"field": field.Key, "effect": decision.Effect}}
		}
	}
	record.Data = out
	return record, denials, nil
}

func (s *RecordContextualFieldPolicyDomainService) decideResolved(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, fieldKey, action string, predicateMatches map[string]map[string]bool) (RecordFieldPolicyDecision, error) {
	if principal.AccessBundle == nil {
		return s.Decide(ctx, principal, object, record, fieldKey, action)
	}
	decision, err := identityevaluator.EvaluateField(*principal.AccessBundle, identityevaluator.FieldRequest{
		Resource: identitysdk.ResourceType(object.Key), Field: fieldKey, Action: identitysdk.Action(action),
	}, func(predicate identitysdk.Predicate) (bool, error) {
		return predicateMatches[sdkPredicateKey(predicate)][record.ID], nil
	})
	if err != nil {
		return RecordFieldPolicyDecision{}, fieldPolicyExpressionError(err)
	}
	return runtimeFieldDecision(fieldKey, decision), nil
}

func (s *RecordContextualFieldPolicyDomainService) resolvePagePredicateMatches(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, records []recordmodel.Record, action string) (map[string]map[string]bool, error) {
	predicates := map[string]identitysdk.Predicate{}
	if principal.AccessBundle != nil {
		for _, policy := range principal.AccessBundle.FieldPolicies {
			if policy.Resource != identitysdk.ResourceType(object.Key) {
				continue
			}
			for _, rule := range policy.Rules {
				if rule.Predicate != nil && sdkFieldActionMatches(rule.Actions, action) {
					predicates[sdkPredicateKey(*rule.Predicate)] = *rule.Predicate
				}
			}
		}
		for _, guardrail := range principal.AccessBundle.Guardrails {
			if guardrail.Resource == identitysdk.ResourceType(object.Key) && strings.TrimSpace(guardrail.Field) != "" && guardrail.Predicate != nil && sdkActionsEquivalent(string(guardrail.Action), action) {
				predicates[sdkPredicateKey(*guardrail.Predicate)] = *guardrail.Predicate
			}
		}
	}
	resolved := make(map[string]map[string]bool, len(predicates))
	if len(records) == 0 || len(predicates) == 0 {
		return resolved, nil
	}
	ids := make([]any, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	objects := s.objectMap()
	evaluation := recordpolicy.RecordSDKEvaluationContext(principal)
	for key, predicate := range predicates {
		expression, err := compileSDKPredicate(object, objects, evaluation, predicate, 0)
		if err != nil {
			return nil, fieldPolicyExpressionError(err)
		}
		if !sdkScopeExpressionHasRelation(expression) {
			matches := make(map[string]bool, len(records))
			for _, record := range records {
				if directSDKScopeExpressionMatches(expression, record) {
					matches[record.ID] = true
				}
			}
			resolved[key] = matches
			continue
		}
		if s.dependencies.Repository == nil {
			return nil, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.field_policy.repository_unavailable"}
		}
		page, err := s.dependencies.Repository.ListRecords(ctx, principal.WorkspaceID, object, recordmodel.RecordListQuery{
			Page: 1, PageSize: len(records), Scope: "custom", RootObjectKey: object.Key,
			ScopeExpression: &expression, Filters: map[string]any{"id__in": ids},
		})
		if err != nil {
			return nil, fmt.Errorf("evaluate contextual field policy page: %w", err)
		}
		matches := make(map[string]bool, len(page.Items))
		for _, matched := range page.Items {
			matches[matched.ID] = true
		}
		resolved[key] = matches
	}
	return resolved, nil
}

func (s *RecordContextualFieldPolicyDomainService) matchesSDKPredicate(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, predicate identitysdk.Predicate) (bool, error) {
	expression, err := compileSDKPredicate(object, s.objectMap(), recordpolicy.RecordSDKEvaluationContext(principal), predicate, 0)
	if err != nil {
		return false, fieldPolicyExpressionError(err)
	}
	if !sdkScopeExpressionHasRelation(expression) {
		return directSDKScopeExpressionMatches(expression, record), nil
	}
	if s.dependencies.Repository == nil {
		return false, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.field_policy.repository_unavailable"}
	}
	page, err := s.dependencies.Repository.ListRecords(ctx, principal.WorkspaceID, object, recordmodel.RecordListQuery{
		Page: 1, PageSize: 1, Scope: "custom", RootObjectKey: object.Key,
		ScopeExpression: &expression, Filters: map[string]any{"id__in": []any{record.ID}},
	})
	if err != nil {
		return false, fmt.Errorf("evaluate contextual field policy: %w", err)
	}
	return len(page.Items) == 1, nil
}

func (s *RecordContextualFieldPolicyDomainService) ValidateWrite(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record, data map[string]any) error {
	for fieldKey := range data {
		decision, err := s.Decide(ctx, principal, object, record, fieldKey, "write")
		if err != nil {
			return err
		}
		if decision.Effect != "allow" {
			return &apperror.CodedError{Code: "backend.validation.field_not_writable", Params: map[string]string{"field": fieldKey, "role": principal.RoleKey, "rule": decision.RuleKey}}
		}
	}
	return nil
}

func runtimeFieldDecision(fieldKey string, decision identityevaluator.FieldDecision) RecordFieldPolicyDecision {
	return RecordFieldPolicyDecision{
		FieldKey: fieldKey, Effect: string(decision.Effect), RuleKey: decision.RuleKey, Reason: decision.Reason,
		MaskStrategy: decision.MaskStrategy, AuditDenial: decision.AuditDenial,
	}
}

func sdkPredicateKey(predicate identitysdk.Predicate) string {
	payload, _ := json.Marshal(predicate)
	return string(payload)
}

func sdkFieldActionMatches(actions []identitysdk.Action, action string) bool {
	for _, candidate := range actions {
		if sdkActionsEquivalent(string(candidate), action) {
			return true
		}
	}
	return false
}

func sdkActionsEquivalent(left, right string) bool {
	left, right = strings.ToLower(strings.TrimSpace(left)), strings.ToLower(strings.TrimSpace(right))
	if left == right {
		return true
	}
	return left == "write" && (right == "create" || right == "update") || right == "write" && (left == "create" || left == "update")
}

func (s *RecordContextualFieldPolicyDomainService) objectMap() map[string]definitionmodel.ObjectSchema {
	result := map[string]definitionmodel.ObjectSchema{}
	if s == nil || s.dependencies.Objects == nil {
		return result
	}
	for _, object := range s.dependencies.Objects() {
		result[object.Key] = object
	}
	return result
}

func fieldPolicyExpressionError(err error) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.field_policy.expression_invalid", Err: err}
}

func RecordApplyMaskStrategy(field definitionmodel.FieldSchema, value any, strategy *identitysdk.MaskStrategy) string {
	if strategy == nil {
		return recordpolicy.RecordMaskFieldValue(field, value)
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return ""
	}
	switch strategy.Type {
	case identitysdk.MaskTypeEmail:
		parts := strings.SplitN(text, "@", 2)
		if len(parts) == 2 && parts[1] != "" {
			local := []rune(parts[0])
			if len(local) > 0 {
				return string(local[:1]) + "***@" + parts[1]
			}
			return "****@" + parts[1]
		}
		return maskLastN(text, 4)
	case identitysdk.MaskTypePhone:
		return maskKeepEdges(text, 3, 4)
	case identitysdk.MaskTypeIDNumber:
		return maskKeepEdges(text, 6, 4)
	case identitysdk.MaskTypeYearOnly:
		if len(text) >= 4 {
			return text[:4]
		}
		return "****"
	case identitysdk.MaskTypeLastN:
		return maskLastN(text, strategy.LastN)
	default:
		return "****"
	}
}

func maskKeepEdges(value string, prefix, suffix int) string {
	runes := []rune(value)
	if len(runes) <= prefix+suffix {
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:prefix]) + strings.Repeat("*", len(runes)-prefix-suffix) + string(runes[len(runes)-suffix:])
}

func maskLastN(value string, lastN int) string {
	if lastN < 1 {
		lastN = 4
	}
	runes := []rune(value)
	if len(runes) <= lastN {
		return strings.Repeat("*", utf8.RuneCountInString(value))
	}
	return strings.Repeat("*", len(runes)-lastN) + string(runes[len(runes)-lastN:])
}
