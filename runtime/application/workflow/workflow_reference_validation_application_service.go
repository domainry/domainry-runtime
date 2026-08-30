package workflow

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"fmt"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	workflowvalidation "github.com/domainry/domainry-runtime/runtime/domain/workflow/validation"
)

type WorkflowReferenceValidator struct {
	schema    WorkflowSchemaProvider
	objectMap func(context.Context) map[string]definitionmodel.ObjectSchema
	identity  identitysdk.Directory
}

func NewWorkflowReferenceValidator(schema WorkflowSchemaProvider, objectMap func(context.Context) map[string]definitionmodel.ObjectSchema, identity identitysdk.Directory) *WorkflowReferenceValidator {
	return &WorkflowReferenceValidator{schema: schema, objectMap: objectMap, identity: identity}
}

func (s *WorkflowReferenceValidator) validateWorkflowReferences(ctx context.Context, workflow definitionmodel.WorkflowSchema) []workflowmodel.WorkflowValidationIssue {
	issues := []workflowmodel.WorkflowValidationIssue{}
	actions := s.workflowActions(ctx)
	issues = append(issues, s.validateWorkflowTriggerContract(ctx, workflow)...)
	if workflow.Graph == nil {
		return issues
	}
	userIDs, roleKeys := s.workflowIdentityReferenceCatalog(ctx)
	for _, node := range workflow.Graph.Nodes {
		switch node.Type {
		case "approval":
			contract := workflowpolicy.WorkflowApprovalNodeContract(node)
			issues = append(issues, s.validateWorkflowResolvers(ctx, workflow, node.ID, contract.Resolvers, userIDs, roleKeys)...)
			issues = append(issues, s.validateWorkflowResolvers(ctx, workflow, node.ID, contract.EscalationResolvers, userIDs, roleKeys)...)
			if contract.DueSeconds < 0 || contract.EscalationSeconds < 0 {
				issues = append(issues, workflowReferenceIssue("backend.workflow.approval_deadline_invalid", node.ID, node.ID))
			}
			if contract.ReminderActionKey != "" {
				issues = append(issues, s.validateWorkflowActionBinding(ctx, node.ID, contract.ReminderActionKey, contract.ReminderInput)...)
				if action, exists := actions[contract.ReminderActionKey]; exists {
					issues = append(issues, s.validateWorkflowRunAsActionPermission(ctx, workflow, node.ID, action)...)
					issues = append(issues, s.validateWorkflowActionDependencies(ctx, node.ID, action)...)
				}
			}
		case "cc":
			contract := workflowpolicy.WorkflowCCNodeContract(node)
			issues = append(issues, s.validateWorkflowResolvers(ctx, workflow, node.ID, contract.Resolvers, userIDs, roleKeys)...)
			issues = append(issues, s.validateWorkflowActionBinding(ctx, node.ID, contract.NotificationActionKey, contract.Input)...)
			if action, exists := actions[contract.NotificationActionKey]; exists {
				issues = append(issues, s.validateWorkflowRunAsActionPermission(ctx, workflow, node.ID, action)...)
				issues = append(issues, s.validateWorkflowActionDependencies(ctx, node.ID, action)...)
			}
		case "action":
			contract := workflowpolicy.WorkflowBusinessActionNodeContract(node)
			issues = append(issues, s.validateWorkflowActionBinding(ctx, node.ID, contract.ActionKey, contract.Input)...)
			if action, exists := actions[contract.ActionKey]; exists {
				issues = append(issues, s.validateWorkflowRunAsActionPermission(ctx, workflow, node.ID, action)...)
				issues = append(issues, s.validateWorkflowActionDependencies(ctx, node.ID, action)...)
			}
			if contract.ObjectKey != "" {
				if _, exists := s.objectMap(ctx)[contract.ObjectKey]; !exists {
					issues = append(issues, workflowReferenceIssue("backend.workflow.object_not_found", node.ID, contract.ObjectKey))
				}
			}
		case "agent_task":
			issues = append(issues, s.validateWorkflowAgentTaskReference(ctx, node)...)
		}
	}
	return issues
}

func (s *WorkflowReferenceValidator) validateWorkflowAgentTaskReference(ctx context.Context, node definitionmodel.WorkflowGraphNode) []workflowmodel.WorkflowValidationIssue {
	if node.Contract == nil {
		return nil
	}
	if node.Contract.AgentTask == nil {
		return nil
	}
	contract := node.Contract.AgentTask
	snapshot := s.schema.WorkflowSchemaSnapshot(ctx, principalmodel.Principal{})
	var task agentsdk.AgentTaskDefinition
	found := false
	for _, candidate := range snapshot.AgentTasks {
		if !candidate.Enabled {
			continue
		}
		if strings.TrimSpace(candidate.Key) != strings.TrimSpace(contract.TaskKey) {
			continue
		}
		if strings.TrimSpace(candidate.Version) == strings.TrimSpace(contract.TaskVersion) {
			task, found = candidate, true
			break
		}
	}
	if !found {
		return []workflowmodel.WorkflowValidationIssue{workflowReferenceIssue("backend.workflow.agent_task_not_published", node.ID, contract.TaskKey+"@"+contract.TaskVersion)}
	}
	issues := []workflowmodel.WorkflowValidationIssue{}
	if contract.Identity.Mode == agentsdk.AgentTaskIdentityService {
		bindingFound := false
		for _, binding := range snapshot.AgentServicePrincipals {
			if !binding.Enabled {
				continue
			}
			if strings.TrimSpace(binding.Key) == strings.TrimSpace(contract.Identity.PrincipalKey) {
				bindingFound = true
				break
			}
		}
		if !bindingFound {
			issues = append(issues, workflowReferenceIssue("backend.workflow.agent_service_principal_not_published", node.ID, contract.Identity.PrincipalKey))
		}
	}
	actions := s.workflowActions(ctx)
	allowedActions := contract.AllowedActions
	if len(allowedActions) == 0 {
		allowedActions = task.AllowedActions
	}
	for _, actionKey := range allowedActions {
		action, exists := actions[strings.TrimSpace(actionKey)]
		if !exists {
			issues = append(issues, workflowReferenceIssue("backend.workflow.agent_action_not_published", node.ID, actionKey))
			continue
		}
		_ = actionmodel.ActionRiskLevel(action)
	}
	return issues
}

func (s *WorkflowReferenceValidator) validateWorkflowTriggerContract(ctx context.Context, workflow definitionmodel.WorkflowSchema) []workflowmodel.WorkflowValidationIssue {
	if workflow.TriggerContract == nil || strings.TrimSpace(workflow.TriggerContract.Type) == "" {
		return []workflowmodel.WorkflowValidationIssue{workflowReferenceIssue("backend.workflow.trigger_contract_required", "", workflow.Key)}
	}
	contract := workflow.TriggerContract
	if !workflowpolicy.WorkflowTriggerContractTypeIsValid(contract.Type) {
		return []workflowmodel.WorkflowValidationIssue{workflowReferenceIssue("backend.workflow.trigger_type_invalid", "", contract.Type)}
	}
	issues := []workflowmodel.WorkflowValidationIssue{}
	for _, objectKey := range append([]string{contract.ObjectKey}, contract.ObjectKeys...) {
		if objectKey != "" {
			if _, exists := s.objectMap(ctx)[objectKey]; !exists {
				issues = append(issues, workflowReferenceIssue("backend.workflow.object_not_found", "", objectKey))
			}
		}
	}
	if contract.Type == "field_changed" && !s.objectHasWorkflowField(ctx, contract.ObjectKey, contract.FieldKey) {
		issues = append(issues, workflowReferenceIssue("backend.workflow.field_not_found", "", contract.ObjectKey+"."+contract.FieldKey))
	}
	if workflow.ConditionContract != nil && !workflowvalidation.WorkflowConditionContractIsValid(*workflow.ConditionContract) {
		issues = append(issues, workflowReferenceIssue("backend.workflow.condition_contract_invalid", "", workflow.Key))
	}
	return issues
}

func (s *WorkflowReferenceValidator) validateWorkflowActionDependencies(ctx context.Context, nodeID string, action definitionmodel.ActionSchema) []workflowmodel.WorkflowValidationIssue {
	issues := []workflowmodel.WorkflowValidationIssue{}
	objectKey := strings.TrimSpace(action.ObjectKey)
	if objectKey != "" {
		if _, exists := s.objectMap(ctx)[objectKey]; !exists {
			issues = append(issues, workflowReferenceIssue("backend.workflow.object_not_found", nodeID, objectKey))
		}
	}
	return issues
}

func (s *WorkflowReferenceValidator) validateWorkflowDependencyMap(ctx context.Context, nodeID string, values map[string]any, inheritedObjectKey, inheritedConnectorKey string) []workflowmodel.WorkflowValidationIssue {
	issues := []workflowmodel.WorkflowValidationIssue{}
	objectKey := workflowValueOrDefault(strings.TrimSpace(fmt.Sprint(values["object_key"])), inheritedObjectKey)
	if objectKey == "<nil>" {
		objectKey = inheritedObjectKey
	}
	connectorKey := workflowValueOrDefault(strings.TrimSpace(fmt.Sprint(values["connector_key"])), inheritedConnectorKey)
	if connectorKey == "<nil>" {
		connectorKey = inheritedConnectorKey
	}
	_, declaresConnector := values["connector_key"]
	_, declaresOperation := values["operation"]
	if objectKey != "" {
		if _, exists := s.objectMap(ctx)[objectKey]; !exists {
			issues = append(issues, workflowReferenceIssue("backend.workflow.object_not_found", nodeID, objectKey))
		}
	}
	for _, fieldKey := range []string{strings.TrimSpace(fmt.Sprint(values["field_key"])), strings.TrimSpace(fmt.Sprint(values["field"]))} {
		if fieldKey != "" && fieldKey != "<nil>" && !strings.HasPrefix(fieldKey, "$") && objectKey != "" && !s.objectHasWorkflowField(ctx, objectKey, fieldKey) {
			issues = append(issues, workflowReferenceIssue("backend.workflow.field_not_found", nodeID, objectKey+"."+fieldKey))
		}
	}
	if dictionaryKey := strings.TrimSpace(fmt.Sprint(values["dictionary_key"])); dictionaryKey != "" && dictionaryKey != "<nil>" && !s.workflowDictionaryExists(ctx, dictionaryKey) {
		issues = append(issues, workflowReferenceIssue("backend.workflow.dictionary_not_found", nodeID, dictionaryKey))
	}
	if connectorKey != "" && (declaresConnector || declaresOperation) && !s.workflowConnectorExists(ctx, connectorKey, strings.TrimSpace(fmt.Sprint(values["operation"]))) {
		issues = append(issues, workflowReferenceIssue("backend.workflow.connector_operation_not_found", nodeID, connectorKey+"."+strings.TrimSpace(fmt.Sprint(values["operation"]))))
	}
	if patch, ok := values["patch"].(map[string]any); ok && objectKey != "" {
		for fieldKey := range patch {
			if !s.objectHasWorkflowField(ctx, objectKey, fieldKey) {
				issues = append(issues, workflowReferenceIssue("backend.workflow.field_not_found", nodeID, objectKey+"."+fieldKey))
			}
		}
	}
	for _, value := range values {
		switch typed := value.(type) {
		case map[string]any:
			issues = append(issues, s.validateWorkflowDependencyMap(ctx, nodeID, typed, objectKey, connectorKey)...)
		case []any:
			for _, item := range typed {
				if nested, ok := item.(map[string]any); ok {
					issues = append(issues, s.validateWorkflowDependencyMap(ctx, nodeID, nested, objectKey, connectorKey)...)
				}
			}
		}
	}
	return issues
}

func (s *WorkflowReferenceValidator) objectHasWorkflowField(ctx context.Context, objectKey, fieldKey string) bool {
	object, exists := s.objectMap(ctx)[objectKey]
	if !exists {
		return false
	}
	if fieldKey == "id" || fieldKey == "created_at" || fieldKey == "updated_at" {
		return true
	}
	for _, field := range object.Fields {
		if field.Key == fieldKey && field.DisabledAt == "" {
			return true
		}
	}
	return false
}

func (s *WorkflowReferenceValidator) workflowDictionaryExists(ctx context.Context, dictionaryKey string) bool {
	for _, dictionary := range s.schema.WorkflowSchemaSnapshot(ctx, principalmodel.Principal{}).Dictionaries {
		if dictionary.Key == dictionaryKey {
			return true
		}
	}
	return false
}

func (s *WorkflowReferenceValidator) workflowConnectorExists(ctx context.Context, connectorKey, operationKey string) bool {
	integrations := s.schema.WorkflowSchemaSnapshot(ctx, principalmodel.Principal{}).Integrations
	for _, connector := range integrations.Connectors {
		if connector.Key != connectorKey {
			continue
		}
		if operationKey == "" || operationKey == "<nil>" {
			return true
		}
		for _, operation := range connector.Operations {
			if operation.Key == operationKey {
				return true
			}
		}
	}
	adapterExists := s.schema.ConnectorAdapterExists(ctx, connectorKey)
	if adapterExists {
		if operationKey == "" || operationKey == "<nil>" {
			return true
		}
		switch connectorKey {
		case "email":
			return operationKey == "send_email" || operationKey == "test_connection"
		case "webhook", "http_webhook", "outbound_webhook":
			return true
		default:
			return true
		}
	}
	return false
}

func (s *WorkflowReferenceValidator) workflowIdentityReferenceCatalog(ctx context.Context) (map[string]bool, map[string]bool) {
	users, roles := map[string]bool{}, map[string]bool{}
	if s.identity == nil {
		return users, roles
	}
	if values, err := s.identity.ListUsers(ctx, identitysdk.DirectoryQuery{}); err == nil {
		for _, user := range values {
			users[user.ID] = true
		}
	}
	if values, err := s.identity.ListRoles(ctx, identitysdk.DirectoryQuery{}); err == nil {
		for _, role := range values {
			roles[role.Key] = true
		}
	}
	return users, roles
}
