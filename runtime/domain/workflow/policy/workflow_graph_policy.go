package policy

import (
	"strings"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func WorkflowValidateGraph(graph *definitionmodel.WorkflowGraphSchema) error {
	if graph == nil {
		return badRequest("backend.workflow.graph_v2_required")
	}
	if graph.Version != 2 || len(graph.Nodes) == 0 {
		return badRequest("backend.workflow.graph_invalid")
	}
	allowedTypes := map[string]bool{"trigger": true, "condition": true, "approval": true, "cc": true, "action": true, "agent_task": true, "wait_until": true, "wait_duration": true, "timer": true}
	allowedApprovalModes := map[string]bool{"all": true, "any": true, "sequential": true}
	nodes := make(map[string]definitionmodel.WorkflowGraphNode, len(graph.Nodes))
	triggerID := ""
	for _, node := range graph.Nodes {
		id := strings.TrimSpace(node.ID)
		typeName := strings.TrimSpace(node.Type)
		if id == "" || !allowedTypes[typeName] || nodes[id].ID != "" {
			return badRequest("backend.workflow.graph_node_invalid", "node", id)
		}
		if typeName == "trigger" {
			if triggerID != "" {
				return badRequest("backend.workflow.graph_trigger_required")
			}
			triggerID = id
		}
		if typeName == "approval" {
			if node.Contract == nil || node.Contract.Approval == nil {
				return badRequest("backend.workflow.approval_resolver_required", "node", id)
			}
			contract := workflowApprovalNodeContract(node)
			mode := strings.TrimSpace(contract.Mode)
			if !allowedApprovalModes[mode] {
				return badRequest("backend.workflow.graph_approval_mode_invalid", "node", id)
			}
			if len(contract.Resolvers) == 0 {
				return badRequest("backend.workflow.approval_resolver_required", "node", id)
			}
			for _, resolver := range contract.Resolvers {
				if !validWorkflowAssigneeResolver(resolver) {
					return badRequest("backend.workflow.approval_resolver_invalid", "node", id, "resolver", resolver.Type)
				}
			}
			switch valueOrDefault(strings.TrimSpace(contract.EmptyAssigneePolicy), "fail") {
			case "fail", "skip", "admin":
			default:
				return badRequest("backend.workflow.approval_empty_policy_invalid", "node", id)
			}
		}
		if typeName == "condition" {
			if node.Contract == nil || node.Contract.Condition == nil || !WorkflowConditionContractIsValid(*node.Contract.Condition) {
				return badRequest("backend.workflow.condition_contract_invalid", "node", id)
			}
		}
		if typeName == "action" {
			if node.Contract == nil || node.Contract.Action == nil {
				return badRequest("backend.workflow.action_key_required", "node", id)
			}
			contract := workflowBusinessActionNodeContract(node)
			if contract.ActionKey == "" {
				return badRequest("backend.workflow.action_key_required", "node", id)
			}
			if contract.TimeoutSeconds < 0 || (contract.Retry != nil && contract.Retry.MaxAttempts < 1) {
				return badRequest("backend.workflow.action_execution_policy_invalid", "node", id)
			}
			switch valueOrDefault(strings.TrimSpace(contract.OnError), "fail") {
			case "fail", "continue", "error_branch":
			default:
				return badRequest("backend.workflow.action_error_policy_invalid", "node", id)
			}
		}
		if typeName == "agent_task" {
			if node.Contract == nil {
				return badRequest("backend.workflow.agent_task_contract_required", "node", id)
			}
			if node.Contract.AgentTask == nil {
				return badRequest("backend.workflow.agent_task_contract_required", "node", id)
			}
			contract := workflowAgentTaskNodeContract(node)
			if strings.TrimSpace(contract.TaskKey) == "" || strings.TrimSpace(contract.TaskVersion) == "" || contract.Input == nil || strings.TrimSpace(contract.OutputVariable) == "" {
				return badRequest("backend.workflow.agent_task_contract_invalid", "node", id)
			}
			if strings.TrimSpace(contract.ExecutionMode) != "async" {
				return badRequest("backend.workflow.agent_task_execution_policy_invalid", "node", id)
			}
			if contract.TimeoutSeconds < 1 {
				return badRequest("backend.workflow.agent_task_execution_policy_invalid", "node", id)
			}
			if contract.Retry != nil && contract.Retry.MaxAttempts < 1 {
				return badRequest("backend.workflow.agent_task_execution_policy_invalid", "node", id)
			}
			switch strings.TrimSpace(contract.Identity.Mode) {
			case agentmodel.AgentTaskIdentityInherit:
				if strings.TrimSpace(contract.Identity.PrincipalKey) != "" {
					return badRequest("backend.workflow.agent_task_identity_invalid", "node", id)
				}
			case agentmodel.AgentTaskIdentityService:
				if strings.TrimSpace(contract.Identity.PrincipalKey) == "" {
					return badRequest("backend.workflow.agent_task_identity_invalid", "node", id)
				}
			default:
				return badRequest("backend.workflow.agent_task_identity_invalid", "node", id)
			}
			switch valueOrDefault(strings.TrimSpace(contract.OnError), "fail") {
			case "fail", "error_branch":
			default:
				return badRequest("backend.workflow.agent_task_error_policy_invalid", "node", id)
			}
			allowedOutcomes := map[string]bool{}
			for _, outcome := range agentmodel.AgentTaskOutcomes {
				allowedOutcomes[outcome] = true
			}
			seenOutcomes := map[string]bool{}
			for _, outcome := range contract.AllowedOutcomes {
				outcome = strings.TrimSpace(outcome)
				if !allowedOutcomes[outcome] || seenOutcomes[outcome] {
					return badRequest("backend.workflow.agent_task_outcome_invalid", "node", id)
				}
				seenOutcomes[outcome] = true
			}
		}
		if typeName == "cc" {
			if node.Contract == nil || node.Contract.CC == nil {
				return badRequest("backend.workflow.cc_contract_required", "node", id)
			}
			contract := workflowCCNodeContract(node)
			if contract.NotificationActionKey == "" || len(contract.Resolvers) == 0 {
				return badRequest("backend.workflow.cc_contract_required", "node", id)
			}
		}
		if typeName == "wait_until" || typeName == "wait_duration" || typeName == "timer" {
			if node.Contract == nil || node.Contract.Timer == nil || !validWorkflowTimerNodeContract(typeName, *node.Contract.Timer) {
				return badRequest("backend.workflow.timer_contract_invalid", "node", id)
			}
		}
		node.ID = id
		node.Type = typeName
		nodes[id] = node
	}
	if triggerID == "" {
		return badRequest("backend.workflow.graph_trigger_required")
	}
	adjacency := map[string][]string{}
	branches := map[string]map[string]bool{}
	for _, edge := range graph.Edges {
		source := strings.TrimSpace(edge.Source)
		target := strings.TrimSpace(edge.Target)
		if nodes[source].ID == "" || nodes[target].ID == "" || source == target {
			return badRequest("backend.workflow.graph_edge_invalid", "edge", edge.ID)
		}
		adjacency[source] = append(adjacency[source], target)
		branch := strings.ToLower(strings.TrimSpace(valueOrDefault(edge.Branch, edge.Label)))
		if branches[source] == nil {
			branches[source] = map[string]bool{}
		}
		if branch != "" && branches[source][branch] {
			return badRequest("backend.workflow.graph_branch_duplicate", "node", source, "branch", branch)
		}
		branches[source][branch] = true
	}
	for nodeID, outgoing := range branches {
		node := nodes[nodeID]
		switch node.Type {
		case "condition":
			if outgoing[""] || hasUnsupportedWorkflowBranch(outgoing, "true", "false") {
				return badRequest("backend.workflow.condition_branch_invalid", "node", nodeID)
			}
		case "approval":
			if outgoing[""] || hasUnsupportedWorkflowBranch(outgoing, "approved", "rejected", "returned", "skipped") {
				return badRequest("backend.workflow.approval_branch_invalid", "node", nodeID)
			}
			if !outgoing["approved"] || !outgoing["rejected"] {
				return badRequest("backend.workflow.approval_outcomes_required", "node", nodeID)
			}
		case "action":
			if workflowBusinessActionNodeContract(node).OnError == "error_branch" && !outgoing["error"] {
				return badRequest("backend.workflow.action_error_branch_required", "node", nodeID)
			}
		case "agent_task":
			allowed := map[string]bool{}
			for _, outcome := range agentmodel.AgentTaskOutcomes {
				allowed[outcome] = true
			}
			for branch := range outgoing {
				if branch == "" || !allowed[branch] {
					return badRequest("backend.workflow.agent_task_branch_invalid", "node", nodeID)
				}
			}
			if workflowAgentTaskNodeContract(node).OnError == "error_branch" {
				if !outgoing["error"] {
					return badRequest("backend.workflow.agent_task_error_branch_required", "node", nodeID)
				}
			}
		}
	}
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return badRequest("backend.workflow.graph_cycle")
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, target := range adjacency[id] {
			if err := visit(target); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	if err := visit(triggerID); err != nil {
		return err
	}
	if len(visited) != len(nodes) {
		return badRequest("backend.workflow.graph_disconnected")
	}
	return nil
}

func badRequest(code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: values}
}

func workflowApprovalNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowApprovalNodeContract {
	if node.Contract != nil && node.Contract.Approval != nil {
		return *node.Contract.Approval
	}
	return definitionmodel.WorkflowApprovalNodeContract{}
}

func workflowBusinessActionNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowBusinessActionNodeContract {
	if node.Contract != nil && node.Contract.Action != nil {
		return *node.Contract.Action
	}
	return definitionmodel.WorkflowBusinessActionNodeContract{}
}

func workflowAgentTaskNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowAgentTaskNodeContract {
	if node.Contract != nil && node.Contract.AgentTask != nil {
		return *node.Contract.AgentTask
	}
	return definitionmodel.WorkflowAgentTaskNodeContract{}
}

func workflowCCNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowCCNodeContract {
	if node.Contract != nil && node.Contract.CC != nil {
		return *node.Contract.CC
	}
	return definitionmodel.WorkflowCCNodeContract{}
}

func WorkflowTimerNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowTimerNodeContract {
	if node.Contract != nil && node.Contract.Timer != nil {
		return *node.Contract.Timer
	}
	return definitionmodel.WorkflowTimerNodeContract{}
}

func validWorkflowTimerNodeContract(nodeType string, contract definitionmodel.WorkflowTimerNodeContract) bool {
	timezone := strings.TrimSpace(contract.Timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return false
	}
	hasAt := strings.TrimSpace(contract.At) != ""
	hasSource := strings.TrimSpace(contract.SourceField) != ""
	if hasAt {
		if _, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(contract.At)); err != nil {
			return false
		}
	}
	switch strings.TrimSpace(nodeType) {
	case "wait_until":
		return hasAt != hasSource && contract.DurationSeconds == 0
	case "wait_duration":
		return contract.DurationSeconds > 0 && !hasAt && !hasSource && strings.TrimSpace(contract.BusinessCalendarKey) == ""
	case "timer":
		if strings.TrimSpace(contract.TimerKey) == "" || strings.TrimSpace(contract.Purpose) == "" {
			return false
		}
		if strings.TrimSpace(contract.BusinessCalendarKey) != "" {
			return hasAt != hasSource
		}
		return contract.DurationSeconds > 0 && !hasAt && !hasSource || contract.DurationSeconds == 0 && hasAt != hasSource
	default:
		return false
	}
}

func validWorkflowAssigneeResolver(resolver definitionmodel.WorkflowAssigneeResolver) bool {
	switch strings.TrimSpace(resolver.Type) {
	case "users":
		seen := map[string]bool{}
		for _, userID := range resolver.UserIDs {
			if userID = strings.TrimSpace(userID); userID != "" {
				seen[userID] = true
			}
		}
		return len(seen) > 0
	case "record_field":
		return strings.TrimSpace(resolver.Field) != ""
	case "manager", "manager_of":
		return strings.TrimSpace(resolver.UserField) != ""
	case "initiator_manager":
		return true
	case "role":
		return strings.TrimSpace(resolver.RoleKey) != ""
	default:
		return false
	}
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func hasUnsupportedWorkflowBranch(branches map[string]bool, allowed ...string) bool {
	valid := map[string]bool{}
	for _, branch := range allowed {
		valid[branch] = true
	}
	for branch := range branches {
		if branch != "" && !valid[branch] {
			return true
		}
	}
	return false
}

func WorkflowConditionContractIsValid(contract definitionmodel.WorkflowConditionContract) bool {
	switch valueOrDefault(strings.TrimSpace(contract.Type), "always") {
	case "always":
		return true
	case "field_equals", "field_changed":
		return strings.TrimSpace(contract.Field) != ""
	case "expression":
		return strings.TrimSpace(contract.Expression) != ""
	case "all", "and", "any", "or":
		if len(contract.Conditions) == 0 {
			return false
		}
		for _, child := range contract.Conditions {
			if !WorkflowConditionContractIsValid(child) {
				return false
			}
		}
		return true
	case "not":
		return contract.Condition != nil && WorkflowConditionContractIsValid(*contract.Condition)
	default:
		return false
	}
}
