package agentmodel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentContractsStrictDecodeRejectsSemanticAndStructuralEscalation(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		out  any
		want string
	}{
		{name: "identity mode", raw: `{"mode":"root"}`, out: &AgentTaskIdentity{}, want: "unsupported Agent Task identity mode"},
		{name: "identity privilege field", raw: `{"mode":"inherit","role_key":"admin"}`, out: &AgentTaskIdentity{}, want: "unknown field"},
		{name: "side effect mode", raw: `{"side_effect_mode":"direct_write","allowed_outcomes":["success"]}`, out: &AgentTaskDefinition{}, want: "unsupported Agent Task side_effect_mode"},
		{name: "outcome", raw: `{"side_effect_mode":"analysis_only","allowed_outcomes":["admin"]}`, out: &AgentTaskDefinition{}, want: "unsupported Agent Task outcome"},
		{name: "task privilege field", raw: `{"side_effect_mode":"analysis_only","allowed_outcomes":["success"],"run_as":"admin"}`, out: &AgentTaskDefinition{}, want: "unknown field"},
		{name: "context hint", raw: `{"allowed_hint_fields":["principal"]}`, out: &GlobalAgentContextContract{}, want: "unsupported Global Agent context hint"},
		{name: "context privilege field", raw: `{"allowed_hint_fields":["route_key"],"workspace_id":"other"}`, out: &GlobalAgentContextContract{}, want: "unknown field"},
		{name: "route type", raw: `{"allowed_route_types":["admin"]}`, out: &AgentRoutingContract{}, want: "unsupported Agent route type"},
		{name: "routing privilege field", raw: `{"allowed_route_types":["agent_task"],"allowed_roles":["admin"]}`, out: &AgentRoutingContract{}, want: "unknown field"},
		{name: "handoff route", raw: `{"route_type":"interactive_query"}`, out: &InteractiveAgentHandoff{}, want: "unsupported durable handoff route_type"},
		{name: "handoff privilege field", raw: `{"route_type":"agent_task","run_as":"admin"}`, out: &InteractiveAgentHandoff{}, want: "unknown field"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := json.Unmarshal([]byte(test.raw), test.out)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAgentContractsStrictDecodeAcceptsPublishedValues(t *testing.T) {
	for _, test := range []struct {
		raw string
		out any
	}{
		{raw: `{"mode":"inherit"}`, out: &AgentTaskIdentity{}},
		{raw: `{"side_effect_mode":"analysis_only","allowed_outcomes":["success","error"]}`, out: &AgentTaskDefinition{}},
		{raw: `{"allowed_hint_fields":["route_key","record_id"]}`, out: &GlobalAgentContextContract{}},
		{raw: `{"allowed_route_types":["interactive_query","agent_task","workflow","proposal"]}`, out: &AgentRoutingContract{}},
		{raw: `{"route_type":"workflow"}`, out: &InteractiveAgentHandoff{}},
	} {
		if err := json.Unmarshal([]byte(test.raw), test.out); err != nil {
			t.Fatalf("published Agent contract rejected for %s: %v", test.raw, err)
		}
	}
}
