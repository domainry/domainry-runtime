package openapi

func addLifecycleOpenAPIPaths(paths map[string]any) {
	for path, operation := range map[string]string{
		"/operations/lifecycle/policies":     "LifecyclePolicies",
		"/operations/lifecycle/legal-holds":  "LifecycleLegalHold",
		"/operations/lifecycle/cleanup/jobs": "LifecycleCleanupJob",
		"/operations/lifecycle/subjects":     "LifecycleSubjectRequest",
	} {
		methods := map[string]any{"post": openAPIOperation("create"+operation, "Lifecycle", "Create governed "+operation+" without returning sensitive payload content", openAPIAdminSecurity(), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse(operation, openAPIObject(nil)))}
		if path == "/operations/lifecycle/policies" {
			methods["get"] = openAPIOperation("listLifecyclePolicies", "Lifecycle", "List versioned retention policy metadata", openAPIAdminSecurity(), openAPIJSONResponse("Lifecycle policies", openAPIObject(nil)))
		}
		paths[path] = methods
	}
	paths["/operations/lifecycle/cleanup/preview"] = map[string]any{"get": openAPIOperation("previewLifecycleCleanup", "Lifecycle", "Dry-run lifecycle cleanup impact", openAPIAdminSecurity(), openAPIJSONResponse("Cleanup impact", openAPIObject(nil)))}
	paths["/operations/lifecycle/legal-holds/{holdID}/end"] = map[string]any{"post": openAPIOperation("endLifecycleLegalHold", "Lifecycle", "End a legal hold with authority and evidence", openAPIAdminSecurity(), openAPIPathParameter("holdID", "Legal hold ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Legal hold", openAPIObject(nil)))}
	paths["/operations/lifecycle/cleanup/jobs/{jobID}/run"] = map[string]any{"post": openAPIOperation("runLifecycleCleanupJob", "Lifecycle", "Run one fenced lifecycle cleanup batch", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Cleanup job ID"), openAPIJSONResponse("Cleanup job", openAPIObject(nil)))}
	paths["/operations/lifecycle/metrics"] = map[string]any{"get": openAPIOperation("getLifecycleMetrics", "Lifecycle", "Get sanitized lifecycle backlog and legal hold metrics", openAPIAdminSecurity(), openAPIJSONResponse("Lifecycle metrics", openAPIObject(nil)))}
	paths["/operations/lifecycle/archive"] = map[string]any{"get": openAPIOperation("listLifecycleArchive", "Lifecycle", "List verified archive metadata without archived sensitive payloads", openAPIAdminSecurity(), openAPIJSONResponse("Lifecycle archive metadata", openAPIObject(nil)))}
	for _, action := range []string{"verify", "preview", "approve", "execute"} {
		path := "/operations/lifecycle/subjects/{requestID}/" + action
		paths[path] = map[string]any{"post": openAPIOperation(action+"LifecycleSubjectRequest", "Lifecycle", action+" a governed subject request", openAPIAdminSecurity(), openAPIPathParameter("requestID", "Subject request ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Subject request", openAPIObject(nil)))}
	}
	paths["/operations/lifecycle/subjects/{requestID}/download"] = map[string]any{"get": openAPIOperation("downloadLifecycleSubjectExport", "Lifecycle", "Download an authorized unexpired subject export", openAPIAdminSecurity(), openAPIPathParameter("requestID", "Subject request ID"), openAPIJSONResponse("Subject export", openAPIObject(nil)))}
	paths["/operations/lifecycle/external-erasures"] = map[string]any{"get": openAPIOperation("listLifecycleExternalErasures", "Lifecycle", "List provider erasure reconciliation status", openAPIAdminSecurity(), openAPIJSONResponse("External erasures", openAPIObject(nil)))}
	paths["/operations/lifecycle/external-erasures/{erasureID}/reconcile"] = map[string]any{"post": openAPIOperation("reconcileLifecycleExternalErasure", "Lifecycle", "Record provider erasure reconciliation evidence", openAPIAdminSecurity(), openAPIPathParameter("erasureID", "External erasure ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("External erasure", openAPIObject(nil)))}
	paths["/operations/lifecycle/deletions/replay"] = map[string]any{"post": openAPIOperation("replayLifecycleDeletions", "Lifecycle", "Reapply registered subject deletions before exposing a restored workspace", openAPIAdminSecurity(), openAPIJSONResponse("Deletion replay result", openAPIObject(nil)))}
}
