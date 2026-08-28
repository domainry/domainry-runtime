package agentdialog

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestAgentAnalysisProposalSuggestionDefaultsAndBindings(t *testing.T) {
	if got := agentAnalysisProposalSuggestion(nil, "intent", "query-1"); got != nil {
		t.Fatalf("nil spec = %#v", got)
	}
	for _, spec := range []map[string]any{{}, {"follow_up": "invalid"}, {"follow_up": map[string]any{}}} {
		if got := agentAnalysisProposalSuggestion(spec, "intent", "query-1"); got != nil {
			t.Fatalf("invalid follow-up = %#v", got)
		}
	}

	suggestion := agentAnalysisProposalSuggestion(map[string]any{
		"anomaly_type": "custom_signal",
		"follow_up": map[string]any{
			"action_binding":   map[string]any{"action_key": "follow_up.create"},
			"workflow_binding": map[string]any{"workflow_key": "review"},
			"diff":             []any{map[string]any{"field": "status"}},
			"affected_records": []any{"record-1"},
		},
	}, "Investigate signal", "query-1")
	if suggestion["title"] != "Create follow-up task" || suggestion["summary"] != "Investigate signal" || suggestion["kind"] != "analysis_follow_up" || suggestion["risk_level"] != "medium" || suggestion["reference"] != "query-1" {
		t.Fatalf("default suggestion = %#v", suggestion)
	}
	policy := suggestion["recommendation_policy"].(map[string]any)
	if policy["policy_id"] != "analysis_policy.generic_follow_up" || policy["anomaly_type"] != "custom_signal" || policy["recommended_action"] != "create_follow_up_proposal" || policy["review_required"] != true {
		t.Fatalf("generic policy = %#v", policy)
	}
	proposed := suggestion["proposed"].(map[string]any)
	for _, key := range []string{"action_binding", "workflow_binding", "diff", "affected_records", "recommendation_policy"} {
		if proposed[key] == nil {
			t.Fatalf("proposed missing %q: %#v", key, proposed)
		}
	}
}

func TestAgentAnalysisRecommendationPolicyExplicitAndEmpty(t *testing.T) {
	if got := agentAnalysisRecommendationPolicy(nil, map[string]any{}, ""); got != nil {
		t.Fatalf("empty policy = %#v", got)
	}
	policy := agentAnalysisRecommendationPolicy(nil, map[string]any{"recommendation_policy": map[string]any{
		"policy_id": " custom.policy ", "recommended_action": " custom_action ", "reason": " because ", "severity": " high ",
	}}, "low")
	if policy["policy_id"] != "custom.policy" || policy["anomaly_type"] != "analysis_anomaly" || policy["recommended_action"] != "custom_action" || policy["reason"] != "because" || policy["severity"] != "high" {
		t.Fatalf("explicit policy = %#v", policy)
	}

	defaults := agentAnalysisRecommendationPolicy(nil, map[string]any{"anomaly_type": " custom "}, "")
	if defaults["severity"] != "medium" || defaults["reason"] == "" {
		t.Fatalf("policy defaults = %#v", defaults)
	}
	if agentAnalysisPolicyString(nil) != "" || agentAnalysisPolicyString(" value ") != "value" || agentAnalysisPolicyString(42) != "42" {
		t.Fatal("policy string normalization mismatch")
	}
	if agentAnalysisDefaultPolicyID("any-business-signal") != "analysis_policy.generic_follow_up" || agentAnalysisDefaultRecommendedAction("any-business-signal") != "create_follow_up_proposal" {
		t.Fatal("Runtime default policy must remain business-agnostic")
	}
}

func TestAgentAnalysisProposalSuggestionExplicitValues(t *testing.T) {
	suggestion := agentAnalysisProposalSuggestion(map[string]any{"follow_up": map[string]any{
		"title": " Review ", "summary": " Summary ", "kind": " custom ", "risk_level": " high ", "reference": " ref-1 ", "rollback": " undo ",
	}}, "fallback", "query")
	if suggestion["title"] != "Review" || suggestion["summary"] != "Summary" || suggestion["kind"] != "custom" || suggestion["risk_level"] != "high" || suggestion["reference"] != "ref-1" || suggestion["rollback"] != "undo" {
		t.Fatalf("explicit suggestion = %#v", suggestion)
	}
	if len(suggestion["proposed"].(map[string]any)) != 0 {
		t.Fatalf("unexpected proposed payload = %#v", suggestion["proposed"])
	}
	if got := agentAnalysisStringFromMap(nil, "title"); got != "" {
		t.Fatalf("nil map string = %q", got)
	}
	if got := agentAnalysisStringFromMap(map[string]any{"title": 42}, "title"); got != "" {
		t.Fatalf("non-string map value = %q", got)
	}
}

func TestAgentAnalysisProposalHTML(t *testing.T) {
	if got := agentAnalysisProposalHTML(nil); got != "" {
		t.Fatalf("nil suggestion HTML = %q", got)
	}
	if got := agentAnalysisRecommendationPolicyHTML(nil); got != "" {
		t.Fatalf("nil policy HTML = %q", got)
	}

	suggestion := map[string]any{
		"title": "<Review>", "summary": "A & B", "kind": "", "risk_level": "", "reference": `ref"1`, "rollback": "undo",
		"recommendation_policy": map[string]any{"policy_id": "policy-1", "anomaly_type": "custom", "severity": "high", "recommended_action": "review", "reason": "Because <risk>"},
		"proposed":              map[string]any{"affected_records": []any{"record-1"}, "diff": []any{map[string]any{"field": "status", "before": "new", "after": "review"}}},
	}
	html := agentAnalysisProposalHTML(suggestion)
	for _, fragment := range []string{"proposal_card", "&lt;Review&gt;", "A &amp; B", `data-risk-level="medium"`, `data-source-count="1"`, "recommendation_policy", `data-proposed-json="`} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("proposal HTML missing %q: %s", fragment, html)
		}
	}
	if strings.Contains(html, "Because <risk>") {
		t.Fatalf("proposal HTML did not escape policy reason: %s", html)
	}

	defaults := agentAnalysisProposalHTML(map[string]any{"title": "", "proposed": map[string]any{}})
	if !strings.Contains(defaults, "Create follow-up task") || strings.Contains(defaults, "data-proposed-json") {
		t.Fatalf("default proposal HTML = %s", defaults)
	}
}

func TestAgentAnalysisProposalPayloadHelpers(t *testing.T) {
	items := []any{"one"}
	if got := agentAnalysisAnyList(items); len(got) != 1 || got[0] != "one" {
		t.Fatalf("any list = %#v", got)
	}
	if got := agentAnalysisAnyList([]string{"one"}); got != nil {
		t.Fatalf("typed string list = %#v", got)
	}
	encoded := agentAnalysisEncodeProposalPayload(map[string]any{"record": "record-1"})
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(raw) != `{"record":"record-1"}` {
		t.Fatalf("encoded payload = %q raw %q err %v", encoded, raw, err)
	}
	if got := agentAnalysisEncodeProposalPayload(map[string]any{"invalid": make(chan int)}); got != "" {
		t.Fatalf("unencodable payload = %q", got)
	}
}
