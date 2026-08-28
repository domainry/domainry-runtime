package policy

import "strings"

func ChangePlanCanonicalResourceType(resourceType string) string {
	switch strings.TrimSpace(resourceType) {
	case "automation_rule":
		return "automation"
	default:
		return strings.TrimSpace(resourceType)
	}
}
