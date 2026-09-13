package contract

import "strings"

// ReceiptReadActionKey authorizes the original start input and acknowledgement.
// It grants neither workflow execution nor access to execution variables.
func ReceiptReadActionKey(workflowKey string) string {
	if workflowKey = strings.TrimSpace(workflowKey); workflowKey == "" {
		return ""
	}
	return "workflow.receipt." + workflowKey + ".read"
}
