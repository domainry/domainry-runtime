package contract

import "strings"

const RunActionBindingKind = "runtime_workflow_action"

// RunActionKey is the canonical source-owned Action identity for invoking one
// published Workflow. The application manifest owner is assigned when the
// Workflow is projected into the frozen Runtime Action registry.
func RunActionKey(workflowKey string) string {
	workflowKey = strings.TrimSpace(workflowKey)
	if workflowKey == "" {
		return ""
	}
	return "workflow." + workflowKey + ".run"
}
