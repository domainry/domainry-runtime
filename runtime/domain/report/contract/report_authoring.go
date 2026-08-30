package contract

import (
	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
)

// ReportAuthoringDomain publishes the Report-owned shape while Metadata owns
// version persistence and composes Runtime object and permission checks.
func ReportAuthoringDomain() capabilitycontract.CapabilityAuthoringDomain {
	payload := reportDefinitionPayloadSchema()
	return capabilitycontract.CapabilityAuthoringDomain{Key: "report", Capabilities: []capabilitycontract.CapabilityAuthoringDefinition{{
		Key: "report.definition", Status: "supported", Lifecycle: "versioned_metadata",
		SystemDraftResourceType: "report",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "key", Type: "report_key", Required: true}, {Key: "name", Type: "string"}, {Key: "i18n", Type: "object"},
			{Key: "dataset", Type: "object", ConflictsWith: []string{"object_sql_v1"}},
			{Key: "object_sql_v1", Type: "object", ConflictsWith: []string{"dataset"}},
			{Key: "required_permissions", Type: "array", ItemSchema: "permission_key"},
			{Key: "evidence_requirements", Type: "array", ItemSchema: "report_evidence_requirement"},
			{Key: "materialization", Type: "object"},
			{Key: "export_scope", Type: "object"},
			{Key: "expected_schema_hash", Type: "schema_hash", Required: true},
		},
		Permissions: []string{"workspace.admin"}, AuditEvents: []string{"metadata_definition_upserted", "metadata_definition_deleted"},
		ValidationEndpoint: "POST /metadata/definitions/report/{resourceKey}/validate", ConfigurationRoutes: reportConfigurationRoutes(), ResourceKeyPathParameter: "resourceKey",
		ResourceOperations: reportResourceOperations(),
		FrontendSupportKey: "report.definition-editor.v1", InputSchema: reportAuthoringRequestSchema(payload), OutputSchema: reportAuthoringOutputSchema(payload),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "report_key", JSONPointer: "/definition/resource_key", Type: "report_key", VisibleTo: "subsequent_capability_calls"}, {Name: "schema_hash", JSONPointer: "/definition/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{
			{Kind: "object_key", InputJSONPointer: "/payload/dataset/source/object_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/object_key"},
			{Kind: "object_key", InputJSONPointer: "/payload/dataset/joins/*/object_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/object_key"},
			{Kind: "object_key", InputJSONPointer: "/payload/object_sql_v1/source_objects/*", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/object_key"},
			{Kind: "object_key", InputJSONPointer: "/payload/export_scope/tags/join/object_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/object_key"},
			{Kind: "object_key", InputJSONPointer: "/payload/export_scope/tags/family_join/object_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/object_key"},
			{Kind: "permission_key", InputJSONPointer: "/payload/required_permissions/*", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/permission_key"},
		},
		Execution: appschemacontract.VersionedApplicationDefinitionExecution("report.definition"),
		Errors:    reportAuthoringErrors(), Examples: reportAuthoringExamples(),
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "model", Path: "runtime/domain/report/model/report_schema.go", Symbol: "ReportSchema"},
			{Kind: "validation", Path: "runtime/domain/appschema/validation/appschema_report_validation.go", Symbol: "ApplicationSchemaValidateReportDefinitionContract"},
			{Kind: "service", Path: "runtime/application/appschema/appschema_definition_orchestration_application_service.go", Symbol: "ApplicationSchemaApplicationService.UpsertApplicationDefinition"},
		},
	}}}
}

func reportDefinitionPayloadSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	stringItem := capabilitycontract.CapabilityAuthoringSchema{Type: "string", MinLength: reportIntPointer(1)}
	field := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"source_alias", "field_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"source_alias": {Type: "string", MinLength: reportIntPointer(1)}, "field_key": {Type: "string", MinLength: reportIntPointer(1)},
	}}
	source := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"object_key", "alias"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"object_key": {Type: "string", MinLength: reportIntPointer(1)}, "alias": {Type: "string", MinLength: reportIntPointer(1)}, "source_type": {Type: "string", Enum: []any{"records", "snapshot"}, Default: "records"},
	}}
	joinEquality := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"left_field", "right_field"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"left_field": {Type: "string", MinLength: reportIntPointer(1)}, "right_field": {Type: "string", MinLength: reportIntPointer(1)},
	}}
	join := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"alias", "object_key", "type", "left_alias", "cardinality"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"alias": {Type: "string", MinLength: reportIntPointer(1)}, "object_key": {Type: "string", MinLength: reportIntPointer(1)}, "type": {Type: "string", Enum: []any{"inner", "left"}},
		"left_alias": {Type: "string", MinLength: reportIntPointer(1)}, "left_field": {Type: "string", MinLength: reportIntPointer(1)}, "right_field": {Type: "string", MinLength: reportIntPointer(1)},
		"field_equalities": {Type: "array", MinItems: reportIntPointer(1), MaxItems: reportIntPointer(8), Items: &joinEquality},
		"cardinality":      {Type: "string", Enum: []any{"one_to_one", "many_to_one", "one_to_many"}},
		"source_type":      {Type: "string", Enum: []any{"records", "snapshot"}, Default: "records"},
	}}
	filter := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"field", "operator"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"field": field, "operator": {Type: "string", Enum: []any{"eq", "ne", "gt", "gte", "lt", "lte", "in", "not_in", "contains", "starts_with", "ends_with", "is_null", "not_null", "between"}},
		"value": {}, "values": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{}},
	}}
	predicate := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "filters"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "filters": {Type: "array", MinItems: reportIntPointer(1), MaxItems: reportIntPointer(16), Items: &filter},
	}}
	dimension := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "field"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "field": field, "time_grain": {Type: "string", Enum: []any{"minute", "hour", "day", "week", "month", "quarter", "year"}},
	}}
	measure := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "operation"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "operation": {Type: "string", Enum: []any{"count", "distinct_count", "sum", "avg", "min", "max", "ratio", "duration", "percentile"}},
		"source_alias": {Type: "string"}, "field": field, "start_field": field, "end_field": field, "numerator_key": {Type: "string"}, "denominator_key": {Type: "string"}, "percentile": {Type: "string"},
	}}
	sortRule := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "direction"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "direction": {Type: "string", Enum: []any{"asc", "desc"}},
	}}
	privacy := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"minimum_group_size", "entity_field"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"minimum_group_size": {Type: "integer", Minimum: reportFloatPointer(2), Maximum: reportFloatPointer(1000)}, "entity_field": field,
	}}
	comparison := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "type", "time_dimension_key", "measure_key", "operation"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "type": {Type: "string", Enum: []any{"period_over_period"}}, "time_dimension_key": {Type: "string", MinLength: reportIntPointer(1)}, "measure_key": {Type: "string", MinLength: reportIntPointer(1)}, "operation": {Type: "string", Enum: []any{"difference", "ratio", "percent_change"}}, "offset_periods": {Type: "integer", Minimum: reportFloatPointer(1), Maximum: reportFloatPointer(100), Default: 1},
	}}
	funnelStage := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "values"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "values": {Type: "array", MinItems: reportIntPointer(1), Items: &capabilitycontract.CapabilityAuthoringSchema{}},
	}}
	analysis := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "type", "entity_field", "time_field"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "type": {Type: "string", Enum: []any{"funnel", "cohort_retention"}}, "entity_field": field, "event_field": field, "time_field": field,
		"stages": {Type: "array", MinItems: reportIntPointer(2), Items: &funnelStage}, "window_seconds": {Type: "integer", Minimum: reportFloatPointer(1), Maximum: reportFloatPointer(31536000)},
		"cohort_grain": {Type: "string", Enum: []any{"day", "week", "month", "quarter", "year"}}, "period_grain": {Type: "string", Enum: []any{"day", "week", "month", "quarter", "year"}}, "maximum_periods": {Type: "integer", Minimum: reportFloatPointer(1), Maximum: reportFloatPointer(120)},
	}}
	dataset := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"source"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"source": source, "joins": {Type: "array", Items: &join}, "filters": {Type: "array", Items: &filter},
		"query_predicates": {Type: "array", Items: &predicate}, "tag_predicates": {Type: "array", Items: &predicate},
		"dimensions": {Type: "array", Items: &dimension}, "measures": {Type: "array", Items: &measure},
		"time_grain": {Type: "string", Enum: []any{"minute", "hour", "day", "week", "month", "quarter", "year"}}, "time_zone": {Type: "string", Default: "UTC"},
		"sort": {Type: "array", Items: &sortRule}, "limit": {Type: "integer", Minimum: reportFloatPointer(1), Maximum: reportFloatPointer(10000), Default: 1000}, "privacy": privacy, "comparisons": {Type: "array", Items: &comparison}, "analyses": {Type: "array", Items: &analysis},
	}}
	objectSQLParameter := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "type"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "type": {Type: "string", Enum: []any{"boolean", "date", "datetime", "decimal", "integer", "number", "text"}}, "required": {Type: "boolean"}, "default": {},
	}}
	objectSQLResultColumn := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "type", "kind"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "type": {Type: "string", MinLength: reportIntPointer(1)}, "kind": {Type: "string", Enum: []any{"dimension", "measure"}},
		"precision": {Type: "integer", Minimum: reportFloatPointer(1)}, "scale": {Type: "integer", Minimum: reportFloatPointer(0)},
	}}
	objectSQLCardinality := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"alias", "cardinality"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"alias": {Type: "string", MinLength: reportIntPointer(1)}, "cardinality": {Type: "string", Enum: []any{"one_to_one", "many_to_one", "one_to_many"}},
	}}
	objectSQL := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"sql", "source_objects", "result_schema"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"sql": {Type: "string", MinLength: reportIntPointer(1)}, "source_objects": {Type: "array", MinItems: reportIntPointer(1), Items: &stringItem},
		"parameters": {Type: "array", Items: &objectSQLParameter}, "result_schema": {Type: "array", MinItems: reportIntPointer(1), Items: &objectSQLResultColumn},
		"join_cardinalities": {Type: "array", Items: &objectSQLCardinality}, "timeout_milliseconds": {Type: "integer", Minimum: reportFloatPointer(1)},
	}}
	evidence := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"object_key", "minimum_records"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"object_key": {Type: "string", MinLength: reportIntPointer(1)}, "minimum_records": {Type: "integer", Minimum: reportFloatPointer(1)}, "required_non_empty_fields": {Type: "array", Items: &stringItem},
	}}
	materialization := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"maximum_lag_seconds"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"maximum_lag_seconds": {Type: "integer", Minimum: reportFloatPointer(1), Maximum: reportFloatPointer(31536000)},
		"consistency_retries": {Type: "integer", Minimum: reportFloatPointer(1), Maximum: reportFloatPointer(10), Default: 3},
	}}
	queryPredicate := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"field", "operator"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"field": field, "operator": {Type: "string", Enum: []any{"contains", "starts_with", "eq"}},
	}}
	queryScope := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"mode", "predicates"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"mode": {Type: "string", Enum: []any{"any", "all"}}, "predicates": {Type: "array", MinItems: reportIntPointer(1), MaxItems: reportIntPointer(16), Items: &queryPredicate},
	}}
	tagScope := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"join", "target_field", "tag_field"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"join": join, "family_join": join, "target_field": field, "tag_field": field, "fixed_filters": {Type: "array", Items: &filter},
		"allowed_match_modes": {Type: "array", MinItems: reportIntPointer(1), MaxItems: reportIntPointer(2), Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", Enum: []any{"any", "all"}}},
		"default_match_mode":  {Type: "string", Enum: []any{"any", "all"}}, "match": {Type: "string", Enum: []any{"any", "all"}},
	}}
	exportScope := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"query": queryScope, "tags": tagScope,
	}}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key"}, OneOf: []capabilitycontract.CapabilityAuthoringSchema{{Required: []string{"dataset"}}, {Required: []string{"object_sql_v1"}}}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string", MinLength: reportIntPointer(1)}, "name": {Type: "string"}, "i18n": {Type: "object", AdditionalProperties: &open},
		"dataset": dataset, "object_sql_v1": objectSQL,
		"required_permissions":  {Type: "array", Items: &stringItem},
		"evidence_requirements": {Type: "array", Items: &evidence},
		"materialization":       materialization,
		"export_scope":          exportScope,
	}}
}

func reportAuthoringRequestSchema(payload capabilitycontract.CapabilityAuthoringSchema) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"expected_schema_hash", "payload"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"object_key": {Type: "string"}, "name": {Type: "string"}, "source_kind": {Type: "string"}, "source_id": {Type: "string"}, "expected_schema_hash": {Type: "string", MinLength: reportIntPointer(1)}, "payload": payload,
	}}
}

func reportAuthoringOutputSchema(payload capabilitycontract.CapabilityAuthoringSchema) *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	definition := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"payload", "resource_key", "resource_type", "schema_hash"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"resource_type": {Type: "string", Const: "report"}, "resource_key": {Type: "string"}, "object_key": {Type: "string"}, "name": {Type: "string"}, "payload": payload,
		"schema_version": {Type: "string"}, "schema_hash": {Type: "string"}, "source_kind": {Type: "string"}, "source_id": {Type: "string"}, "disabled_at": {Type: "string"}, "created_at": {Type: "string", Format: "date-time"}, "updated_at": {Type: "string", Format: "date-time"},
	}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"definition", "resource_hash", "schema", "snapshot_hash"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"definition": definition, "resource_hash": {Type: "string"}, "snapshot_hash": {Type: "string"}, "schema": {Type: "object", AdditionalProperties: &open},
	}}
}

func reportConfigurationRoutes() []string {
	return appschemacontract.VersionedApplicationDefinitionRoutes("report")
}

func reportResourceOperations() *capabilitycontract.CapabilityAuthoringResourceOperations {
	return nil
}

func reportAuthoringErrors() []capabilitycontract.CapabilityAuthoringError {
	codes := []struct{ code, path string }{
		{"backend.report.definition_invalid", "payload"}, {"backend.report.key_required", "payload.key"},
		{"backend.report.execution_definition_missing", "payload"}, {"backend.report.execution_definition_conflict", "payload"},
		{"backend.report.object_sql_invalid", "payload.object_sql_v1"}, {"backend.report.object_sql_p0_feature_forbidden", "payload.object_sql_v1"},
		{"backend.report.dataset_source_invalid", "payload.dataset.source"},
		{"backend.report.dataset_alias_invalid", "payload.dataset"}, {"backend.report.source_object_not_found", "payload.dataset"}, {"backend.report.join_invalid", "payload.dataset.joins[]"}, {"backend.report.join_cardinality_invalid", "payload.dataset.joins[].cardinality"},
		{"backend.report.permission_invalid", "payload.required_permissions[]"}, {"backend.report.permission_not_found", "payload.required_permissions[]"},
		{"backend.report.field_reference_invalid", "payload.dataset"}, {"backend.report.field_not_found", "payload.dataset"}, {"backend.report.field_permission_denied", "payload.dataset"}, {"backend.report.filter_invalid", "payload.dataset.filters[]"}, {"backend.report.predicate_invalid", "payload.dataset.query_predicates[]"}, {"backend.report.predicate_invalid", "payload.dataset.tag_predicates[]"}, {"backend.report.dimension_invalid", "payload.dataset.dimensions[]"}, {"backend.report.measure_invalid", "payload.dataset.measures[]"}, {"backend.report.join_measure_amplification", "payload.dataset.measures[]"}, {"backend.report.privacy_invalid", "payload.dataset.privacy"}, {"backend.report.comparison_invalid", "payload.dataset.comparisons[]"}, {"backend.report.analysis_invalid", "payload.dataset.analyses[]"}, {"backend.report.time_grain_invalid", "payload.dataset.time_grain"}, {"backend.report.sort_invalid", "payload.dataset.sort[]"}, {"backend.report.limit_invalid", "payload.dataset.limit"},
		{"backend.report.materialization_invalid", "payload.materialization"}, {"backend.report.required_index_missing", "payload.dataset"},
		{"backend.report.export_query_definition_invalid", "payload.export_scope.query"}, {"backend.report.export_tag_definition_invalid", "payload.export_scope.tags"},
		{"backend.report.evidence_object_invalid", "payload.evidence_requirements[].object_key"}, {"backend.report.evidence_source_not_declared", "payload.evidence_requirements[].object_key"}, {"backend.report.evidence_minimum_invalid", "payload.evidence_requirements[].minimum_records"}, {"backend.report.evidence_field_invalid", "payload.evidence_requirements[].required_non_empty_fields[]"}, {"backend.report.evidence_field_not_found", "payload.evidence_requirements[].required_non_empty_fields[]"}, {"backend.report.evidence_unavailable", "payload.evidence_requirements[]"}, {"backend.report.evidence_insufficient", "payload.evidence_requirements[]"},
	}
	errors := make([]capabilitycontract.CapabilityAuthoringError, 0, len(codes))
	for _, item := range codes {
		errors = append(errors, capabilitycontract.CapabilityAuthoringError{Code: item.code, FieldPath: item.path, MessageKey: item.code})
	}
	return errors
}

func reportAuthoringExamples() []capabilitycontract.CapabilityAuthoringExample {
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "customer.summary", "dataset": map[string]any{"source": map[string]any{"object_key": "customer", "alias": "customer"}, "measures": []any{map[string]any{"key": "customers", "operation": "count"}}}}}},
		{Name: "representative", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "name": "Customer names", "source_kind": "builder_v4", "source_id": "$builder_task_id", "payload": map[string]any{"key": "customer.names", "name": "Customer names", "object_sql_v1": map[string]any{"sql": "SELECT c.name AS name FROM customer c ORDER BY c.name ASC LIMIT 10", "source_objects": []any{"customer"}, "result_schema": []any{map[string]any{"key": "name", "type": "text", "kind": "dimension"}}}, "required_permissions": []any{"customer.read"}}}},
		{Name: "invalid_with_repair", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "customer.invalid", "dataset": map[string]any{"source": map[string]any{"object_key": "customer", "alias": "customer"}, "measures": []any{map[string]any{"key": "total", "operation": "sum", "field": map[string]any{"source_alias": "customer", "field_key": "missing"}}}}}}, ExpectedErrorCodes: []string{"backend.report.field_not_found"}},
	}
}

func reportIntPointer(value int) *int           { return &value }
func reportFloatPointer(value float64) *float64 { return &value }
