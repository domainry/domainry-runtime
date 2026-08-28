package openapi

func openAPIRuntimeClient(operation map[string]any, method string) map[string]any {
	operation["x-domainry-runtime-client-method"] = method
	return operation
}

func recordCSVRequestBody() openAPIRequestBody {
	return openAPIRequestBody{Value: map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{"schema": openAPIRequiredObject([]string{"csv"}, map[string]any{"csv": map[string]any{"type": "string", "minLength": 1}})},
			"text/csv":         map[string]any{"schema": map[string]any{"type": "string", "minLength": 1}},
		},
	}}
}

func recordBatchJobOpenAPISchema() map[string]any {
	return openAPIRequiredObject(
		[]string{"id", "workspace_id", "kind", "object_key", "status", "checkpoint", "total", "attempt_count", "actor_id", "role_key", "created_at", "updated_at"},
		map[string]any{
			"id": map[string]any{"type": "string"}, "workspace_id": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"},
			"object_key": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "checkpoint": map[string]any{"type": "integer"},
			"total": map[string]any{"type": "integer"}, "attempt_count": map[string]any{"type": "integer"}, "actor_id": map[string]any{"type": "string"},
			"role_key": map[string]any{"type": "string"}, "created_at": map[string]any{"type": "string"}, "updated_at": map[string]any{"type": "string"},
			"result_filename": map[string]any{"type": "string"}, "result_content_type": map[string]any{"type": "string"}, "result_chunks": map[string]any{"type": "integer"},
			"audit_id": map[string]any{"type": "string"}, "result_artifact_id": map[string]any{"type": "string"}, "error_code": map[string]any{"type": "string"},
		},
	)
}

func recordImportPreviewOpenAPISchema() map[string]any {
	return openAPIRequiredObject([]string{"object_key", "rows", "valid_rows", "invalid_rows", "duplicate_rows", "can_apply"}, map[string]any{
		"object_key": map[string]any{"type": "string"}, "rows": openAPIArray(openAPIObject(nil)), "error_rows": openAPIArray(openAPIObject(nil)),
		"valid_rows": map[string]any{"type": "integer"}, "invalid_rows": map[string]any{"type": "integer"}, "duplicate_rows": map[string]any{"type": "integer"}, "can_apply": map[string]any{"type": "boolean"},
	})
}

func addRecordBatchOpenAPIPaths(paths map[string]any) {
	idempotencyKey := openAPIParameter{Value: openAPIHeaderParameter("Idempotency-Key", "Stable caller key; replay returns the existing result and a different fingerprint conflicts", true)}
	objectKey := openAPIPathParameter("objectKey", "Object key")
	paths["/objects/{objectKey}/records/export"] = map[string]any{"get": openAPIRuntimeClient(openAPIOperation(
		"exportObjectRecords", "Objects", "Export object records as an authorized bounded CSV", openAPIAdminSecurity(), objectKey,
		openAPIResponse("CSV export", "text/csv", map[string]any{"type": "string", "format": "binary"}),
	), "exportRecords")}
	paths["/objects/{objectKey}/records/export/jobs"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"enqueueObjectRecordExport", "Objects", "Queue a durable bounded asynchronous CSV export", openAPIAdminSecurity(), objectKey, idempotencyKey,
		openAPIJSONResponse("Queued batch job", recordBatchJobOpenAPISchema()),
	), "enqueueRecordExport")}
	paths["/objects/{objectKey}/records/import/preview"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"previewObjectRecordImport", "Objects", "Preview and validate object record CSV without mutation", openAPIAdminSecurity(), objectKey, recordCSVRequestBody(),
		openAPIJSONResponse("Import preview", recordImportPreviewOpenAPISchema()),
	), "previewRecordImport")}
	paths["/objects/{objectKey}/records/import/apply"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"applyObjectRecordImport", "Objects", "Idempotently apply a validated bounded CSV import", openAPIAdminSecurity(), objectKey, idempotencyKey, recordCSVRequestBody(),
		openAPIJSONResponse("Import result", openAPIRequiredObject([]string{"object_key", "created", "skipped", "preview"}, map[string]any{"object_key": map[string]any{"type": "string"}, "created": map[string]any{"type": "integer"}, "skipped": map[string]any{"type": "integer"}, "preview": recordImportPreviewOpenAPISchema()})),
	), "applyRecordImport")}
	paths["/objects/{objectKey}/records/import/jobs"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"enqueueObjectRecordImport", "Objects", "Queue a durable bounded asynchronous CSV import", openAPIAdminSecurity(), objectKey, idempotencyKey, recordCSVRequestBody(),
		openAPIJSONResponse("Queued batch job", recordBatchJobOpenAPISchema()),
	), "enqueueRecordImport")}
	paths["/record-batch-jobs/{jobID}"] = map[string]any{"get": openAPIRuntimeClient(openAPIOperation(
		"getRecordBatchJob", "Objects", "Get durable asynchronous import or export status", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Batch job ID"), openAPIJSONResponse("Batch job", recordBatchJobOpenAPISchema()),
	), "getRecordBatchJob")}
	paths["/record-batch-jobs/{jobID}/cancel"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"cancelRecordBatchJob", "Objects", "Idempotently cancel a queued or running batch job", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Batch job ID"), idempotencyKey, openAPIJSONResponse("Cancelled batch job", recordBatchJobOpenAPISchema()),
	), "cancelRecordBatchJob")}
	paths["/record-batch-jobs/{jobID}/download"] = map[string]any{"get": openAPIRuntimeClient(openAPIOperation(
		"downloadRecordBatchJob", "Objects", "Download a completed authorized asynchronous export", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Batch job ID"), openAPIResponse("CSV export", "text/csv", map[string]any{"type": "string", "format": "binary"}),
	), "downloadRecordBatchJob")}
	paths["/objects/{objectKey}/actions/{actionKey}/bulk"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"executeBulkObjectAction", "Actions", "Idempotently execute one action for an explicit bounded record set", openAPIAdminSecurity(), objectKey, openAPIPathParameter("actionKey", "Action key"), idempotencyKey,
		openAPIJSONRequest(openAPIRef("BulkActionRequest")), openAPIJSONResponse("Bulk action result", openAPIObject(nil)),
	), "runBulkAction")}
}
