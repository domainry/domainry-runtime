package service

import (
	"testing"
	"time"

	expressionmodel "github.com/domainry/domainry-runtime/runtime/domain/expression/model"
	rulesetmodel "github.com/domainry/domainry-runtime/runtime/domain/ruleset/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestEvaluateRuleSetResolvesEffectiveVersionAndFirstMatchingPriority(t *testing.T) {
	versions := []rulesetmodel.RuleSetVersion{
		ruleSetVersion("workspace-a", "1", "hash-1", "2026-01-01", "0.10"),
		ruleSetVersion("workspace-a", "2", "hash-2", "2026-07-01", "0.20"),
		ruleSetVersion("workspace-b", "9", "hash-other", "2026-01-01", "0.99"),
	}
	before, err := EvaluateRuleSet(versions, "workspace-a", "order.adjustment_policy", mustTime(t, "2026-06-30T23:59:59Z"), map[string]any{"priority": "normal"})
	if err != nil || before.Version != "1" || before.Outputs["rate"] != "0.10" || before.MatchedRuleKey != "standard" {
		t.Fatalf("before=%+v err=%v", before, err)
	}
	atBoundary, err := EvaluateRuleSet(versions, "workspace-a", "order.adjustment_policy", mustTime(t, "2026-07-01T00:00:00Z"), map[string]any{"priority": "high"})
	if err != nil || atBoundary.Version != "2" || atBoundary.ResourceHash != "hash-2" || atBoundary.Outputs["rate"] != "0.00" || atBoundary.MatchedRuleKey != "priority" {
		t.Fatalf("boundary=%+v err=%v", atBoundary, err)
	}
}

func TestEvaluateRuleSetUsesTypedDefaultAndFailsClosed(t *testing.T) {
	version := ruleSetVersion("workspace-a", "1", "hash", "2026-01-01", "0.10")
	version.Definition.Rules = version.Definition.Rules[:1]
	version.Definition.Rules[0].When = literal("boolean", false)
	resolved, err := EvaluateRuleSet([]rulesetmodel.RuleSetVersion{version}, "workspace-a", version.Definition.Key, mustTime(t, "2026-06-01T00:00:00Z"), map[string]any{"priority": "normal"})
	if err != nil || resolved.MatchedRuleKey != "" || resolved.Outputs["reason"] != "default" {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	tests := []struct {
		name      string
		workspace string
		inputs    map[string]any
		code      string
	}{
		{name: "workspace isolation", workspace: "workspace-b", inputs: map[string]any{"priority": "normal"}, code: "backend.rule_set.not_effective"},
		{name: "missing input", workspace: "workspace-a", inputs: map[string]any{}, code: "backend.rule_set.input_contract_mismatch"},
		{name: "invalid input", workspace: "workspace-a", inputs: map[string]any{"priority": 10}, code: "backend.rule_set.input_value_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, evaluateErr := EvaluateRuleSet([]rulesetmodel.RuleSetVersion{version}, test.workspace, version.Definition.Key, mustTime(t, "2026-06-01T00:00:00Z"), test.inputs)
			if code := apperror.CodeOf(apperror.FromError(apperror.KindBadRequest, evaluateErr)); code != test.code {
				t.Fatalf("code=%q want=%q err=%v", code, test.code, evaluateErr)
			}
		})
	}
}

func TestEvaluateRuleSetCoversEvaluationAndOutputFailures(t *testing.T) {
	base := ruleSetVersion("workspace-a", "1", "hash", "2026-01-01", "0.10")
	effective := mustTime(t, "2026-06-01T00:00:00Z")
	for _, test := range []struct {
		name   string
		mutate func(*rulesetmodel.RuleSetVersion)
		code   string
	}{
		{name: "condition evaluation", mutate: func(version *rulesetmodel.RuleSetVersion) {
			version.Definition.Rules = []rulesetmodel.RuleSetRule{{Key: "bad", Priority: 1, When: expressionmodel.BusinessExpression{Kind: "unsupported"}}}
		}, code: "backend.rule_set.condition_evaluation_failed"},
		{name: "condition result type", mutate: func(version *rulesetmodel.RuleSetVersion) {
			version.Definition.Rules = []rulesetmodel.RuleSetRule{{Key: "bad", Priority: 1, When: literal("text", "true")}}
		}, code: "backend.rule_set.condition_result_invalid"},
		{name: "output evaluation", mutate: func(version *rulesetmodel.RuleSetVersion) {
			version.Definition.Rules = nil
			version.Definition.DefaultOutputs["rate"] = expressionmodel.BusinessExpression{Kind: "unsupported"}
		}, code: "backend.rule_set.output_evaluation_failed"},
		{name: "output type", mutate: func(version *rulesetmodel.RuleSetVersion) {
			version.Definition.Rules = nil
			version.Definition.DefaultOutputs["rate"] = literal("text", "wrong")
		}, code: "backend.rule_set.output_type_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			version := base
			version.Definition.Rules = append([]rulesetmodel.RuleSetRule(nil), base.Definition.Rules...)
			version.Definition.DefaultOutputs = cloneRuleSetOutputs(base.Definition.DefaultOutputs)
			test.mutate(&version)
			_, err := EvaluateRuleSet([]rulesetmodel.RuleSetVersion{version}, "workspace-a", version.Definition.Key, effective, map[string]any{"priority": "normal"})
			if code := apperror.CodeOf(apperror.FromError(apperror.KindBadRequest, err)); code != test.code {
				t.Fatalf("code=%q want=%q err=%v", code, test.code, err)
			}
		})
	}
}

func TestResolveRuleSetVersionCoversStoredTimeVersionAndSelectionEdges(t *testing.T) {
	effective := mustTime(t, "2026-06-01T00:00:00Z")
	valid := ruleSetVersion("workspace-a", "1", "hash", "2026-01-01", "0.10")
	valid.Definition.EffectiveTo = "2026-07-01"

	selected, err := resolveRuleSetVersion([]rulesetmodel.RuleSetVersion{
		ruleSetVersion("workspace-a", "1", "wrong-key", "2026-01-01", "0.10"),
		ruleSetVersion("workspace-a", "1", "future", "2026-08-01", "0.10"),
		ruleSetVersion("workspace-a", "5", "latest-date", "2026-05-01", "0.10"),
		ruleSetVersion("workspace-a", "8", "older-date", "2026-03-01", "0.10"),
		ruleSetVersion("workspace-a", "4", "same-date-lower", "2026-05-01", "0.10"),
		ruleSetVersion("workspace-a", "6", "same-date-higher", "2026-05-01", "0.10"),
	}, "workspace-a", "order.adjustment_policy", effective)
	if err != nil || selected.ResourceHash != "same-date-higher" {
		t.Fatalf("selected=%#v err=%v", selected, err)
	}

	wrongKey := valid
	wrongKey.Definition.Key = "other.policy"
	for _, test := range []struct {
		name      string
		candidate rulesetmodel.RuleSetVersion
		at        time.Time
		code      string
	}{
		{name: "wrong key", candidate: wrongKey, at: effective, code: "backend.rule_set.not_effective"},
		{name: "invalid from", candidate: ruleSetVersion("workspace-a", "1", "hash", "invalid", "0.10"), at: effective, code: "backend.rule_set.effective_from_invalid"},
		{name: "invalid to", candidate: withRuleSetEffectiveTo(valid, "invalid"), at: effective, code: "backend.rule_set.effective_to_invalid"},
		{name: "non increasing to", candidate: withRuleSetEffectiveTo(valid, "2026-01-01"), at: effective, code: "backend.rule_set.effective_to_invalid"},
		{name: "expired", candidate: valid, at: mustTime(t, "2026-07-01T00:00:00Z"), code: "backend.rule_set.not_effective"},
		{name: "invalid version", candidate: withRuleSetVersion(valid, "latest"), at: effective, code: "backend.rule_set.version_invalid"},
		{name: "zero version", candidate: withRuleSetVersion(valid, "0"), at: effective, code: "backend.rule_set.version_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, resolveErr := resolveRuleSetVersion([]rulesetmodel.RuleSetVersion{test.candidate}, "workspace-a", "order.adjustment_policy", test.at)
			if code := apperror.CodeOf(apperror.FromError(apperror.KindBadRequest, resolveErr)); code != test.code {
				t.Fatalf("code=%q want=%q err=%v", code, test.code, resolveErr)
			}
		})
	}
}

func TestNormalizeRuleSetInputsRejectsMissingAndNilValuesWithMatchingCardinality(t *testing.T) {
	definition := ruleSetVersion("workspace-a", "1", "hash", "2026-01-01", "0.10").Definition
	for _, inputs := range []map[string]any{{"other": "normal"}, {"priority": nil}} {
		if _, err := normalizeRuleSetInputs(definition, inputs); apperror.CodeOf(apperror.FromError(apperror.KindBadRequest, err)) != "backend.rule_set.input_contract_mismatch" {
			t.Fatalf("inputs=%#v error=%v", inputs, err)
		}
	}
}

func TestEvaluateRuleSetOrdersEqualPriorityByStableKey(t *testing.T) {
	version := ruleSetVersion("workspace-a", "1", "hash", "2026-01-01", "0.10")
	version.Definition.Rules = []rulesetmodel.RuleSetRule{
		{Key: "z-last", Priority: 10, When: literal("boolean", true), Outputs: cloneRuleSetOutputs(version.Definition.DefaultOutputs)},
		{Key: "a-first", Priority: 10, When: literal("boolean", true), Outputs: cloneRuleSetOutputs(version.Definition.DefaultOutputs)},
	}
	result, err := EvaluateRuleSet([]rulesetmodel.RuleSetVersion{version}, "workspace-a", version.Definition.Key, mustTime(t, "2026-06-01T00:00:00Z"), map[string]any{"priority": "normal"})
	if err != nil || result.MatchedRuleKey != "a-first" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func cloneRuleSetOutputs(source map[string]expressionmodel.BusinessExpression) map[string]expressionmodel.BusinessExpression {
	result := make(map[string]expressionmodel.BusinessExpression, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func withRuleSetEffectiveTo(version rulesetmodel.RuleSetVersion, value string) rulesetmodel.RuleSetVersion {
	version.Definition.EffectiveTo = value
	return version
}

func withRuleSetVersion(version rulesetmodel.RuleSetVersion, value string) rulesetmodel.RuleSetVersion {
	version.Version = value
	return version
}

func ruleSetVersion(workspace, version, hash, from, standardRate string) rulesetmodel.RuleSetVersion {
	return rulesetmodel.RuleSetVersion{WorkspaceID: workspace, Version: version, ResourceHash: hash, Definition: rulesetmodel.RuleSetDefinition{
		Key: "order.adjustment_policy", Name: "Order adjustment", MatchPolicy: rulesetmodel.RuleSetMatchFirst,
		InputTypes: map[string]string{"priority": "text"}, OutputTypes: map[string]string{"rate": "decimal", "reason": "text"}, EffectiveFrom: from,
		Rules: []rulesetmodel.RuleSetRule{
			{Key: "standard", Priority: 20, When: literal("boolean", true), Outputs: map[string]expressionmodel.BusinessExpression{"rate": literal("decimal", standardRate), "reason": literal("text", "standard")}},
			{Key: "priority", Priority: 10, When: expressionmodel.BusinessExpression{Kind: "operation", Operator: "eq", Arguments: []expressionmodel.BusinessExpression{{Kind: "reference", ValueType: "text", Reference: &expressionmodel.ExpressionReference{Source: "input", Path: []string{"priority"}}}, literal("text", "high")}}, Outputs: map[string]expressionmodel.BusinessExpression{"rate": literal("decimal", "0"), "reason": literal("text", "priority")}},
		},
		DefaultOutputs: map[string]expressionmodel.BusinessExpression{"rate": literal("decimal", "0"), "reason": literal("text", "default")},
	}}
}

func literal(valueType string, value any) expressionmodel.BusinessExpression {
	return expressionmodel.BusinessExpression{Kind: "literal", ValueType: valueType, Value: value}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
