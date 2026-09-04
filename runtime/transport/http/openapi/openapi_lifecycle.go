package openapi

// Runtime owns only the durable Operations orchestration endpoint. All other
// Lifecycle OpenAPI operations arrive from the Lifecycle module Adapter.
func addLifecycleOpenAPIPaths(paths map[string]any) {
	paths["/operations/lifecycle/cleanup/jobs/{jobID}/run"] = map[string]any{
		"post": openAPIOperation(
			"runLifecycleCleanupJob", "Lifecycle", "Run one fenced lifecycle cleanup batch through a durable Runtime operation",
			openAPIAdminSecurity(), openAPIPathParameter("jobID", "Cleanup job ID"), openAPIJSONResponse("Cleanup job", openAPIObject(nil)),
		),
	}
}
