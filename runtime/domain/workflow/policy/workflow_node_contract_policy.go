package policy

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func WorkflowApprovalNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowApprovalNodeContract {
	if node.Contract != nil && node.Contract.Approval != nil {
		return *node.Contract.Approval
	}
	return definitionmodel.WorkflowApprovalNodeContract{}
}

func WorkflowBusinessActionNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowBusinessActionNodeContract {
	if node.Contract != nil && node.Contract.Action != nil {
		return *node.Contract.Action
	}
	return definitionmodel.WorkflowBusinessActionNodeContract{}
}

func WorkflowCCNodeContract(node definitionmodel.WorkflowGraphNode) definitionmodel.WorkflowCCNodeContract {
	if node.Contract != nil && node.Contract.CC != nil {
		return *node.Contract.CC
	}
	return definitionmodel.WorkflowCCNodeContract{}
}

func WorkflowAssigneeResolverIsValid(resolver definitionmodel.WorkflowAssigneeResolver) bool {
	switch strings.TrimSpace(resolver.Type) {
	case "users":
		return workflowHasNonEmptyUniqueValue(resolver.UserIDs)
	case "variable_user", "record_user_field":
		return strings.TrimSpace(resolver.Field) != ""
	case "manager", "manager_of":
		return strings.TrimSpace(resolver.UserField) != ""
	case "initiator_manager":
		return true
	case "role":
		return strings.TrimSpace(resolver.RoleKey) != ""
	case "project":
		return strings.TrimSpace(resolver.ResolverKey) != ""
	case "relation_user":
		return workflowAssigneeRelationPathValid(resolver.RelationPath)
	case "relation_role":
		return workflowAssigneeRelationPathValid(resolver.RelationPath) && strings.TrimSpace(resolver.RoleField) != ""
	case "manager_chain":
		return workflowManagerChainResolverValid(resolver)
	default:
		return false
	}
}

func workflowAssigneeRelationPathValid(path []string) bool {
	if len(path) == 0 || len(path) > 5 {
		return false
	}
	for _, field := range path {
		if strings.TrimSpace(field) == "" {
			return false
		}
	}
	return true
}

func workflowManagerChainResolverValid(resolver definitionmodel.WorkflowAssigneeResolver) bool {
	if resolver.MaxDepth < 1 || resolver.MaxDepth > 20 {
		return false
	}
	switch strings.TrimSpace(resolver.Source) {
	case "initiator":
		return true
	case "variable", "record":
		return strings.TrimSpace(resolver.Field) != ""
	default:
		return false
	}
}

func workflowHasNonEmptyUniqueValue(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = true
		}
	}
	return len(seen) > 0
}
