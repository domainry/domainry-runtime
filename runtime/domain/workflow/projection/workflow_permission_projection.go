package projection

import (
	"encoding/json"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func WorkflowBusinessDefinition(workflow definitionmodel.WorkflowSchema, actions []definitionmodel.ActionSchema) definitionmodel.WorkflowSchema {
	visible := workflowCloneSchema(workflow)
	visible.DefinitionVersionID, visible.PublishedVersion = "", 0
	visible.Trigger, visible.Condition, visible.Action = nil, nil, nil
	visible.TriggerContract, visible.ConditionContract, visible.ActionContract = nil, nil, nil
	visible.RunAs, visible.IdempotencyKeys, visible.Retry, visible.DeadLetterPolicy = "", nil, nil, nil
	visible.TimeoutSeconds, visible.AuditEvent = 0, ""
	if visible.Graph == nil {
		return visible
	}
	for index := range visible.Graph.Nodes {
		node := &visible.Graph.Nodes[index]
		node.Config = workflowNodeBusinessSummary(*node, actions)
		node.Contract = nil
	}
	return visible
}

func WorkflowProcessForPrincipal(process workflowmodel.WorkflowProcessInstance, advanced bool, actions []definitionmodel.ActionSchema) workflowmodel.WorkflowProcessInstance {
	if advanced {
		return process
	}
	visible := process
	visible.DefinitionVersionID, visible.DefinitionHash, visible.ErrorCode = "", "", ""
	visible.DefinitionSnapshot = WorkflowBusinessDefinition(process.DefinitionSnapshot, actions)
	return visible
}

func WorkflowTaskForPrincipal(task workflowmodel.WorkflowTask, advanced bool) workflowmodel.WorkflowTask {
	if advanced {
		return task
	}
	visible := task
	visible.ResolverSnapshot, visible.CandidateSource, visible.NodeDefinitionVersion = nil, "", 0
	return visible
}

func WorkflowNodeForPrincipal(node workflowmodel.WorkflowNodeInstance, advanced bool) workflowmodel.WorkflowNodeInstance {
	if advanced {
		return node
	}
	visible := node
	visible.Input, visible.Output, visible.ErrorCode = nil, nil, ""
	return visible
}

func WorkflowEventForPrincipal(event workflowmodel.WorkflowProcessEvent, advanced bool) workflowmodel.WorkflowProcessEvent {
	if advanced {
		return event
	}
	visible := event
	visible.Metadata = nil
	return visible
}

func workflowNodeBusinessSummary(node definitionmodel.WorkflowGraphNode, actions []definitionmodel.ActionSchema) map[string]any {
	summary := map[string]any{"business_name": node.Name}
	switch node.Type {
	case "trigger":
		summary["business_summary_key"] = "workflow.node.trigger.summary"
	case "condition":
		summary["business_summary_key"] = "workflow.node.condition.summary"
	case "approval":
		contract := workflowpolicy.WorkflowApprovalNodeContract(node)
		summary["approval_mode"], summary["assignee_summary_keys"] = contract.Mode, workflowAssigneeBusinessSummaryKeys(contract.Resolvers)
	case "action":
		contract := workflowpolicy.WorkflowBusinessActionNodeContract(node)
		summary["action_key"] = contract.ActionKey
		summary["action_name"], summary["action_name_key"], summary["impact_summary"], summary["impact_summary_key"] = workflowActionBusinessDescription(contract.ActionKey, actions)
	case "cc":
		contract := workflowpolicy.WorkflowCCNodeContract(node)
		summary["action_key"] = contract.NotificationActionKey
		summary["notification_name"], summary["notification_name_key"], summary["impact_summary"], summary["impact_summary_key"] = workflowActionBusinessDescription(contract.NotificationActionKey, actions)
		summary["recipient_summary_keys"] = workflowAssigneeBusinessSummaryKeys(contract.Resolvers)
	case "wait_until", "wait_duration", "timer":
		contract := workflowpolicy.WorkflowTimerNodeContract(node)
		summary["business_summary_key"] = "workflow.node.timer.summary"
		summary["timer_key"], summary["purpose"] = contract.TimerKey, contract.Purpose
	}
	return summary
}

func workflowActionBusinessDescription(actionKey string, actions []definitionmodel.ActionSchema) (name, nameKey, impact, impactKey string) {
	for _, action := range actions {
		if action.Key != actionKey {
			continue
		}
		name = workflowValueOrDefault(action.Label, action.Key)
		impact, impactKey = "", "workflow.action.recordUpdateImpact"
		return name, "", impact, impactKey
	}
	return "", "workflow.action.defaultName", "", "workflow.action.defaultImpact"
}

func workflowAssigneeBusinessSummaryKeys(resolvers []definitionmodel.WorkflowAssigneeResolver) []string {
	parts := []string{}
	for _, resolver := range resolvers {
		switch resolver.Type {
		case "users":
			parts = append(parts, "workflow.assignee.users")
		case "role":
			parts = append(parts, "workflow.assignee.role")
		case "manager", "manager_of", "initiator_manager":
			parts = append(parts, "workflow.assignee.manager")
		case "record_field":
			parts = append(parts, "workflow.assignee.recordOwner")
		default:
			parts = append(parts, "workflow.assignee.configured")
		}
	}
	sort.Strings(parts)
	return parts
}

func workflowCloneSchema(workflow definitionmodel.WorkflowSchema) definitionmodel.WorkflowSchema {
	encoded, _ := json.Marshal(workflow)
	var cloned definitionmodel.WorkflowSchema
	_ = json.Unmarshal(encoded, &cloned)
	return cloned
}

func workflowValueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
