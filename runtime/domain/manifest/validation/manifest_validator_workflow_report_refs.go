package validation

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	workflowvalidation "github.com/domainry/domainry-runtime/runtime/domain/workflow/validation"
)

func (state *validationState) validateWorkflows() {
	seen := map[string]bool{}
	for index, workflow := range state.manifest.Workflows {
		path := fmt.Sprintf("workflows[%d]", index)
		key := strings.TrimSpace(workflow.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if seen[key] {
			state.add(path+".key", "duplicate workflow key %q", key)
		}
		seen[key] = true
		for _, objectKey := range workflowObjectKeys(workflow) {
			if state.objects[objectKey].Key == "" {
				state.add(path+".trigger.object_key", "unknown object %q", objectKey)
			}
		}
		triggerType := ""
		if workflow.TriggerContract != nil {
			triggerType = strings.TrimSpace(workflow.TriggerContract.Type)
		}
		if workflow.TriggerContract == nil || strings.TrimSpace(workflow.TriggerContract.Type) == "" {
			state.add(path+".trigger_contract", "backend.workflow.trigger_contract_required")
		} else if !manifestWorkflowTriggerTypeSupported(triggerType) {
			state.add(path+".trigger_contract.type", "backend.workflow.trigger_type_invalid: %q", triggerType)
		}
		if workflow.TriggerContract != nil && workflow.TriggerContract.Type == "field_changed" && !state.workflowObjectHasField(workflow.TriggerContract.ObjectKey, workflow.TriggerContract.FieldKey) {
			state.add(path+".trigger_contract.field_key", "backend.workflow.field_not_found: %s.%s", workflow.TriggerContract.ObjectKey, workflow.TriggerContract.FieldKey)
		}
		if workflow.ConditionContract != nil && !workflowvalidation.WorkflowConditionContractIsValid(*workflow.ConditionContract) {
			state.add(path+".condition_contract", "backend.workflow.condition_contract_invalid")
		}
		legacyTriggerType := strings.ToLower(strings.TrimSpace(mapString(workflow.Trigger, "type")))
		phase := strings.ToLower(mapString(workflow.Trigger, "phase"))
		insertionPoint := mapString(workflow.Trigger, "insertion_point")
		if phase == "before" || phase == "after" || insertionPoint != "" || strings.HasPrefix(legacyTriggerType, "before_") || strings.HasPrefix(legacyTriggerType, "after_") || strings.HasPrefix(legacyTriggerType, "lifecycle_") {
			state.add(path+".trigger", "object lifecycle insertion points belong in automation_rules, not workflows")
		}
		if workflow.Graph == nil || workflow.Graph.Version != 2 || len(workflow.Graph.Nodes) == 0 {
			state.add(path+".graph", "graph v2 with typed nodes is required")
			continue
		}
		if err := workflowvalidation.WorkflowValidateGraph(workflow.Graph); err != nil {
			state.add(path+".graph", "%s", apperror.CodeOf(err))
		}
		for nodeIndex, node := range workflow.Graph.Nodes {
			nodePath := fmt.Sprintf("%s.graph.nodes[%d]", path, nodeIndex)
			switch node.Type {
			case "timer", "wait_duration", "wait_until":
				if node.Contract != nil && node.Contract.Timer != nil {
					state.validateWorkflowTimerBusinessCalendar(nodePath+".contract.timer", node.Contract.Timer.BusinessCalendarKey, node.Contract.Timer.Timezone)
				}
			case "approval":
				contract := manifestWorkflowApprovalNodeContract(node)
				state.validateWorkflowResolvers(nodePath+".contract.approval.resolvers", workflow, contract.Resolvers)
				state.validateWorkflowResolvers(nodePath+".contract.approval.escalation_resolvers", workflow, contract.EscalationResolvers)
				if contract.DueSeconds < 0 || contract.EscalationSeconds < 0 {
					state.add(nodePath+".contract.approval", "backend.workflow.approval_deadline_invalid")
				}
				if strings.TrimSpace(contract.ReminderActionKey) != "" {
					state.validateWorkflowActionReference(nodePath+".contract.approval.reminder_action_key", workflow, contract.ReminderActionKey, contract.ReminderInput)
				}
			case "action":
				if node.Contract == nil || node.Contract.Action == nil || strings.TrimSpace(node.Contract.Action.ActionKey) == "" {
					state.add(nodePath+".contract.action.action_key", "is required")
				} else if state.actions[strings.TrimSpace(node.Contract.Action.ActionKey)].Key == "" {
					state.add(nodePath+".contract.action.action_key", "unknown Business Action %q", node.Contract.Action.ActionKey)
				} else {
					state.validateWorkflowActionReference(nodePath+".contract.action", workflow, node.Contract.Action.ActionKey, node.Contract.Action.Input)
					if objectKey := strings.TrimSpace(node.Contract.Action.ObjectKey); objectKey != "" && state.objects[objectKey].Key == "" {
						state.add(nodePath+".contract.action.object_key", "backend.workflow.object_not_found: %q", objectKey)
					}
				}
			case "cc":
				if node.Contract == nil || node.Contract.CC == nil || strings.TrimSpace(node.Contract.CC.NotificationActionKey) == "" {
					state.add(nodePath+".contract.cc.notification_action_key", "is required")
				} else if state.actions[strings.TrimSpace(node.Contract.CC.NotificationActionKey)].Key == "" {
					state.add(nodePath+".contract.cc.notification_action_key", "unknown Business Action %q", node.Contract.CC.NotificationActionKey)
				} else {
					state.validateWorkflowResolvers(nodePath+".contract.cc.resolvers", workflow, node.Contract.CC.Resolvers)
					state.validateWorkflowActionReference(nodePath+".contract.cc", workflow, node.Contract.CC.NotificationActionKey, node.Contract.CC.Input)
				}
			}
		}
	}
}

func manifestWorkflowTriggerTypeSupported(value string) bool {
	switch strings.TrimSpace(value) {
	case "manual", "record_created", "record_updated", "field_changed", "action_completed", "scheduled", "integration_event":
		return true
	default:
		return false
	}
}

func manifestWorkflowApprovalNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowApprovalNodeContract {
	if node.Contract == nil || node.Contract.Approval == nil {
		return definitionmodel.WorkflowApprovalNodeContract{}
	}
	return *node.Contract.Approval
}

func (state *validationState) validateWorkflowActionReference(path string, workflow definitionmodel.WorkflowSchema, actionKey string, input map[string]any) {
	action, exists := state.actions[strings.TrimSpace(actionKey)]
	if !exists {
		state.add(path, "backend.workflow.action_not_registered: %q", actionKey)
		return
	}
	state.validateWorkflowActionInput(path, action, input)
}

func (state *validationState) validateWorkflowResolvers(path string, workflow definitionmodel.WorkflowSchema, resolvers []definitionmodel.WorkflowAssigneeResolver) {
	for index, resolver := range resolvers {
		resolverPath := fmt.Sprintf("%s[%d]", path, index)
		switch strings.TrimSpace(resolver.Type) {
		case "role":
			if strings.TrimSpace(resolver.RoleKey) == "" {
				state.add(resolverPath+".role_key", "backend.workflow.resolver_role_required")
			}
		case "record_user_field":
			field, found := state.workflowTriggerObjectField(workflow, resolver.Field)
			if !found {
				state.add(resolverPath+".field", "backend.workflow.resolver_field_not_found: %q", resolver.Field)
			} else if !manifestWorkflowIdentityField(field) {
				state.add(resolverPath+".field", "backend.workflow.resolver_field_type_invalid: %q", resolver.Field)
			}
		case "manager", "manager_of":
			field, found := state.workflowTriggerObjectField(workflow, resolver.UserField)
			if !found {
				state.add(resolverPath+".user_field", "backend.workflow.resolver_field_not_found: %q", resolver.UserField)
			} else if !manifestWorkflowIdentityField(field) {
				state.add(resolverPath+".user_field", "backend.workflow.resolver_field_type_invalid: %q", resolver.UserField)
			}
		case "relation_user":
			state.validateWorkflowRelationResolver(resolverPath, workflow, resolver, true)
		case "relation_role":
			state.validateWorkflowRelationResolver(resolverPath, workflow, resolver, false)
		case "manager_chain":
			if strings.TrimSpace(resolver.Source) == "record" {
				field, found := state.workflowTriggerObjectField(workflow, resolver.Field)
				if !found {
					state.add(resolverPath+".field", "backend.workflow.resolver_field_not_found: %q", resolver.Field)
				} else if !manifestWorkflowIdentityField(field) {
					state.add(resolverPath+".field", "backend.workflow.resolver_field_type_invalid: %q", resolver.Field)
				}
			}
		}
	}
}

func (state *validationState) validateWorkflowRelationResolver(path string, workflow definitionmodel.WorkflowSchema, resolver definitionmodel.WorkflowAssigneeResolver, userTerminal bool) {
	objectKeys := manifestWorkflowTriggerObjectKeys(workflow)
	if len(objectKeys) == 0 {
		state.add(path+".relation_path", "backend.workflow.resolver_object_not_found")
		return
	}
	for _, rootObjectKey := range objectKeys {
		objectKey := rootObjectKey
		for index, fieldKey := range resolver.RelationPath {
			fieldKey = strings.TrimSpace(fieldKey)
			field, found := state.fields[objectKey][fieldKey]
			isLast := index == len(resolver.RelationPath)-1
			if !found || field.DisabledAt != "" {
				state.add(path+".relation_path", "backend.workflow.resolver_field_not_found: %s.%s", objectKey, fieldKey)
				break
			}
			if userTerminal && isLast {
				if !manifestWorkflowIdentityField(field) {
					state.add(path+".relation_path", "backend.workflow.resolver_field_type_invalid: %s.%s", objectKey, fieldKey)
				}
				break
			}
			targetObjectKey := runtimeRelationTargetObject(field)
			if strings.TrimSpace(field.Type) != "relation" || targetObjectKey == "" || targetObjectKey == "identity_user" || targetObjectKey == "identity_role" {
				state.add(path+".relation_path", "backend.workflow.resolver_field_type_invalid: %s.%s", objectKey, fieldKey)
				break
			}
			if state.objects[targetObjectKey].Key == "" {
				state.add(path+".relation_path", "backend.workflow.resolver_object_not_found: %s", targetObjectKey)
				break
			}
			objectKey = targetObjectKey
			if !userTerminal && isLast {
				roleField, roleFound := state.fields[objectKey][strings.TrimSpace(resolver.RoleField)]
				if !roleFound || roleField.DisabledAt != "" {
					state.add(path+".role_field", "backend.workflow.resolver_field_not_found: %s.%s", objectKey, resolver.RoleField)
				} else if !manifestWorkflowRoleField(roleField) {
					state.add(path+".role_field", "backend.workflow.resolver_field_type_invalid: %s.%s", objectKey, resolver.RoleField)
				}
			}
		}
	}
}

func (state *validationState) workflowTriggerObjectsHaveField(workflow definitionmodel.WorkflowSchema, fieldKey string) bool {
	objectKeys := manifestWorkflowTriggerObjectKeys(workflow)
	if len(objectKeys) == 0 {
		return strings.TrimSpace(fieldKey) != ""
	}
	for _, objectKey := range objectKeys {
		if state.workflowObjectHasField(objectKey, fieldKey) {
			return true
		}
	}
	return false
}

func (state *validationState) workflowTriggerObjectField(workflow definitionmodel.WorkflowSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	fieldKey = strings.TrimSpace(fieldKey)
	objectKeys := manifestWorkflowTriggerObjectKeys(workflow)
	if fieldKey == "" || len(objectKeys) == 0 {
		return definitionmodel.FieldSchema{}, false
	}
	var common definitionmodel.FieldSchema
	for _, objectKey := range objectKeys {
		field, exists := state.fields[objectKey][fieldKey]
		if !exists || field.DisabledAt != "" || common.Key != "" && strings.TrimSpace(common.Type) != strings.TrimSpace(field.Type) {
			return definitionmodel.FieldSchema{}, false
		}
		common = field
	}
	return common, common.Key != ""
}

func manifestWorkflowIdentityField(field definitionmodel.FieldSchema) bool {
	switch strings.TrimSpace(field.Type) {
	case "user", "identity_user":
		return true
	case "relation":
		return runtimeRelationTargetObject(field) == "identity_user"
	default:
		return false
	}
}

func manifestWorkflowRoleField(field definitionmodel.FieldSchema) bool {
	switch strings.TrimSpace(field.Type) {
	case "text", "select", "multi_select", "role", "identity_role":
		return true
	case "relation":
		return runtimeRelationTargetObject(field) == "identity_role"
	default:
		return false
	}
}

func manifestWorkflowTriggerObjectKeys(workflow definitionmodel.WorkflowSchema) []string {
	if workflow.TriggerContract == nil {
		return []string{}
	}
	objectKeys := make([]string, 0, 1+len(workflow.TriggerContract.ObjectKeys))
	seen := map[string]bool{}
	for _, value := range append([]string{workflow.TriggerContract.ObjectKey}, workflow.TriggerContract.ObjectKeys...) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] {
			seen[value] = true
			objectKeys = append(objectKeys, value)
		}
	}
	return objectKeys
}

func (state *validationState) workflowObjectHasField(objectKey, fieldKey string) bool {
	fieldKey = strings.TrimSpace(fieldKey)
	if fieldKey == "id" || fieldKey == "created_at" || fieldKey == "updated_at" {
		return state.objects[strings.TrimSpace(objectKey)].Key != ""
	}
	field := state.fields[strings.TrimSpace(objectKey)][fieldKey]
	return strings.TrimSpace(field.Key) != "" && field.DisabledAt == ""
}

func (state *validationState) validateWorkflowActionInput(path string, action definitionmodel.ActionSchema, input map[string]any) {
	declared := map[string]definitionmodel.ActionPayloadField{}
	for _, field := range action.PayloadFields {
		fieldKey := strings.TrimSpace(field.Key)
		if fieldKey == "" {
			continue
		}
		declared[fieldKey] = field
		if field.Required && field.DefaultValue == nil && action.Defaults[fieldKey] == nil {
			if _, exists := input[fieldKey]; !exists {
				state.add(path+".input."+fieldKey, "required input for Business Action %q is missing", action.Key)
			}
		}
	}
	if len(declared) == 0 {
		return
	}
	for key := range input {
		if _, exists := declared[key]; !exists {
			state.add(path+".input."+key, "is not declared by Business Action %q", action.Key)
		}
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (state *validationState) validateReports() {
	seen := map[string]bool{}
	for index, report := range state.manifest.Reports {
		path := fmt.Sprintf("reports[%d]", index)
		key := strings.TrimSpace(report.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if seen[key] {
			state.add(path+".key", "duplicate report key %q", key)
		}
		seen[key] = true
		if report.ObjectSQLV1 == nil {
			state.add(path+".object_sql_v1", "backend.report.execution_definition_missing")
			continue
		}
		if _, err := reportcontract.CompileReportObjectSQL(*report.ObjectSQLV1, state.objects); err != nil {
			if planErr, ok := err.(*reportmodel.ReportObjectSQLPlanError); ok {
				state.add(path+"."+planErr.Path, "%s", planErr.Code)
			} else {
				state.add(path+".object_sql_v1", "backend.report.object_sql_invalid")
			}
		}
		sourceObjects := map[string]bool{}
		sources, _ := reportcontract.ReportObjectSQLSourceObjects(*report.ObjectSQLV1)
		for sourceIndex, objectKey := range sources {
			objectKey = strings.TrimSpace(objectKey)
			sourceObjects[objectKey] = true
			if state.objects[objectKey].Key == "" {
				sourcePath := path + ".object_sql_v1.sql"
				if len(report.ObjectSQLV1.SourceObjects) > 0 {
					sourcePath = fmt.Sprintf("%s.object_sql_v1.source_objects[%d]", path, sourceIndex)
				}
				state.add(sourcePath, "unknown object %q", objectKey)
			}
		}
		for evidenceIndex, requirement := range report.EvidenceRequirements {
			evidencePath := fmt.Sprintf("%s.evidence_requirements[%d]", path, evidenceIndex)
			objectKey := strings.TrimSpace(requirement.ObjectKey)
			if !sourceObjects[objectKey] {
				state.add(evidencePath+".object_key", "object %q is not a Report source", objectKey)
			}
			if requirement.MinimumRecords < 1 {
				state.add(evidencePath+".minimum_records", "must be at least 1")
			}
			for fieldIndex, fieldKey := range requirement.RequiredNonEmptyFields {
				if state.fields[objectKey][strings.TrimSpace(fieldKey)].Key == "" {
					state.add(fmt.Sprintf("%s.required_non_empty_fields[%d]", evidencePath, fieldIndex), "unknown field %q.%s", objectKey, fieldKey)
				}
			}
		}
	}
}

func workflowObjectKeys(workflow definitionmodel.WorkflowSchema) []string {
	keys := []string{}
	if workflow.TriggerContract != nil {
		if key := strings.TrimSpace(workflow.TriggerContract.ObjectKey); key != "" {
			keys = append(keys, key)
		}
		keys = append(keys, workflow.TriggerContract.ObjectKeys...)
	}
	if key := mapString(workflow.Trigger, "object_key"); key != "" {
		keys = append(keys, key)
	}
	if raw, ok := workflow.Trigger["object_keys"].([]any); ok {
		for _, item := range raw {
			if key := strings.TrimSpace(fmt.Sprint(item)); key != "" {
				keys = append(keys, key)
			}
		}
	}
	return keys
}
