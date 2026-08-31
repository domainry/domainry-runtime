package openapi

// addAdminCapabilityDisclosureOpenAPIPaths closes fixed-route OpenAPI omissions
// for capabilities exposed on the Admin listener. It does not change routing,
// Business reuse classification, or authorization.
func addAdminCapabilityDisclosureOpenAPIPaths(paths map[string]any) {
	addWorkspaceProvisioningOpenAPIPaths(paths)
	paths["/metrics"] = map[string]any{
		"get": openAPIOperation("getMetrics", "Operations", "Prometheus Runtime metrics", openAPIAdminSecurity(), openAPIResponse("Prometheus metrics", "text/plain", map[string]any{"type": "string"})),
	}
}
