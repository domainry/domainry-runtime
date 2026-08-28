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

func workflowHasNonEmptyUniqueValue(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = true
		}
	}
	return len(seen) > 0
}
