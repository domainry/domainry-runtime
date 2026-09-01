package openapi

import "strings"

// annotateStaticModuleOwnerFallbacks makes the ownership of static client
// discovery paths explicit when Build is called without mounted module
// surfaces. Once a module is mounted, its route and OpenAPI contracts remain
// authoritative and annotateModuleOwnedOpenAPIPaths adds the stronger live
// ownership evidence.
func annotateStaticModuleOwnerFallbacks(paths map[string]any) {
	for path, rawPathItem := range paths {
		owner := staticModuleOwner(path)
		if owner == "" {
			continue
		}
		pathItem, _ := rawPathItem.(map[string]any)
		for _, rawOperation := range pathItem {
			operation, _ := rawOperation.(map[string]any)
			if operation == nil || operation["x-domainry-endpoint-contract"] != nil {
				continue
			}
			operation["x-domainry-module-owner-fallback"] = owner
		}
	}
}

func staticModuleOwner(path string) string {
	switch {
	case path == "/party" || strings.HasPrefix(path, "/party/") || strings.HasPrefix(path, "/foundation/"):
		return "party"
	case path == "/notifications" || strings.HasPrefix(path, "/notifications/") ||
		path == "/business/notifications" || strings.HasPrefix(path, "/business/notifications/") ||
		path == "/portal/notifications" || strings.HasPrefix(path, "/portal/notifications/") ||
		path == "/business/notification-preferences" || path == "/portal/notification-preferences":
		return "notification"
	default:
		return ""
	}
}
