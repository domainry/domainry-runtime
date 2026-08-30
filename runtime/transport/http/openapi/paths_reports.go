package openapi

import surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"

func addReportQueryOpenAPIPaths(paths map[string]any) {
	paths["/reports/{reportKey}/summary"] = map[string]any{
		"get": openAPIOperation("getReportSummary", "Reports", "Get one bounded, stably ordered server page of Report rows", openAPIAdminSecurity(), openAPIPathParameter("reportKey", "Report key"), openAPIQueryParameter("mode", "Execution mode; scoped predicates require realtime.", map[string]any{"type": "string", "enum": []string{"realtime", "snapshot"}}), openAPIQueryParameter("query_key", "Stable key declared by dataset.query_predicates.", map[string]any{"type": "string"}), openAPIQueryParameter("tags", "Repeatable stable key declared by dataset.tag_predicates; all selected predicates are combined with AND.", map[string]any{"type": "array", "items": map[string]any{"type": "string"}}), openAPIQueryParameter("page_size", "Requested server page size; maximum 200.", map[string]any{"type": "integer", "minimum": 1, "maximum": 200}), openAPIQueryParameter("cursor", "Opaque cursor bound to Report, parameters, authorization scope and result version.", map[string]any{"type": "string"}), openAPIJSONResponse("Report summary page", reportSummaryPageSchema())),
	}
	paths["/reports/{reportKey}/query"] = map[string]any{
		"post": openAPIOperation("queryReportObjectSQL", "Reports", "Execute a published restricted object_sql_v1 report with typed Runtime parameters", openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace, surfacemodel.ProductSurfaceConsumerPortal), openAPIAdminSecurity(), openAPIPathParameter("reportKey", "Report key"), openAPIJSONRequest(reportObjectSQLQueryRequestSchema()), openAPIJSONResponse("Object SQL report result", reportObjectSQLSummarySchema())),
	}
}

func reportObjectSQLQueryRequestSchema() map[string]any {
	schema := openAPIRequiredObject([]string{"parameters"}, map[string]any{
		"parameters": reportObjectSQLParametersSchema(),
		"page_size":  map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
		"cursor":     map[string]any{"type": "string"},
	})
	schema["additionalProperties"] = false
	return schema
}

func reportObjectSQLParametersSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": map[string]any{"type": []string{"string", "number", "integer", "boolean", "null"}}}
}

func reportObjectSQLSummarySchema() map[string]any {
	return reportSummaryPageSchema()
}

func reportSummaryPageSchema() map[string]any {
	stringMap := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}}
	row := openAPIObject(map[string]any{"dimensions": stringMap, "measures": stringMap})
	column := openAPIRequiredObject([]string{"key", "type", "kind"}, map[string]any{
		"key": map[string]any{"type": "string"}, "type": map[string]any{"type": "string"}, "kind": map[string]any{"type": "string", "enum": []string{"dimension", "measure"}},
		"precision": map[string]any{"type": "integer"}, "scale": map[string]any{"type": "integer"},
	})
	column["additionalProperties"] = false
	return openAPIRequiredObject([]string{"key", "rows", "row_count", "source_row_count", "execution_mode", "page_size", "truncated", "total", "total_semantics"}, map[string]any{
		"key": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "rows": openAPIArray(row),
		"row_count": map[string]any{"type": "integer"}, "source_row_count": map[string]any{"type": "integer"},
		"execution_mode": map[string]any{"type": "string"}, "result_schema": openAPIArray(column),
		"page_size": map[string]any{"type": "integer", "minimum": 1, "maximum": 200}, "next_cursor": map[string]any{"type": "string"},
		"truncated": map[string]any{"type": "boolean"}, "total": map[string]any{"type": "integer", "minimum": 0}, "total_semantics": map[string]any{"type": "string", "enum": []string{"exact", "at_least"}},
	})
}

func reportExportJobSchema() map[string]any {
	return openAPIRequiredObject([]string{"id", "data_exchange_job_id", "audit_id", "report_key", "object_key", "status", "pages_completed", "rows_exported", "total", "scope", "created_at", "updated_at"}, map[string]any{
		"id": map[string]any{"type": "string"}, "data_exchange_job_id": map[string]any{"type": "string"}, "audit_id": map[string]any{"type": "string"},
		"report_key": map[string]any{"type": "string"}, "object_key": map[string]any{"type": "string"},
		"status":          map[string]any{"type": "string", "enum": []string{"accepted", "running", "completed", "failed", "cancelled"}},
		"pages_completed": map[string]any{"type": "integer"}, "rows_exported": map[string]any{"type": "integer"}, "total": map[string]any{"type": "integer"},
		"scope": openAPIReportExportScopeSchema(), "artifact_id": map[string]any{"type": "string"}, "content_sha256": map[string]any{"type": "string"}, "download_token": map[string]any{"type": "string"}, "expires_at": map[string]any{"type": "string", "format": "date-time"},
		"error_code": map[string]any{"type": "string"}, "created_at": map[string]any{"type": "string", "format": "date-time"}, "updated_at": map[string]any{"type": "string", "format": "date-time"},
	})
}

func openAPIReportExportScopeSchema() map[string]any {
	filter := openAPIRequiredObject([]string{"dimension_key", "operator"}, map[string]any{
		"dimension_key": map[string]any{"type": "string"},
		"operator":      map[string]any{"type": "string", "enum": []string{"eq", "ne", "gt", "gte", "lt", "lte", "in", "not_in", "between", "is_null", "not_null"}},
		"values":        openAPIArray(map[string]any{"type": "string"}),
	})
	dateRange := openAPIRequiredObject([]string{"dimension_key", "from", "to"}, map[string]any{"dimension_key": map[string]any{"type": "string"}, "from": map[string]any{"type": "string"}, "to": map[string]any{"type": "string"}})
	metric := openAPIRequiredObject([]string{"key", "version"}, map[string]any{"key": map[string]any{"type": "string"}, "version": map[string]any{"type": "string"}})
	freshness := openAPIRequiredObject([]string{"mode"}, map[string]any{"mode": map[string]any{"type": "string", "enum": []string{"realtime", "snapshot"}}, "snapshot_id": map[string]any{"type": "string"}, "maximum_lag_seconds": map[string]any{"type": "integer", "format": "int64", "minimum": 0, "maximum": 86400}})
	return openAPIRequiredObject([]string{"purpose", "freshness"}, map[string]any{
		"parameters": reportObjectSQLParametersSchema(),
		"query_key":  map[string]any{"type": "string", "description": "Stable dataset.query_predicates key also allowlisted by the selected Report Export Control."}, "analysis_key": map[string]any{"type": "string"}, "filters": openAPIArray(filter), "date_range": dateRange,
		"timezone": map[string]any{"type": "string"}, "tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Stable dataset.tag_predicates keys also allowlisted by the selected Report Export Control; combined with AND."}, "field_projection": openAPIArray(map[string]any{"type": "string"}),
		"purpose": map[string]any{"type": "string"}, "metric_definitions": openAPIArray(metric), "freshness": freshness, "role_key": map[string]any{"type": "string"},
		"data_scopes": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
	})
}

func openAPIReportExportDownloadSchema() map[string]any {
	return openAPIRequiredObject([]string{"id", "report_key", "object_key", "filename", "token", "expires_at", "content_sha256", "row_count", "scope", "watermarked"}, map[string]any{
		"id": map[string]any{"type": "string"}, "report_key": map[string]any{"type": "string"}, "object_key": map[string]any{"type": "string"}, "filename": map[string]any{"type": "string"},
		"token": map[string]any{"type": "string"}, "expires_at": map[string]any{"type": "string", "format": "date-time"}, "content_sha256": map[string]any{"type": "string"},
		"row_count": map[string]any{"type": "integer"}, "scope": openAPIReportExportScopeSchema(), "watermarked": map[string]any{"type": "boolean"},
	})
}

func openAPIBusinessAuditExportFilterSchema() map[string]any {
	properties := map[string]any{}
	for _, key := range []string{"event", "object_key", "record_id", "actor_id", "role_key", "result"} {
		properties[key] = map[string]any{"type": "string", "maxLength": 128}
	}
	properties["created_from"] = map[string]any{"type": "string", "format": "date-time"}
	properties["created_to"] = map[string]any{"type": "string", "format": "date-time"}
	schema := openAPIObject(properties)
	schema["additionalProperties"] = false
	return schema
}

func openAPIBusinessAuditExportRequestSchema() map[string]any {
	schema := openAPIRequiredObject([]string{"filters"}, map[string]any{
		"filters": openAPIBusinessAuditExportFilterSchema(),
		"format":  map[string]any{"type": "string", "enum": []string{"csv"}},
	})
	schema["additionalProperties"] = false
	return schema
}

func openAPIBusinessAuditExportPreparedSchema() map[string]any {
	return openAPIRequiredObject([]string{"id", "report_source", "filename", "content_sha256", "row_count", "audit_identity", "scope_sha256", "filters", "download_token", "expires_at"}, map[string]any{
		"id": map[string]any{"type": "string"}, "report_source": map[string]any{"type": "string", "enum": []string{"business_audit_events"}}, "filename": map[string]any{"type": "string"},
		"content_sha256": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"}, "row_count": map[string]any{"type": "integer", "minimum": 1},
		"audit_identity": map[string]any{"type": "string"}, "scope_sha256": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
		"filters": openAPIBusinessAuditExportFilterSchema(), "download_token": map[string]any{"type": "string", "writeOnly": true}, "expires_at": map[string]any{"type": "string", "format": "date-time"},
	})
}
