package contract

import "strings"

// ReceiptReadActionKey covers the original invocation input and neutral receipt,
// independently of permission to execute the published Action.
func ReceiptReadActionKey(actionKey string) string {
	if actionKey = strings.TrimSpace(actionKey); actionKey == "" {
		return ""
	}
	return "action.receipt." + actionKey + ".read"
}
