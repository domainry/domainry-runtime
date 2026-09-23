package runtime

import "strings"

func valueOrDefault(value, fallback string) string {
	if normalized := strings.TrimSpace(value); normalized != "" {
		return normalized
	}
	return fallback
}
