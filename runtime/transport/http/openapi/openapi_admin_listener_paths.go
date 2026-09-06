package openapi

import "github.com/domainry/domainry-runtime/pkg/runtimeext"

// addAdminListenerOpenAPIPaths closes fixed-route OpenAPI omissions for
// endpoints exposed on the Admin listener. It does not change routing,
// listener reuse classification, or authorization.
func addAdminListenerOpenAPIPaths(paths map[string]any, participant runtimeext.WorkspaceBootstrapParticipant) {
	addWorkspaceProvisioningOpenAPIPaths(paths, participant)
	paths["/metrics"] = map[string]any{
		"get": openAPIOperation("getMetrics", "Operations", "Prometheus Runtime metrics", openAPIAdminSecurity(), openAPIResponse("Prometheus metrics", "text/plain", map[string]any{"type": "string"})),
	}
}
