package validation

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	expressionmodel "github.com/domainry/domainry-foundation/expression/model"
	rulesetmodel "github.com/domainry/domainry-runtime/runtime/domain/ruleset/model"
)

func TestDecodeRuleSetDefinitionAcceptsGenericTypedFirstMatchContract(t *testing.T) {
	payload := json.RawMessage(`{
		"key":"order.adjustment_policy","name":"Order adjustment policy","match_policy":"first_match",
		"input_types":{"elapsed_hours":"decimal","priority":"text"},
		"output_types":{"allowed":"boolean","fee_rate":"decimal","reason":"text"},
		"effective_from":"2026-07-01T00:00:00Z","effective_to":"2027-01-01T00:00:00Z",
		"rules":[
			{"key":"standard","priority":20,"when":{"kind":"literal","value_type":"boolean","value":true},"outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":true},"fee_rate":{"kind":"literal","value_type":"decimal","value":"0.10"},"reason":{"kind":"literal","value_type":"text","value":"standard"}}},
			{"key":"priority","priority":10,"when":{"kind":"operation","operator":"eq","arguments":[{"kind":"reference","value_type":"text","reference":{"source":"input","path":["priority"]}},{"kind":"literal","value_type":"text","value":"high"}]},"outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":true},"fee_rate":{"kind":"literal","value_type":"decimal","value":"0"},"reason":{"kind":"literal","value_type":"text","value":"priority"}}}
		],
		"default_outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":false},"fee_rate":{"kind":"literal","value_type":"decimal","value":"0"},"reason":{"kind":"literal","value_type":"text","value":"not_allowed"}}
	}`)
	definition, err := DecodeRuleSetDefinition("order.adjustment_policy", payload)
	if err != nil {
		t.Fatalf("decode rule set: %v", err)
	}
	if len(definition.Rules) != 2 || definition.Rules[0].Key != "priority" || definition.Rules[1].Key != "standard" {
		t.Fatalf("rules were not canonicalized by priority: %+v", definition.Rules)
	}
}

func TestDecodeRuleSetDefinitionRejectsOpenInvalidAndUntypedContracts(t *testing.T) {
	base := `{"key":"policy.limit","name":"Limit policy","match_policy":"first_match","input_types":{"count":"integer"},"output_types":{"allowed":"boolean"},"effective_from":"2026-07-01","rules":[{"key":"limit","priority":10,"when":{"kind":"literal","value_type":"boolean","value":true},"outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":true}}}],"default_outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":false}}}`
	tests := []struct {
		name    string
		payload string
		code    string
	}{
		{name: "unknown field", payload: base[:len(base)-1] + `,"legacy":true}`, code: "backend.rule_set.definition_invalid"},
		{name: "resource key mismatch", payload: base, code: "backend.rule_set.definition_invalid"},
		{name: "unknown match policy", payload: replaceOnce(base, `"first_match"`, `"all_matches"`), code: "backend.rule_set.match_policy_invalid"},
		{name: "invalid effective interval", payload: replaceOnce(base, `"rules"`, `"effective_to":"2026-06-30","rules"`), code: "backend.rule_set.effective_to_invalid"},
		{name: "unknown input type", payload: replaceOnce(base, `"integer"`, `"money"`), code: "backend.rule_set.input_type_invalid"},
		{name: "condition is not boolean", payload: replaceOnce(base, `"value_type":"boolean","value":true},"outputs"`, `"value_type":"integer","value":1},"outputs"`), code: "backend.rule_set.condition_type_invalid"},
		{name: "output type mismatch", payload: replaceOnce(base, `"value_type":"boolean","value":true}}}],`, `"value_type":"text","value":"yes"}}}],`), code: "backend.rule_set.output_type_mismatch"},
		{name: "missing default output", payload: replaceOnce(base, `"default_outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":false}}`, `"default_outputs":{}`), code: "backend.rule_set.contract_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			key := "policy.limit"
			if test.name == "resource key mismatch" {
				key = "policy.other"
			}
			_, err := DecodeRuleSetDefinition(key, json.RawMessage(test.payload))
			wrapped := apperror.FromError(apperror.KindBadRequest, err)
			if err == nil || apperror.CodeOf(wrapped) != test.code {
				t.Fatalf("code=%q want=%q err=%v", apperror.CodeOf(wrapped), test.code, err)
			}
			if apperror.ParamsOf(wrapped)["rule_set_key"] != key {
				t.Fatalf("params=%v want key=%q", apperror.ParamsOf(wrapped), key)
			}
		})
	}
}

func replaceOnce(value, old, replacement string) string {
	for index := 0; index+len(old) <= len(value); index++ {
		if value[index:index+len(old)] == old {
			return value[:index] + replacement + value[index+len(old):]
		}
	}
	return value
}

func TestDecodeRuleSetDefinitionCoversGenericEnvelopeFailures(t *testing.T) {
	base := `{"key":"policy.limit","name":"Limit policy","match_policy":"first_match","input_types":{"count":"integer"},"output_types":{"allowed":"boolean"},"effective_from":"2026-07-01","rules":[{"key":"limit","priority":10,"when":{"kind":"literal","value_type":"boolean","value":true},"outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":true}}}],"default_outputs":{"allowed":{"kind":"literal","value_type":"boolean","value":false}}}`
	cases := []struct {
		key     string
		payload string
	}{
		{key: "", payload: `{`},
		{key: "", payload: base + ` {}`},
		{key: "", payload: base + ` !`},
		{key: "", payload: replaceOnce(base, `"key":"policy.limit"`, `"key":""`)},
		{key: "", payload: replaceOnce(base, `"name":"Limit policy"`, `"name":""`)},
		{key: "", payload: replaceOnce(base, `"key":"policy.limit"`, `"key":"1invalid"`)},
		{key: "policy.limit", payload: replaceOnce(base, `"effective_from":"2026-07-01"`, `"effective_from":"invalid"`)},
		{key: "policy.limit", payload: replaceOnce(base, `"rules"`, `"effective_to":"invalid","rules"`)},
		{key: "policy.limit", payload: replaceOnce(base, `"rules"`, `"effective_to":"2026-07-01","rules"`)},
	}
	for _, test := range cases {
		if _, err := DecodeRuleSetDefinition(test.key, json.RawMessage(test.payload)); err == nil {
			t.Fatalf("accepted key=%q payload=%s", test.key, test.payload)
		}
	}
	if _, err := DecodeRuleSetDefinition(" ", json.RawMessage(base)); err != nil {
		t.Fatalf("blank resource key should defer to document key: %v", err)
	}
	if firstNonEmpty(" ", "") != "" {
		t.Fatal("all-empty fallback returned a value")
	}
}

func TestValidateRuleSetContractCoversGenericTypedFailures(t *testing.T) {
	assertInvalid := func(t *testing.T, mutate func(*rulesetmodel.RuleSetDefinition)) {
		t.Helper()
		definition := validRuleSetDefinition()
		mutate(&definition)
		if err := validateRuleSetContract(&definition); err == nil {
			t.Fatalf("accepted definition=%#v", definition)
		}
	}
	for _, mutate := range []func(*rulesetmodel.RuleSetDefinition){
		func(value *rulesetmodel.RuleSetDefinition) { value.InputTypes = nil },
		func(value *rulesetmodel.RuleSetDefinition) { value.OutputTypes = nil },
		func(value *rulesetmodel.RuleSetDefinition) { value.Rules = nil },
		func(value *rulesetmodel.RuleSetDefinition) { value.DefaultOutputs = nil },
		func(value *rulesetmodel.RuleSetDefinition) { value.InputTypes = map[string]string{"count": "unknown"} },
		func(value *rulesetmodel.RuleSetDefinition) { value.InputTypes = map[string]string{"1count": "integer"} },
		func(value *rulesetmodel.RuleSetDefinition) {
			value.InputTypes = map[string]string{"count": "integer", " count ": "integer"}
		},
		func(value *rulesetmodel.RuleSetDefinition) {
			value.OutputTypes = map[string]string{"allowed": "unknown"}
		},
		func(value *rulesetmodel.RuleSetDefinition) {
			value.OutputTypes = map[string]string{"1allowed": "boolean"}
		},
		func(value *rulesetmodel.RuleSetDefinition) {
			value.OutputTypes = map[string]string{"allowed": "boolean", " allowed ": "boolean"}
		},
		func(value *rulesetmodel.RuleSetDefinition) {
			value.DefaultOutputs = map[string]expressionmodel.BusinessExpression{"extra": literalRuleSetExpression("boolean", true)}
		},
		func(value *rulesetmodel.RuleSetDefinition) {
			value.DefaultOutputs["allowed"] = expressionmodel.BusinessExpression{Kind: "unknown"}
		},
		func(value *rulesetmodel.RuleSetDefinition) {
			value.DefaultOutputs["allowed"] = literalRuleSetExpression("text", "yes")
		},
		func(value *rulesetmodel.RuleSetDefinition) { value.Rules[0].Key = "1invalid" },
		func(value *rulesetmodel.RuleSetDefinition) { value.Rules[0].Priority = 0 },
		func(value *rulesetmodel.RuleSetDefinition) { value.Rules = append(value.Rules, value.Rules[0]) },
		func(value *rulesetmodel.RuleSetDefinition) {
			duplicate := value.Rules[0]
			duplicate.Key = "other"
			value.Rules = append(value.Rules, duplicate)
		},
		func(value *rulesetmodel.RuleSetDefinition) {
			value.Rules[0].When = expressionmodel.BusinessExpression{Kind: "unknown"}
		},
		func(value *rulesetmodel.RuleSetDefinition) {
			value.Rules[0].When = literalRuleSetExpression("integer", 1)
		},
		func(value *rulesetmodel.RuleSetDefinition) { value.Rules[0].Outputs = nil },
	} {
		t.Run("invalid", func(t *testing.T) { assertInvalid(t, mutate) })
	}

	definition := validRuleSetDefinition()
	definition.InputTypes = map[string]string{
		"flag": "boolean", "count": "integer", "amount": "decimal", "date": "date",
		"at": "datetime", "window": "duration", "label": "text",
	}
	if err := validateRuleSetContract(&definition); err != nil {
		t.Fatalf("all scalar input types rejected: %v", err)
	}
	if _, ok := ruleSetExpressionType("unknown"); ok {
		t.Fatal("unknown expression type accepted")
	}
}

func validRuleSetDefinition() rulesetmodel.RuleSetDefinition {
	return rulesetmodel.RuleSetDefinition{
		Key:           "policy.limit",
		Name:          "Limit policy",
		MatchPolicy:   rulesetmodel.RuleSetMatchFirst,
		InputTypes:    map[string]string{"count": "integer"},
		OutputTypes:   map[string]string{"allowed": "boolean"},
		EffectiveFrom: "2026-07-01",
		Rules: []rulesetmodel.RuleSetRule{{
			Key: "limit", Priority: 10,
			When:    literalRuleSetExpression("boolean", true),
			Outputs: map[string]expressionmodel.BusinessExpression{"allowed": literalRuleSetExpression("boolean", true)},
		}},
		DefaultOutputs: map[string]expressionmodel.BusinessExpression{"allowed": literalRuleSetExpression("boolean", false)},
	}
}

func literalRuleSetExpression(valueType string, value any) expressionmodel.BusinessExpression {
	return expressionmodel.BusinessExpression{Kind: "literal", ValueType: valueType, Value: value}
}
