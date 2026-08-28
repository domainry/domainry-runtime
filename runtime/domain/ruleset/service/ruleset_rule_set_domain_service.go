package service

import (
	"sort"
	"strconv"
	"strings"
	"time"

	expressioncontract "github.com/domainry/domainry-foundation/expression/contract"
	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
	rulesetmodel "github.com/domainry/domainry-runtime/runtime/domain/ruleset/model"
)

func EvaluateRuleSet(versions []rulesetmodel.RuleSetVersion, workspaceID, ruleSetKey string, effectiveAt time.Time, inputs map[string]any) (rulesetmodel.RuleSetResolution, error) {
	selected, err := resolveRuleSetVersion(versions, workspaceID, ruleSetKey, effectiveAt)
	if err != nil {
		return rulesetmodel.RuleSetResolution{}, err
	}
	normalizedInputs, err := normalizeRuleSetInputs(selected.Definition, inputs)
	if err != nil {
		return rulesetmodel.RuleSetResolution{}, err
	}
	context := expressionmodel.ExpressionContext{Sources: map[string]any{"input": normalizedInputs}, Location: time.UTC}
	rules := append([]rulesetmodel.RuleSetRule(nil), selected.Definition.Rules...)
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Priority == rules[j].Priority {
			return rules[i].Key < rules[j].Key
		}
		return rules[i].Priority < rules[j].Priority
	})
	outputs := selected.Definition.DefaultOutputs
	matchedRuleKey := ""
	for _, rule := range rules {
		condition, evaluateErr := expressioncontract.ExpressionEvaluate(rule.When, context)
		if evaluateErr != nil {
			return rulesetmodel.RuleSetResolution{}, ruleSetServiceError("backend.rule_set.condition_evaluation_failed", ruleSetKey, evaluateErr)
		}
		if condition.Type.Kind != expressionmodel.ExpressionTypeBoolean {
			return rulesetmodel.RuleSetResolution{}, ruleSetServiceError("backend.rule_set.condition_result_invalid", ruleSetKey, nil)
		}
		// ExpressionEvaluate owns the closed invariant that a boolean result
		// carries a Go bool value.
		matched := condition.Value.(bool)
		if matched {
			outputs = rule.Outputs
			matchedRuleKey = rule.Key
			break
		}
	}
	resolvedOutputs := make(map[string]any, len(outputs))
	for key, expression := range outputs {
		value, evaluateErr := expressioncontract.ExpressionEvaluate(expression, context)
		if evaluateErr != nil {
			return rulesetmodel.RuleSetResolution{}, ruleSetServiceError("backend.rule_set.output_evaluation_failed", ruleSetKey, evaluateErr)
		}
		if value.Type.Kind != strings.TrimSpace(selected.Definition.OutputTypes[key]) {
			return rulesetmodel.RuleSetResolution{}, ruleSetServiceError("backend.rule_set.output_type_mismatch", ruleSetKey, nil)
		}
		resolvedOutputs[key] = value.Value
	}
	return rulesetmodel.RuleSetResolution{
		WorkspaceID: selected.WorkspaceID, RuleSetKey: selected.Definition.Key, Version: selected.Version,
		ResourceHash: selected.ResourceHash, EffectiveAt: effectiveAt.UTC().Format(time.RFC3339Nano),
		EffectiveFrom: selected.Definition.EffectiveFrom, EffectiveTo: selected.Definition.EffectiveTo,
		MatchedRuleKey: matchedRuleKey, Outputs: resolvedOutputs,
	}, nil
}

func resolveRuleSetVersion(versions []rulesetmodel.RuleSetVersion, workspaceID, ruleSetKey string, effectiveAt time.Time) (rulesetmodel.RuleSetVersion, error) {
	workspaceID, ruleSetKey = strings.TrimSpace(workspaceID), strings.TrimSpace(ruleSetKey)
	var selected rulesetmodel.RuleSetVersion
	var selectedFrom time.Time
	selectedVersion := int64(-1)
	found := false
	for _, candidate := range versions {
		if strings.TrimSpace(candidate.WorkspaceID) != workspaceID || strings.TrimSpace(candidate.Definition.Key) != ruleSetKey {
			continue
		}
		from, err := rulesetmodel.ParseEffectiveTime(candidate.Definition.EffectiveFrom)
		if err != nil {
			return rulesetmodel.RuleSetVersion{}, ruleSetServiceError("backend.rule_set.effective_from_invalid", ruleSetKey, err)
		}
		if effectiveAt.UTC().Before(from) {
			continue
		}
		if strings.TrimSpace(candidate.Definition.EffectiveTo) != "" {
			to, parseErr := rulesetmodel.ParseEffectiveTime(candidate.Definition.EffectiveTo)
			if parseErr != nil || !to.After(from) {
				return rulesetmodel.RuleSetVersion{}, ruleSetServiceError("backend.rule_set.effective_to_invalid", ruleSetKey, parseErr)
			}
			if !effectiveAt.UTC().Before(to) {
				continue
			}
		}
		version, parseErr := strconv.ParseInt(strings.TrimSpace(candidate.Version), 10, 64)
		if parseErr != nil || version < 1 {
			return rulesetmodel.RuleSetVersion{}, ruleSetServiceError("backend.rule_set.version_invalid", ruleSetKey, parseErr)
		}
		if !found || from.After(selectedFrom) || (from.Equal(selectedFrom) && version > selectedVersion) {
			selected, selectedFrom, selectedVersion, found = candidate, from, version, true
		}
	}
	if !found {
		return rulesetmodel.RuleSetVersion{}, ruleSetServiceError("backend.rule_set.not_effective", ruleSetKey, nil)
	}
	return selected, nil
}

func normalizeRuleSetInputs(definition rulesetmodel.RuleSetDefinition, inputs map[string]any) (map[string]any, error) {
	if len(inputs) != len(definition.InputTypes) {
		return nil, ruleSetServiceError("backend.rule_set.input_contract_mismatch", definition.Key, nil)
	}
	result := make(map[string]any, len(inputs))
	for key, valueType := range definition.InputTypes {
		value, ok := inputs[key]
		if !ok || value == nil {
			return nil, ruleSetServiceError("backend.rule_set.input_contract_mismatch", definition.Key, nil)
		}
		normalized, err := expressioncontract.ExpressionEvaluate(expressionmodel.BusinessExpression{Kind: "literal", ValueType: strings.TrimSpace(valueType), Value: value}, expressionmodel.ExpressionContext{Location: time.UTC})
		if err != nil {
			return nil, ruleSetServiceError("backend.rule_set.input_value_invalid", definition.Key, err)
		}
		result[key] = normalized.Value
	}
	return result, nil
}

func ruleSetServiceError(code, key string, cause error) error {
	return &rulesetmodel.RuleSetError{Code: code, RuleSetKey: strings.TrimSpace(key), Cause: cause}
}
