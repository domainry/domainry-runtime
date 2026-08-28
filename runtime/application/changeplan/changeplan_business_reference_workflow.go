package changeplan

import changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func AddWorkflowReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, snapshot ReferenceSchema) {
	for _, workflow := range snapshot.Workflows {
		builder.Node("workflow", workflow.Key, workflowObjectKey(workflow), workflow.Name, "")
		if runAs := strings.TrimSpace(workflow.RunAs); runAs != "" && runAs != "initiator" {
			builder.Edge("workflow", workflow.Key, "role", runAs, "runs_as_role", "run_as")
		}
		addWorkflowTriggerReferences(builder, workflow)
		addWorkflowConditionReferences(builder, workflow.Key, workflowObjectKey(workflow), workflow.ConditionContract, "condition_contract")
		if workflow.ActionContract != nil {
			addWorkflowActionReference(builder, workflow.Key, *workflow.ActionContract, "action_contract")
		}
		if workflow.Graph == nil {
			continue
		}
		for index, node := range workflow.Graph.Nodes {
			path := fmt.Sprintf("graph.nodes[%d]", index)
			if node.Contract == nil {
				continue
			}
			if node.Contract.Condition != nil {
				addWorkflowConditionReferences(builder, workflow.Key, workflowObjectKey(workflow), node.Contract.Condition, path+".contract.condition")
			}
			if node.Contract.Action != nil {
				action := node.Contract.Action
				builder.Edge("workflow", workflow.Key, "action", action.ActionKey, "invokes_action", path+".contract.action.action_key")
				builder.Edge("workflow", workflow.Key, "object", action.ObjectKey, "operates_on", path+".contract.action.object_key")
				addExpressionFieldReferences(builder, "workflow", workflow.Key, workflowObjectKey(workflow), action.Input, path+".contract.action.input")
			}
			if node.Contract.Approval != nil {
				for resolverIndex, resolver := range node.Contract.Approval.Resolvers {
					addWorkflowResolverReferences(builder, workflow.Key, workflowObjectKey(workflow), resolver, fmt.Sprintf("%s.contract.approval.resolvers[%d]", path, resolverIndex))
				}
				builder.Edge("workflow", workflow.Key, "action", node.Contract.Approval.ReminderActionKey, "invokes_reminder_action", path+".contract.approval.reminder_action_key")
			}
			if node.Contract.CC != nil {
				builder.Edge("workflow", workflow.Key, "action", node.Contract.CC.NotificationActionKey, "invokes_notification_action", path+".contract.cc.notification_action_key")
				for resolverIndex, resolver := range node.Contract.CC.Resolvers {
					addWorkflowResolverReferences(builder, workflow.Key, workflowObjectKey(workflow), resolver, fmt.Sprintf("%s.contract.cc.resolvers[%d]", path, resolverIndex))
				}
			}
			if node.Contract.Timer != nil {
				addWorkflowTimerReferences(builder, workflow.Key, workflowObjectKey(workflow), node.ID, *node.Contract.Timer, path+".contract.timer")
			}
		}
	}
}

func addWorkflowTimerReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, workflowKey, objectKey, nodeID string, timer definitionmodel.WorkflowTimerNodeContract, path string) {
	timerKey := strings.TrimSpace(timer.TimerKey)
	if timerKey == "" {
		timerKey = strings.TrimSpace(workflowKey) + "." + strings.TrimSpace(nodeID)
	}
	builder.Node("timer", timerKey, objectKey, timer.Purpose, "")
	builder.Edge("timer", timerKey, "workflow", workflowKey, "resumes_workflow", path)
	if field := strings.TrimSpace(timer.SourceField); objectKey != "" && field != "" {
		builder.Edge("timer", timerKey, "field", objectKey+"."+field, "reads_schedule_field", path+".source_field")
	}
	if calendar := strings.TrimSpace(timer.BusinessCalendarKey); calendar != "" {
		builder.Edge("timer", timerKey, "business_calendar", calendar, "uses_business_calendar", path+".business_calendar_key")
	}
}

func workflowObjectKey(workflow definitionmodel.WorkflowSchema) string {
	if workflow.TriggerContract != nil {
		if strings.TrimSpace(workflow.TriggerContract.ObjectKey) != "" {
			return strings.TrimSpace(workflow.TriggerContract.ObjectKey)
		}
		for _, objectKey := range workflow.TriggerContract.ObjectKeys {
			if strings.TrimSpace(objectKey) != "" {
				return strings.TrimSpace(objectKey)
			}
		}
	}
	if workflow.ActionContract != nil {
		return strings.TrimSpace(workflow.ActionContract.ObjectKey)
	}
	return ""
}

func addWorkflowTriggerReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, workflow definitionmodel.WorkflowSchema) {
	if workflow.TriggerContract == nil {
		return
	}
	trigger := workflow.TriggerContract
	objects := append([]string{trigger.ObjectKey}, trigger.ObjectKeys...)
	for _, objectKey := range objects {
		builder.Edge("workflow", workflow.Key, "object", strings.TrimSpace(objectKey), "triggered_by_object", "trigger_contract.object_key")
	}
	if objectKey := workflowObjectKey(workflow); objectKey != "" && strings.TrimSpace(trigger.FieldKey) != "" {
		builder.Edge("workflow", workflow.Key, "field", objectKey+"."+strings.TrimSpace(trigger.FieldKey), "triggered_by_field", "trigger_contract.field_key")
	}
}

func addWorkflowConditionReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, workflowKey, objectKey string, condition *definitionmodel.WorkflowConditionContract, path string) {
	if condition == nil {
		return
	}
	if field := strings.TrimSpace(condition.Field); objectKey != "" && field != "" {
		builder.Edge("workflow", workflowKey, "field", objectKey+"."+field, "reads_field", path+".field")
		if value, ok := condition.Value.(string); ok && strings.TrimSpace(value) != "" {
			builder.Edge("workflow", workflowKey, "state_value", objectKey+"."+field+":"+strings.TrimSpace(value), "checks_state_value", path+".value")
		}
	}
	addExpressionFieldReferences(builder, "workflow", workflowKey, objectKey, condition.Expression, path+".expression")
	for index := range condition.Conditions {
		child := condition.Conditions[index]
		addWorkflowConditionReferences(builder, workflowKey, objectKey, &child, fmt.Sprintf("%s.conditions[%d]", path, index))
	}
	addWorkflowConditionReferences(builder, workflowKey, objectKey, condition.Condition, path+".condition")
}

func addWorkflowActionReference(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, workflowKey string, action definitionmodel.WorkflowActionContract, path string) {
	builder.Edge("workflow", workflowKey, "object", action.ObjectKey, "operates_on", path+".object_key")
	builder.Edge("workflow", workflowKey, "action", action.ActionKey, "invokes_action", path+".action_key")
}

func addWorkflowResolverReferences(builder *changeplanprojection.ChangePlanReferenceGraphBuilder, workflowKey, objectKey string, resolver definitionmodel.WorkflowAssigneeResolver, path string) {
	builder.Edge("workflow", workflowKey, "role", resolver.RoleKey, "resolves_role", path+".role_key")
	field := strings.TrimSpace(referenceValueOrDefault(resolver.UserField, resolver.Field))
	if objectKey != "" && field != "" {
		builder.Edge("workflow", workflowKey, "field", objectKey+"."+field, "resolves_from_field", path+".field")
	}
}
