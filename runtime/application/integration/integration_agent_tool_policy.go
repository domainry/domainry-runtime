package integration

import (
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"

	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type AgentToolGuardedWrite struct {
	ObjectKey string
	Operation string
	ActionKey string
	Endpoint  string
}

type AgentToolGuardedWriteLookup func(objectKey, operation string) (AgentToolGuardedWrite, bool)

type AgentToolRiskDecision struct {
	ConnectorKey     string
	RiskLevel        string
	Policy           string
	RequiresApproval bool
}

func AssessAgentToolRiskForGuardedWrites(toolKey string, input map[string]any, schema integrationmodel.IntegrationSchema, guardedWrites []AgentToolGuardedWrite) (AgentToolRiskDecision, error) {
	return AssessAgentToolRisk(toolKey, input, schema, func(objectKey, operation string) (AgentToolGuardedWrite, bool) {
		for _, contract := range guardedWrites {
			if contract.ObjectKey == objectKey && contract.Operation == operation && contract.ActionKey != "" {
				return contract, true
			}
		}
		return AgentToolGuardedWrite{}, false
	})
}

func CandidateAgentTools(agent agentmodel.AgentSchema) []string {
	if agent.Tools == nil {
		return []string{}
	}
	return agent.Tools
}

// AgentAllowsTool keeps the agent-tool allowlist rule in the Integration
// owner instead of requiring composition callers to duplicate slice matching.
func AgentAllowsTool(agent agentmodel.AgentSchema, toolKey string) bool {
	toolKey = strings.TrimSpace(toolKey)
	for _, candidate := range CandidateAgentTools(agent) {
		if strings.TrimSpace(candidate) == toolKey {
			return true
		}
	}
	return false
}

func AssessAgentToolRisk(toolKey string, input map[string]any, schema integrationmodel.IntegrationSchema, guardedWrite AgentToolGuardedWriteLookup) (AgentToolRiskDecision, error) {
	decision := AgentToolRiskDecision{RiskLevel: "low", Policy: "role_tool_allowlist", RequiresApproval: agentToolRequiresApproval(toolKey)}
	operation, objectKey := agentToolWriteOperation(toolKey), AgentToolObjectKey(input)
	if operation != "" && objectKey != "" && guardedWrite != nil {
		if contract, ok := guardedWrite(objectKey, operation); ok && contract.ActionKey != "" {
			decision.RiskLevel, decision.Policy, decision.RequiresApproval = "high", "guarded_business_action_required", true
			return decision, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.agent_tool.guarded_action_required", Params: map[string]string{
				"object_key": contract.ObjectKey, "operation": contract.Operation, "action_key": contract.ActionKey, "endpoint": contract.Endpoint,
			}}
		}
	}
	if !agentToolCallsExternalConnector(toolKey) {
		return decision, nil
	}
	decision.RiskLevel, decision.Policy, decision.RequiresApproval = "high", "connector_catalog_allowlist", true
	decision.ConnectorKey = agentToolConnectorKey(toolKey, input)
	if decision.ConnectorKey == "" {
		return decision, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.agent_tool.connector_required"}
	}
	if !SchemaHasConnector(schema, decision.ConnectorKey) {
		return decision, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.integration.agent_tool.connector_not_allowed"}
	}
	return decision, nil
}

func AgentToolApprovalPlan(agentKey, toolKey string, req integrationmodel.IntegrationAgentToolInvocationRequest, resolved integrationmodel.IntegrationExternalIdentityResolveResult, principal principalmodel.Principal, risk AgentToolRiskDecision, now time.Time) map[string]any {
	if !risk.RequiresApproval {
		return nil
	}
	operation := "agent." + strings.TrimSpace(agentKey) + "." + strings.TrimSpace(toolKey)
	return map[string]any{
		"queue_key": "integration_agent_tool_approval", "operation": operation, "risk_level": risk.RiskLevel, "risk_policy": risk.Policy,
		"connector_key": risk.ConnectorKey, "request_ref": strings.TrimSpace(req.RequestRef), "external_principal": resolved.ExternalPrincipal,
		"actor_id": principal.UserID, "role_key": principal.RoleKey, "approval_required": true,
		"expires_at":     now.UTC().Add(30 * time.Minute).Format(time.RFC3339),
		"review_steps":   []string{"verify_actor_role", "review_tool_input", "confirm_business_record_scope", "approve_or_reject_before_execution"},
		"approve_action": map[string]any{"method": "POST", "path": "/integrations/agents/" + strings.TrimSpace(agentKey) + "/tools/" + strings.TrimSpace(toolKey) + "/invoke", "body_patch": map[string]any{"approved": true}},
		"reject_action":  map[string]any{"status": "rejected", "audit_event": "integration_agent_tool_approval_rejected"},
		"retry_action":   map[string]any{"status": "retry_after_approval", "request_ref": strings.TrimSpace(req.RequestRef)},
		"rollback_plan":  map[string]any{"strategy": "no_provider_call_before_approval", "audit_event": "integration_agent_tool_approval_rolled_back", "requires_human_review": true},
	}
}

func AgentToolObjectKey(input map[string]any) string {
	for _, key := range []string{"object_key", "objectKey", "object"} {
		if value := strings.TrimSpace(fmt.Sprint(input[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func agentToolRequiresApproval(toolKey string) bool {
	switch strings.TrimSpace(toolKey) {
	case "createRecord", "updateRecord", "deleteRecord", "callConnector", "sendMessage", "sendEmail":
		return true
	default:
		return false
	}
}

func agentToolWriteOperation(toolKey string) string {
	switch strings.TrimSpace(toolKey) {
	case "createRecord":
		return "create"
	case "updateRecord":
		return "update"
	case "deleteRecord":
		return "delete"
	default:
		return ""
	}
}

func agentToolCallsExternalConnector(toolKey string) bool {
	switch strings.TrimSpace(toolKey) {
	case "callConnector", "sendMessage", "sendEmail":
		return true
	default:
		return false
	}
}

func agentToolConnectorKey(toolKey string, input map[string]any) string {
	for _, key := range []string{"connector_key", "connectorKey", "connector"} {
		if value := strings.TrimSpace(fmt.Sprint(input[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	if strings.TrimSpace(toolKey) == "sendEmail" {
		return "email"
	}
	return ""
}

func SchemaHasConnector(schema integrationmodel.IntegrationSchema, connectorKey string) bool {
	connectorKey = strings.TrimSpace(connectorKey)
	for _, connector := range schema.Connectors {
		if strings.TrimSpace(connector.Key) == connectorKey && connectorKey != "" {
			return true
		}
	}
	return false
}
