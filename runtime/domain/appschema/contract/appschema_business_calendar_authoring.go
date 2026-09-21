package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func ApplicationSchemaBusinessCalendarAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	payload := metadataBusinessCalendarPayloadSchema()
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "schema.business_calendar", Status: "supported", Lifecycle: "source_controlled_json",
		SystemDraftResourceType: "business_calendar",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "name", Type: "string", Required: true}, {Key: "revision", Type: "string", Required: true},
			{Key: "timezone", Type: "iana_timezone", Required: true},
			{Key: "weekly_working_intervals", Type: "array", Required: true, ItemSchema: "business_calendar_weekly_schedule"},
			{Key: "holidays", Type: "array", ItemSchema: "local_date"}, {Key: "date_exceptions", Type: "array", ItemSchema: "business_calendar_date_exception"},
		},
		Permissions:         []string{"runtime.appschema.validate_application_definition"},
		ValidationEndpoint:  "POST /application-schema/definitions/business_calendar/{resourceKey}/validate",
		ConfigurationRoutes: metadataConfigurationRoutes("business_calendar"), ResourceKeyPathParameter: "resourceKey",
		InputSchema: metadataAuthoringRequestSchema(payload, false), OutputSchema: metadataAuthoringOutputSchema(payload),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{
			{Name: "calendar_key", JSONPointer: "/definition/payload/key", Type: "business_calendar_key", VisibleTo: "subsequent_capability_calls"},
			{Name: "calendar_revision", JSONPointer: "/definition/payload/revision", Type: "string", VisibleTo: "subsequent_capability_calls"},
		},
		Execution: metadataAuthoringExecution("schema.business_calendar"),
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.business_calendar.definition_invalid", FieldPath: "payload", MessageKey: "backend.business_calendar.definition_invalid"},
			{Code: "backend.business_calendar.key_mismatch", FieldPath: "payload.key", MessageKey: "backend.business_calendar.key_mismatch"},
			{Code: "backend.business_calendar.identity_required", FieldPath: "payload", MessageKey: "backend.business_calendar.identity_required"},
			{Code: "backend.business_calendar.timezone_invalid", FieldPath: "payload.timezone", MessageKey: "backend.business_calendar.timezone_invalid"},
			{Code: "backend.business_calendar.weekly_schedule_invalid", FieldPath: "payload.weekly_working_intervals", MessageKey: "backend.business_calendar.weekly_schedule_invalid"},
			{Code: "backend.business_calendar.date_invalid", FieldPath: "payload.holidays", MessageKey: "backend.business_calendar.date_invalid"},
			{Code: "backend.business_calendar.date_duplicate", FieldPath: "payload.date_exceptions", MessageKey: "backend.business_calendar.date_duplicate"},
			{Code: "backend.business_calendar.interval_invalid", FieldPath: "payload.weekly_working_intervals", MessageKey: "backend.business_calendar.interval_invalid"},
		},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"payload": map[string]any{"name": "Weekday office hours", "revision": "2026.1", "timezone": "Asia/Shanghai", "weekly_working_intervals": []any{map[string]any{"weekday": "monday", "intervals": []any{map[string]any{"start": "09:00", "end": "18:00"}}}}}}},
			{Name: "representative", Value: map[string]any{"payload": map[string]any{"name": "China operations", "revision": "2026.2", "timezone": "Asia/Shanghai", "weekly_working_intervals": []any{map[string]any{"weekday": "monday", "intervals": []any{map[string]any{"start": "09:00", "end": "12:00"}, map[string]any{"start": "13:00", "end": "18:00"}}}}, "holidays": []any{"2026-10-01"}, "date_exceptions": []any{map[string]any{"date": "2026-10-10", "intervals": []any{map[string]any{"start": "09:00", "end": "17:00"}}}}}}},
			{Name: "invalid_with_repair", Value: map[string]any{"payload": map[string]any{"name": "Invalid", "revision": "1", "timezone": "Mars/Olympus", "weekly_working_intervals": []any{}}}, ExpectedErrorCodes: []string{"backend.business_calendar.timezone_invalid"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "contract", Path: "runtime/domain/appschema/contract/appschema_business_calendar_authoring.go", Symbol: "ApplicationSchemaBusinessCalendarAuthoringCapability"},
			{Kind: "domain_model", Path: "runtime/domain/businesscalendar/model/business_calendar.go", Symbol: "BusinessCalendarSchema"},
			{Kind: "validation", Path: "runtime/domain/businesscalendar/policy/business_calendar_policy.go", Symbol: "Validate"},
			{Kind: "service", Path: "runtime/application/appschema/appschema_definition_validation_application_service.go", Symbol: "ApplicationSchemaApplicationService.ValidateApplicationDefinitionRequestPayload"},
		},
	}
}

func metadataBusinessCalendarPayloadSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	interval := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"start", "end"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"start": {Type: "string", Pattern: `^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`}, "end": {Type: "string", Pattern: `^(?:(?:[01][0-9]|2[0-3]):[0-5][0-9]|24:00)$`},
	}}
	intervals := capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &interval, MaxItems: metadataIntPointer(8)}
	weekly := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"weekday", "intervals"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"weekday":   {Type: "string", Enum: []any{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}},
		"intervals": intervals,
	}}
	exception := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"date", "intervals"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"date": {Type: "string", Format: "date"}, "intervals": intervals,
	}}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"name", "revision", "timezone", "weekly_working_intervals"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key":  metadataNonEmptyStringSchema("Optional echo of the authoritative resourceKey path value; when present it must match."),
		"name": metadataNonEmptyStringSchema("Human-readable calendar name."), "revision": metadataNonEmptyStringSchema("Immutable calendar revision."),
		"timezone":                 metadataNonEmptyStringSchema("IANA timezone governing all local dates and intervals."),
		"weekly_working_intervals": {Type: "array", Items: &weekly, MinItems: metadataIntPointer(1), MaxItems: metadataIntPointer(7)},
		"holidays":                 {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", Format: "date"}, MaxItems: metadataIntPointer(366)},
		"date_exceptions":          {Type: "array", Items: &exception, MaxItems: metadataIntPointer(366)},
	}}
}
