package validation

import (
	"fmt"
	"strings"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func validateAgentExecutionLimits(state *validationState, path string, limits agentmodel.AgentExecutionLimits) {
	for key, value := range map[string]int{"max_steps": limits.MaxSteps, "timeout_seconds": limits.TimeoutSeconds, "max_tool_calls": limits.MaxToolCalls, "max_input_bytes": limits.MaxInputBytes, "max_output_bytes": limits.MaxOutputBytes} {
		if value < 0 {
			state.add(path+"."+key, "must not be negative")
		}
	}
	if budget := strings.ToLower(strings.TrimSpace(limits.CostBudget)); budget != "" {
		switch budget {
		case "low", "standard", "high":
		default:
			state.add(path+".cost_budget", "must be one of low, standard, high")
		}
	}
}

func validateAgentJSONSchema(state *validationState, path string, schema map[string]any) {
	if schema == nil {
		state.add(path, "is required")
		return
	}
	value, ok := schema["type"].(string)
	if !ok {
		state.add(path+".type", "must be object")
	} else if strings.TrimSpace(value) != "object" {
		state.add(path+".type", "must be object")
	}
	validateAgentJSONSchemaValue(state, path, schema, schema, 0)
}

func validateAgentJSONSchemaValue(state *validationState, path string, value any, root map[string]any, depth int) {
	if depth > 32 {
		state.add(path, "exceeds maximum schema depth 32")
		return
	}
	switch typed := value.(type) {
	case map[string]any:
		if rawRef, exists := typed["$ref"]; exists {
			ref, ok := rawRef.(string)
			if !ok || strings.TrimSpace(ref) == "" {
				state.add(path+".$ref", "must be a non-empty string")
			} else {
				validateAgentJSONSchemaRef(state, path+".$ref", strings.TrimSpace(ref), root)
			}
		}
		for key, child := range typed {
			if key != "$ref" {
				validateAgentJSONSchemaValue(state, path+"."+key, child, root, depth+1)
			}
		}
	case []any:
		for index, child := range typed {
			validateAgentJSONSchemaValue(state, fmt.Sprintf("%s[%d]", path, index), child, root, depth+1)
		}
	}
}

func validateAgentJSONSchemaRef(state *validationState, path, ref string, root map[string]any) {
	switch {
	case strings.HasPrefix(ref, "#/$defs/"):
		defs, _ := root["$defs"].(map[string]any)
		if _, exists := defs[strings.TrimPrefix(ref, "#/$defs/")]; !exists {
			state.add(path, "references unknown local schema %q", ref)
		}
	case strings.HasPrefix(ref, "domainry://objects/"):
		key := strings.TrimPrefix(ref, "domainry://objects/")
		if _, exists := state.objects[key]; !exists {
			state.add(path, "references unknown object schema %q", key)
		}
	case strings.HasPrefix(ref, "domainry://actions/"):
		key := strings.TrimPrefix(ref, "domainry://actions/")
		if _, exists := state.actions[key]; !exists {
			state.add(path, "references unknown Action schema %q", key)
		}
	default:
		state.add(path, "uses unsupported schema reference %q", ref)
	}
}

func validateAgentTaskOutcomes(state *validationState, path string, outcomes []string) {
	allowed := stringSet(agentmodel.AgentTaskOutcomes)
	seen := map[string]bool{}
	if len(outcomes) == 0 {
		state.add(path, "must declare at least one outcome")
	}
	for index, outcome := range outcomes {
		outcome = strings.TrimSpace(outcome)
		if !allowed[outcome] {
			state.add(fmt.Sprintf("%s[%d]", path, index), "unsupported outcome %q", outcome)
		} else if seen[outcome] {
			state.add(fmt.Sprintf("%s[%d]", path, index), "duplicate outcome %q", outcome)
		}
		seen[outcome] = true
	}
}

func agentAllowedObjects(agent agentmodel.AgentSchema, skills map[string]agentmodel.SkillSchema) map[string]bool {
	allowed := map[string]bool{}
	for _, skillKey := range agent.SkillKeys {
		for _, objectKey := range skills[strings.TrimSpace(skillKey)].AllowedObjects {
			allowed[strings.TrimSpace(objectKey)] = true
		}
	}
	return allowed
}

func agentAllowsCapability(agent agentmodel.AgentSchema, skills map[string]agentmodel.SkillSchema, capability string) bool {
	for _, tool := range agent.Tools {
		if strings.TrimSpace(tool) == capability {
			return true
		}
	}
	for _, skillKey := range agent.SkillKeys {
		for _, tool := range skills[strings.TrimSpace(skillKey)].AllowedTools {
			if strings.TrimSpace(tool) == capability {
				return true
			}
		}
	}
	return false
}

func validateGlobalAgentContextContract(state *validationState, path string, contract agentmodel.GlobalAgentContextContract) {
	if contract.ContractVersion != agentmodel.GlobalAgentContextContractVersion {
		state.add(path+".contract_version", "must be %q", agentmodel.GlobalAgentContextContractVersion)
	}
	allowedHints := stringSet([]string{"route_key", "object_key", "record_id", "selected_record_ids", "locale", "timezone"})
	seen := map[string]bool{}
	for index, field := range contract.AllowedHintFields {
		field = strings.TrimSpace(field)
		if !allowedHints[field] {
			state.add(fmt.Sprintf("%s.allowed_hint_fields[%d]", path, index), "unsupported context hint %q", field)
		} else if seen[field] {
			state.add(fmt.Sprintf("%s.allowed_hint_fields[%d]", path, index), "duplicate context hint %q", field)
		}
		seen[field] = true
	}
	if contract.MaxSelectedRecord < 1 {
		state.add(path+".max_selected_records", "must be between 1 and 100")
	} else if contract.MaxSelectedRecord > 100 {
		state.add(path+".max_selected_records", "must be between 1 and 100")
	}
	if contract.MaxContextBytes < 1024 {
		state.add(path+".max_context_bytes", "must be between 1024 and 1048576")
	} else if contract.MaxContextBytes > 1048576 {
		state.add(path+".max_context_bytes", "must be between 1024 and 1048576")
	}
}

func validateAgentRoutingContract(state *validationState, path string, contract agentmodel.AgentRoutingContract) {
	if contract.ContractVersion != agentmodel.AgentRoutingContractVersion {
		state.add(path+".contract_version", "must be %q", agentmodel.AgentRoutingContractVersion)
	}
	allowed := stringSet([]string{agentmodel.AgentRouteInteractiveQuery, agentmodel.AgentRouteTask, agentmodel.AgentRouteWorkflow, agentmodel.AgentRouteProposal})
	seen := map[string]bool{}
	if len(contract.AllowedRouteTypes) == 0 {
		state.add(path+".allowed_route_types", "must declare at least one route type")
	}
	for index, routeType := range contract.AllowedRouteTypes {
		routeType = strings.TrimSpace(routeType)
		if !allowed[routeType] {
			state.add(fmt.Sprintf("%s.allowed_route_types[%d]", path, index), "unsupported route type %q", routeType)
		} else if seen[routeType] {
			state.add(fmt.Sprintf("%s.allowed_route_types[%d]", path, index), "duplicate route type %q", routeType)
		}
		seen[routeType] = true
	}
	if contract.AllowRecursive {
		state.add(path+".allow_recursive", "recursive Agent delegation is not supported")
	}
}

func validAgentRoutePattern(pattern string, entrypointKeys map[string]bool) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || strings.ContainsAny(pattern, " /?#\\") || strings.Count(pattern, "*") > 1 {
		return false
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if prefix == "" {
			return false
		}
		for key := range entrypointKeys {
			if strings.HasPrefix(key, prefix) {
				return true
			}
		}
		return false
	}
	return entrypointKeys[pattern]
}

func agentRoutePatternMatches(pattern, key string) bool {
	pattern = strings.TrimSpace(pattern)
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(key, strings.TrimSuffix(pattern, "*"))
	}
	return key == pattern
}

func runtimeEntrypointSurface(entrypoint definitionmodel.EntryPointSchema) string {
	switch strings.TrimSpace(fmt.Sprint(entrypoint.Config["kind"])) {
	case "backoffice", "operator_console", "business_workspace":
		return "business_workspace"
	case "admin_console":
		return "admin_console"
	case "customer_portal", "consumer_portal":
		return "consumer_portal"
	default:
		return ""
	}
}

func validateAgentStringSet(state *validationState, path string, values []string) {
	seen := map[string]bool{}
	for index, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			state.add(fmt.Sprintf("%s[%d]", path, index), "must not be empty")
		} else if seen[value] {
			state.add(fmt.Sprintf("%s[%d]", path, index), "duplicate value %q", value)
		}
		seen[value] = true
	}
}

func validateAgentNodeAllowlist(state *validationState, path string, contract definitionmodel.WorkflowAgentTaskNodeContract, task agentmodel.AgentTaskDefinition, taskExists bool) {
	if !taskExists {
		return
	}
	objects := stringSet(task.AllowedObjects)
	for index, objectKey := range contract.AllowedObjects {
		if !objects[strings.TrimSpace(objectKey)] {
			state.add(fmt.Sprintf("%s.allowed_objects[%d]", path, index), "object %q is outside Agent Task allowlist", objectKey)
		}
	}
	actions := stringSet(task.AllowedActions)
	for index, actionKey := range contract.AllowedActions {
		if !actions[strings.TrimSpace(actionKey)] {
			state.add(fmt.Sprintf("%s.allowed_actions[%d]", path, index), "Action %q is outside Agent Task allowlist", actionKey)
		}
	}
	outcomes := stringSet(task.AllowedOutcomes)
	for index, outcome := range contract.AllowedOutcomes {
		if !outcomes[strings.TrimSpace(outcome)] {
			state.add(fmt.Sprintf("%s.allowed_outcomes[%d]", path, index), "outcome %q is outside Agent Task outcomes", outcome)
		}
	}
}

func stringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[strings.TrimSpace(value)] = true
	}
	return result
}
