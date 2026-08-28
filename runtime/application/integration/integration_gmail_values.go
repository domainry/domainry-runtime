package integration

import (
	"strconv"
	"strings"
)

// These helpers belong to Runtime's Gmail delivery/event projection, not to
// mailbox synchronization state or scheduling.
func gmailString(values map[string]any, key string) string {
	switch value := values[key].(type) {
	case string:
		return strings.TrimSpace(value)
	case float64:
		return strconv.FormatInt(int64(value), 10)
	default:
		return ""
	}
}

func gmailConfigBool(config map[string]any, key string) bool {
	value, _ := config[key].(bool)
	return value
}
