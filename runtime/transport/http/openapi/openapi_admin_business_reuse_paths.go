package openapi

// addAdminCapabilityDisclosureOpenAPIPaths closes fixed-route OpenAPI omissions
// for capabilities exposed on the Admin listener. It does not change routing,
// Business reuse classification, or authorization.
func addAdminCapabilityDisclosureOpenAPIPaths(paths map[string]any) {
	addBuilderPath(paths, "/agent-dialog/diagnostics", "Agent Dialog Diagnostics", "get")
	paths["/metrics"] = map[string]any{
		"get": openAPIOperation("getMetrics", "Operations", "Prometheus Runtime metrics", openAPIAdminSecurity(), openAPIResponse("Prometheus metrics", "text/plain", map[string]any{"type": "string"})),
	}
	addBuilderPath(paths, "/business-seeds/{seedKey}", "Business Seeds", "get", "put")
	addBuilderPath(paths, "/business-seeds/{seedKey}/validate", "Business Seeds", "post")
	addBuilderPath(paths, "/business-seeds/{seedKey}/versions", "Business Seeds", "get")
}
