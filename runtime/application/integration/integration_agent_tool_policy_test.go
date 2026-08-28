package integration

import (
	"reflect"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestCandidateAgentToolsAndAllowlist(t *testing.T) {
	if tools := CandidateAgentTools(agentmodel.AgentSchema{}); tools == nil || len(tools) != 0 {
		t.Fatalf("nil tools projection = %#v", tools)
	}
	agent := agentmodel.AgentSchema{Tools: []string{" readRecord ", "callConnector"}}
	if got := CandidateAgentTools(agent); !reflect.DeepEqual(got, agent.Tools) {
		t.Fatalf("candidate tools = %#v", got)
	}
	if !AgentAllowsTool(agent, " readRecord ") || AgentAllowsTool(agent, "deleteRecord") {
		t.Fatal("agent tool allowlist did not normalize or reject as expected")
	}
}

func TestAssessAgentToolRiskForGuardedWrites(t *testing.T) {
	guarded := []AgentToolGuardedWrite{
		{ObjectKey: "invoice", Operation: "update"},
		{ObjectKey: "invoice", Operation: "create", ActionKey: "create_invoice", Endpoint: "/actions/create-invoice"},
		{ObjectKey: "invoice", Operation: "update", ActionKey: "approve_change", Endpoint: "/actions/approve-change"},
		{ObjectKey: "invoice", Operation: "delete", ActionKey: "void_invoice", Endpoint: "/actions/void-invoice"},
	}
	tests := []struct {
		tool, operation, action, endpoint string
	}{
		{tool: "createRecord", operation: "create", action: "create_invoice", endpoint: "/actions/create-invoice"},
		{tool: "updateRecord", operation: "update", action: "approve_change", endpoint: "/actions/approve-change"},
		{tool: "deleteRecord", operation: "delete", action: "void_invoice", endpoint: "/actions/void-invoice"},
	}
	for _, test := range tests {
		decision, err := AssessAgentToolRiskForGuardedWrites(
			test.tool,
			map[string]any{"object_key": "invoice"},
			integrationmodel.IntegrationSchema{},
			guarded,
		)
		if apperror.CodeOf(err) != "backend.integration.agent_tool.guarded_action_required" {
			t.Fatalf("%s guarded write error = %v", test.tool, err)
		}
		if decision.RiskLevel != "high" || decision.Policy != "guarded_business_action_required" || !decision.RequiresApproval {
			t.Fatalf("%s guarded decision = %#v", test.tool, decision)
		}
		wantParams := map[string]string{
			"object_key": "invoice", "operation": test.operation, "action_key": test.action, "endpoint": test.endpoint,
		}
		if got := apperror.ParamsOf(err); !reflect.DeepEqual(got, wantParams) {
			t.Fatalf("%s guarded error params = %#v", test.tool, got)
		}
	}

	decision, err := AssessAgentToolRiskForGuardedWrites("createRecord", map[string]any{"object_key": "contact"}, integrationmodel.IntegrationSchema{}, guarded)
	if err != nil || decision.RiskLevel != "low" || !decision.RequiresApproval {
		t.Fatalf("unguarded write decision = %#v err=%v", decision, err)
	}
	if _, err := AssessAgentToolRisk("createRecord", nil, integrationmodel.IntegrationSchema{}, func(string, string) (AgentToolGuardedWrite, bool) { return AgentToolGuardedWrite{}, false }); err != nil {
		t.Fatalf("object-less guarded lookup error=%v", err)
	}
	if _, err := AssessAgentToolRisk("createRecord", map[string]any{"object_key": "invoice"}, integrationmodel.IntegrationSchema{}, nil); err != nil {
		t.Fatalf("nil guarded lookup error=%v", err)
	}
	if _, err := AssessAgentToolRisk("createRecord", map[string]any{"object_key": "invoice"}, integrationmodel.IntegrationSchema{}, func(string, string) (AgentToolGuardedWrite, bool) {
		return AgentToolGuardedWrite{ObjectKey: "invoice", Operation: "create"}, true
	}); err != nil {
		t.Fatalf("action-less guarded contract error=%v", err)
	}
}

func TestAssessAgentToolRiskConnectorPolicy(t *testing.T) {
	schema := integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "slack"}, {Key: " email "}}}

	t.Run("non external tool", func(t *testing.T) {
		decision, err := AssessAgentToolRisk("readRecord", nil, schema, nil)
		if err != nil || decision.RiskLevel != "low" || decision.Policy != "role_tool_allowlist" || decision.RequiresApproval {
			t.Fatalf("decision = %#v err=%v", decision, err)
		}
	})

	t.Run("connector required", func(t *testing.T) {
		decision, err := AssessAgentToolRisk("callConnector", nil, schema, nil)
		if apperror.CodeOf(err) != "backend.integration.agent_tool.connector_required" || decision.ConnectorKey != "" || !decision.RequiresApproval {
			t.Fatalf("decision = %#v err=%v", decision, err)
		}
	})

	t.Run("connector forbidden", func(t *testing.T) {
		decision, err := AssessAgentToolRisk("sendMessage", map[string]any{"connectorKey": "teams"}, schema, nil)
		if apperror.CodeOf(err) != "backend.integration.agent_tool.connector_not_allowed" || decision.ConnectorKey != "teams" {
			t.Fatalf("decision = %#v err=%v", decision, err)
		}
	})

	t.Run("connector allowed", func(t *testing.T) {
		decision, err := AssessAgentToolRisk("callConnector", map[string]any{"connector": " slack "}, schema, nil)
		if err != nil || decision.ConnectorKey != "slack" || decision.RiskLevel != "high" || decision.Policy != "connector_catalog_allowlist" || !decision.RequiresApproval {
			t.Fatalf("decision = %#v err=%v", decision, err)
		}
	})

	t.Run("email default connector", func(t *testing.T) {
		decision, err := AssessAgentToolRisk("sendEmail", map[string]any{}, schema, nil)
		if err != nil || decision.ConnectorKey != "email" {
			t.Fatalf("decision = %#v err=%v", decision, err)
		}
	})
}

func TestAgentToolApprovalPlan(t *testing.T) {
	if plan := AgentToolApprovalPlan("agent", "readRecord", integrationmodel.IntegrationAgentToolInvocationRequest{}, integrationmodel.IntegrationExternalIdentityResolveResult{}, principalmodel.Principal{}, AgentToolRiskDecision{}, time.Time{}); plan != nil {
		t.Fatalf("low-risk plan = %#v", plan)
	}

	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	plan := AgentToolApprovalPlan(
		" operations ",
		" callConnector ",
		integrationmodel.IntegrationAgentToolInvocationRequest{RequestRef: " request-1 "},
		integrationmodel.IntegrationExternalIdentityResolveResult{ExternalPrincipal: "external-user"},
		accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}, accessfixture.Bundle{Key: "operator"}),
		AgentToolRiskDecision{ConnectorKey: "slack", RiskLevel: "high", Policy: "connector_catalog_allowlist", RequiresApproval: true},
		now,
	)
	if plan["operation"] != "agent.operations.callConnector" || plan["request_ref"] != "request-1" || plan["connector_key"] != "slack" {
		t.Fatalf("approval plan identity = %#v", plan)
	}
	if plan["expires_at"] != now.UTC().Add(30*time.Minute).Format(time.RFC3339) {
		t.Fatalf("expires_at = %#v", plan["expires_at"])
	}
	approve := plan["approve_action"].(map[string]any)
	if approve["path"] != "/integrations/agents/operations/tools/callConnector/invoke" {
		t.Fatalf("approve action = %#v", approve)
	}
	if len(plan["review_steps"].([]string)) != 4 || plan["approval_required"] != true {
		t.Fatalf("approval controls = %#v", plan)
	}
}

func TestAgentToolPolicyNormalizationHelpers(t *testing.T) {
	for _, test := range []struct {
		input map[string]any
		want  string
	}{
		{input: map[string]any{"object_key": nil, "objectKey": "invoice"}, want: "invoice"},
		{input: map[string]any{"object_key": "", "objectKey": "invoice"}, want: "invoice"},
		{input: map[string]any{"object": 42}, want: "42"},
		{input: nil, want: ""},
	} {
		if got := AgentToolObjectKey(test.input); got != test.want {
			t.Fatalf("object key for %#v = %q, want %q", test.input, got, test.want)
		}
	}

	for _, tool := range []string{"createRecord", "updateRecord", "deleteRecord", "callConnector", "sendMessage", "sendEmail"} {
		if !agentToolRequiresApproval(" " + tool + " ") {
			t.Fatalf("tool %q must require approval", tool)
		}
	}
	if agentToolRequiresApproval("readRecord") {
		t.Fatal("readRecord unexpectedly requires approval")
	}

	writes := map[string]string{"createRecord": "create", "updateRecord": "update", "deleteRecord": "delete", "readRecord": ""}
	for tool, want := range writes {
		if got := agentToolWriteOperation(" " + tool + " "); got != want {
			t.Fatalf("write operation for %q = %q, want %q", tool, got, want)
		}
	}

	for _, tool := range []string{"callConnector", "sendMessage", "sendEmail"} {
		if !agentToolCallsExternalConnector(" " + tool + " ") {
			t.Fatalf("tool %q must be external", tool)
		}
	}
	if agentToolCallsExternalConnector("updateRecord") {
		t.Fatal("updateRecord unexpectedly classified as external")
	}

	connectorCases := []struct {
		tool  string
		input map[string]any
		want  string
	}{
		{tool: "callConnector", input: map[string]any{"connector_key": nil, "connectorKey": "slack"}, want: "slack"},
		{tool: "callConnector", input: map[string]any{"connector_key": "", "connectorKey": "slack"}, want: "slack"},
		{tool: "sendMessage", input: map[string]any{"connector": 7}, want: "7"},
		{tool: " sendEmail ", input: nil, want: "email"},
		{tool: "callConnector", input: nil, want: ""},
	}
	for _, test := range connectorCases {
		if got := agentToolConnectorKey(test.tool, test.input); got != test.want {
			t.Fatalf("connector key for %q %#v = %q, want %q", test.tool, test.input, got, test.want)
		}
	}

	schema := integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: " slack "}, {Key: ""}}}
	if !SchemaHasConnector(schema, " slack ") || SchemaHasConnector(schema, "") || SchemaHasConnector(schema, "teams") {
		t.Fatal("schema connector matching failed")
	}
}
