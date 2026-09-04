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
		[]string{"id", "workspace_id", "kind", "object_key", "status", "checkpoint", "total", "actor_id", "role_key", "created_at", "updated_at"},
		map[string]any{
			"id": map[string]any{"type": "string"}, "workspace_id": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string"},
			"object_key": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "checkpoint": map[string]any{"type": "integer"},
			"total": map[string]any{"type": "integer"}, "actor_id": map[string]any{"type": "string"},
			"role_key": map[string]any{"type": "string"}, "created_at": map[string]any{"type": "string"}, "updated_at": map[string]any{"type": "string"},
			"result_filename": map[string]any{"type": "string"}, "result_content_type": map[string]any{"type": "string"},
			"result_artifact_id": map[string]any{"type": "string"}, "error_code": map[string]any{"type": "string"},
		},
	)
}

func recordImportPreviewOpenAPISchema() map[string]any {
	return openAPIRequiredObject([]string{"object_key", "rows", "valid_rows", "invalid_rows", "duplicate_rows", "can_apply"}, map[string]any{
		"object_key": map[string]any{"type": "string"}, "rows": openAPIArray(openAPIObject(nil)), "error_rows": openAPIArray(openAPIObject(nil)),
		"valid_rows": map[string]any{"type": "integer"}, "invalid_rows": map[string]any{"type": "integer"}, "duplicate_rows": map[string]any{"type": "integer"}, "can_apply": map[string]any{"type": "boolean"},
	})
}

func recordExportDispatchOpenAPIResponses(operation map[string]any) map[string]any {
	deliveryHeader := map[string]any{"description": "UI delivery signal; background means show a preparation notice and wait for Notification Inbox SSE", "schema": map[string]any{"type": "string", "enum": []string{"direct", "background"}}}
	directResponse := openAPIResponseValue("Direct CSV export", "text/csv", map[string]any{"type": "string", "format": "binary"})
	directResponse["headers"] = map[string]any{"X-Export-Delivery": deliveryHeader}
	backgroundResponse := openAPIResponseValue("Background export accepted", "application/json", recordBatchJobOpenAPISchema())
	backgroundResponse["headers"] = map[string]any{"X-Export-Delivery": deliveryHeader, "Location": map[string]any{"description": "Opaque job status location for UI infrastructure", "schema": map[string]any{"type": "string"}}}
	operation["responses"] = map[string]any{
		"200":     directResponse,
		"202":     backgroundResponse,
		"default": openAPIJSONResponse("Error", openAPIRef("Error")).Value,
	}
	return operation
}

func addRecordBatchOpenAPIPaths(paths map[string]any) {
	idempotencyKey := openAPIParameter{Value: openAPIHeaderParameter("Idempotency-Key", "Stable caller key; replay returns the existing result and a different fingerprint conflicts", true)}
	objectKey := openAPIPathParameter("objectKey", "Object key")
	exportDispatch := recordExportDispatchOpenAPIResponses(openAPIRuntimeClient(openAPIOperation(
		"dispatchObjectRecordExport", "Objects", "Export records; Runtime automatically returns a direct file or creates a background download", openAPIAdminSecurity(), objectKey, idempotencyKey,
	), "exportRecords"))
	paths["/records/{objectKey}/export"] = map[string]any{"post": exportDispatch}
	paths["/records/exports/jobs/{jobID}"] = map[string]any{"get": openAPIRuntimeClient(openAPIOperation(
		"downloadObjectRecordExport", "Objects", "Download a completed record export owned by the current user", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Export job ID"),
		openAPIResponse("Export artifact", "text/csv", map[string]any{"type": "string", "format": "binary"}),
	), "downloadRecordExport")}
	paths["/records/{objectKey}/import/preview"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"previewObjectRecordImport", "Objects", "Preview and validate object record CSV without mutation", openAPIAdminSecurity(), objectKey, recordCSVRequestBody(),
		openAPIJSONResponse("Import preview", recordImportPreviewOpenAPISchema()),
	), "previewRecordImport")}
	paths["/records/{objectKey}/import/apply"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"applyObjectRecordImport", "Objects", "Idempotently apply a validated bounded CSV import", openAPIAdminSecurity(), objectKey, idempotencyKey, recordCSVRequestBody(),
		openAPIJSONResponse("Import result", openAPIRequiredObject([]string{"object_key", "created", "skipped", "preview"}, map[string]any{"object_key": map[string]any{"type": "string"}, "created": map[string]any{"type": "integer"}, "skipped": map[string]any{"type": "integer"}, "preview": recordImportPreviewOpenAPISchema()})),
	), "applyRecordImport")}
	paths["/records/{objectKey}/import/jobs"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"enqueueObjectRecordImport", "Objects", "Submit a durable bounded asynchronous CSV import to Data Exchange", openAPIAdminSecurity(), objectKey, idempotencyKey, recordCSVRequestBody(),
		openAPIJSONResponse("Queued Data Exchange job", recordBatchJobOpenAPISchema()),
	), "enqueueRecordImport")}
	paths["/records/{objectKey}/actions/{actionKey}/bulk"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"executeBulkObjectAction", "Actions", "Idempotently execute one action for an explicit bounded record set", openAPIAdminSecurity(), objectKey, openAPIPathParameter("actionKey", "Action key"), idempotencyKey,
		openAPIJSONRequest(openAPIRef("BulkActionRequest")), openAPIJSONResponse("Bulk action result", openAPIObject(nil)),
	), "runBulkAction")}
}
