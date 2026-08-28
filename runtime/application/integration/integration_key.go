package integration

import "strings"

func sanitizeKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "integration"
	}
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z', char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char + ('a' - 'A'))
		default:
			builder.WriteByte('_')
		}
	}
	if result := strings.Trim(builder.String(), "_"); result != "" {
		return result
	}
	return "integration"
}

func integrationExternalRoleKeyAllowed(roleKey string) bool {
	roleKey = strings.ToLower(strings.TrimSpace(roleKey))
	return roleKey != "" && roleKey != "admin" && roleKey != "owner" && !strings.Contains(roleKey, "workspace_admin")
}
