package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	expressioncontract "github.com/domainry/domainry-runtime/runtime/domain/expression/contract"
	expressionmodel "github.com/domainry/domainry-runtime/runtime/domain/expression/model"
	rulesetmodel "github.com/domainry/domainry-runtime/runtime/domain/ruleset/model"
)

var ruleSetIdentifierPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)

func DecodeRuleSetDefinition(resourceKey string, payload json.RawMessage) (rulesetmodel.RuleSetDefinition, error) {
	var definition rulesetmodel.RuleSetDefinition
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return definition, ruleSetError("backend.rule_set.definition_invalid", resourceKey, err)
	}
	if err := requireRuleSetJSONEOF(decoder); err != nil {
		return definition, ruleSetError("backend.rule_set.definition_invalid", resourceKey, err)
	}
	definition.Key = strings.TrimSpace(definition.Key)
	definition.Name = strings.TrimSpace(definition.Name)
	definition.MatchPolicy = strings.TrimSpace(definition.MatchPolicy)
	definition.EffectiveFrom = strings.TrimSpace(definition.EffectiveFrom)
	definition.EffectiveTo = strings.TrimSpace(definition.EffectiveTo)
	if definition.Key == "" || definition.Name == "" || !ruleSetIdentifierPattern.MatchString(definition.Key) {
		return definition, ruleSetError("backend.rule_set.definition_invalid", firstNonEmpty(resourceKey, definition.Key), nil)
	}
	if resourceKey = strings.TrimSpace(resourceKey); resourceKey != "" && resourceKey != definition.Key {
		return definition, ruleSetError("backend.rule_set.definition_invalid", resourceKey, nil)
	}
	if definition.MatchPolicy != rulesetmodel.RuleSetMatchFirst {
		return definition, ruleSetError("backend.rule_set.match_policy_invalid", definition.Key, nil)
	}
	from, err := rulesetmodel.ParseEffectiveTime(definition.EffectiveFrom)
	if err != nil {
		return definition, ruleSetError("backend.rule_set.effective_from_invalid", definition.Key, err)
	}
	if definition.EffectiveTo != "" {
		to, parseErr := rulesetmodel.ParseEffectiveTime(definition.EffectiveTo)
		if parseErr != nil || !to.After(from) {
			return definition, ruleSetError("backend.rule_set.effective_to_invalid", definition.Key, parseErr)
		}
	}
	if err := validateRuleSetContract(&definition); err != nil {
		return definition, err
	}
	return definition, nil
}

func validateRuleSetContract(definition *rulesetmodel.RuleSetDefinition) error {
	if len(definition.InputTypes) == 0 || len(definition.OutputTypes) == 0 || len(definition.Rules) == 0 || len(definition.DefaultOutputs) == 0 {
		return ruleSetError("backend.rule_set.contract_invalid", definition.Key, nil)
	}
	environment := expressionmodel.ExpressionEnvironment{ReferenceTypes: map[string]expressionmodel.ExpressionType{}}
	normalizedInputTypes := make(map[string]string, len(definition.InputTypes))
	for key, valueType := range definition.InputTypes {
		key = strings.TrimSpace(key)
		valueType = strings.TrimSpace(valueType)
		typed, ok := ruleSetExpressionType(valueType)
		if !ok || !ruleSetIdentifierPattern.MatchString(key) || normalizedInputTypes[key] != "" {
			return ruleSetError("backend.rule_set.input_type_invalid", definition.Key, nil)
		}
		normalizedInputTypes[key] = valueType
		environment.ReferenceTypes["input:"+key] = typed
	}
	definition.InputTypes = normalizedInputTypes
	outputTypes := make(map[string]expressionmodel.ExpressionType, len(definition.OutputTypes))
	normalizedOutputTypes := make(map[string]string, len(definition.OutputTypes))
	for key, valueType := range definition.OutputTypes {
		key = strings.TrimSpace(key)
		valueType = strings.TrimSpace(valueType)
		typed, ok := ruleSetExpressionType(valueType)
		if !ok || !ruleSetIdentifierPattern.MatchString(key) || normalizedOutputTypes[key] != "" {
			return ruleSetError("backend.rule_set.output_type_invalid", definition.Key, nil)
		}
		normalizedOutputTypes[key] = valueType
		outputTypes[key] = typed
	}
	definition.OutputTypes = normalizedOutputTypes
	if err := validateRuleSetOutputs(definition.Key, definition.DefaultOutputs, outputTypes, environment); err != nil {
		return err
	}
	seenKeys, seenPriorities := map[string]bool{}, map[int]bool{}
	for index := range definition.Rules {
		rule := &definition.Rules[index]
		ruleKey := strings.TrimSpace(rule.Key)
		if !ruleSetIdentifierPattern.MatchString(ruleKey) || rule.Priority <= 0 || seenKeys[ruleKey] || seenPriorities[rule.Priority] {
			return ruleSetError("backend.rule_set.rule_identity_invalid", definition.Key, nil)
		}
		rule.Key = ruleKey
		seenKeys[ruleKey], seenPriorities[rule.Priority] = true, true
		conditionType, err := expressioncontract.ExpressionValidate(rule.When, environment)
		if err != nil {
			return ruleSetError("backend.rule_set.condition_invalid", definition.Key, err)
		}
		if conditionType.Kind != expressionmodel.ExpressionTypeBoolean {
			return ruleSetError("backend.rule_set.condition_type_invalid", definition.Key, nil)
		}
		if err := validateRuleSetOutputs(definition.Key, rule.Outputs, outputTypes, environment); err != nil {
			return err
		}
	}
	sort.Slice(definition.Rules, func(i, j int) bool {
		// Priorities are unique by the identity validation above.
		return definition.Rules[i].Priority < definition.Rules[j].Priority
	})
	return nil
}

func validateRuleSetOutputs(ruleSetKey string, outputs map[string]expressionmodel.BusinessExpression, expected map[string]expressionmodel.ExpressionType, environment expressionmodel.ExpressionEnvironment) error {
	if len(outputs) != len(expected) {
		return ruleSetError("backend.rule_set.outputs_invalid", ruleSetKey, nil)
	}
	for key, expectedType := range expected {
		expression, ok := outputs[key]
		if !ok {
			return ruleSetError("backend.rule_set.outputs_invalid", ruleSetKey, nil)
		}
		actualType, err := expressioncontract.ExpressionValidate(expression, environment)
		if err != nil {
			return ruleSetError("backend.rule_set.output_expression_invalid", ruleSetKey, err)
		}
		if actualType != expectedType {
			return ruleSetError("backend.rule_set.output_type_mismatch", ruleSetKey, nil)
		}
	}
	return nil
}

func ruleSetExpressionType(value string) (expressionmodel.ExpressionType, bool) {
	switch strings.TrimSpace(value) {
	case expressionmodel.ExpressionTypeBoolean, expressionmodel.ExpressionTypeInteger, expressionmodel.ExpressionTypeDecimal,
		expressionmodel.ExpressionTypeDate, expressionmodel.ExpressionTypeDateTime, expressionmodel.ExpressionTypeDuration,
		expressionmodel.ExpressionTypeText:
		return expressionmodel.ExpressionType{Kind: strings.TrimSpace(value)}, true
	default:
		return expressionmodel.ExpressionType{}, false
	}
}

func ruleSetError(code, key string, cause error) error {
	return &rulesetmodel.RuleSetError{Code: code, RuleSetKey: strings.TrimSpace(key), Cause: cause}
}

func requireRuleSetJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if normalized := strings.TrimSpace(value); normalized != "" {
			return normalized
		}
	}
	return ""
}
