package validation

import (
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestRejectsRuntimeOwnedSchedulerRecordsAndUnsupportedExportTarget(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects:     []definitionmodel.ObjectSchema{{Key: "customer"}, {Key: "job_run"}},
		SeedRecords: nil,
		SchedulerDefinitions: []map[string]any{{
			"key": "nightly", "name": "Nightly", "status": "enabled", "target_type": "report_export", "target_key": "orders", "schedule_type": "interval", "interval_seconds": 60, "max_attempts": 1, "timeout_seconds": 300,
		}},
	}
	state := newValidationState(manifest, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	diagnostics := state.errs.Error()
	if !strings.Contains(diagnostics, `owner-managed Scheduler object "job_run"`) || !strings.Contains(diagnostics, "backend.scheduler.target_type_unsupported") {
		t.Fatalf("Scheduler ownership diagnostics=%v", state.errs)
	}
}

func TestManifestSchedulerBusinessCalendarReferenceIsClosedAndTimezoneBound(t *testing.T) {
	calendar := businesscalendarmodel.BusinessCalendarSchema{
		Key: "cn_operations", Name: "CN operations", Revision: "2026.09", Timezone: "Asia/Shanghai",
		WeeklyWorkingIntervals: []businesscalendarmodel.BusinessCalendarWeeklySchedule{{Weekday: "monday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "18:00"}}}},
	}
	definition := map[string]any{
		"key": "daily", "name": "Daily", "status": "enabled", "target_type": "workflow", "target_key": "scheduled:*",
		"schedule_type": "daily_at", "time_of_day": "09:00", "timezone": "Asia/Shanghai", "business_calendar_key": "cn_operations", "non_working_day_policy": "roll_forward",
		"max_attempts": 1, "timeout_seconds": 300,
	}
	manifest := manifestmodel.ManifestSchema{BusinessCalendars: []businesscalendarmodel.BusinessCalendarSchema{calendar}, SchedulerDefinitions: []map[string]any{definition}}
	state := newValidationState(manifest, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	if len(state.errs) != 0 {
		t.Fatalf("valid calendar reference rejected: %v", state.errs)
	}

	missing := manifest
	missing.SchedulerDefinitions = []map[string]any{cloneManifestSchedulerDefinition(definition)}
	missing.SchedulerDefinitions[0]["business_calendar_key"] = "missing"
	state = newValidationState(missing, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	if diagnostic := state.errs.Error(); !strings.Contains(diagnostic, "backend.scheduler.business_calendar_not_found") {
		t.Fatalf("missing calendar diagnostic=%v", state.errs)
	}

	mismatch := manifest
	mismatch.SchedulerDefinitions = []map[string]any{cloneManifestSchedulerDefinition(definition)}
	mismatch.SchedulerDefinitions[0]["timezone"] = "UTC"
	state = newValidationState(mismatch, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	if diagnostic := state.errs.Error(); !strings.Contains(diagnostic, "backend.scheduler.business_calendar_timezone_mismatch") {
		t.Fatalf("timezone mismatch diagnostic=%v", state.errs)
	}
}

func TestManifestSchedulerBusinessActionRequiresMatchingObjectAndServiceRoleGrant(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "order"}},
		Actions: []definitionmodel.ActionSchema{{Key: "order.expire", ObjectKey: "order", Kind: definitionmodel.ActionKindObjectOperation}},
		Roles: []manifestmodel.RoleSchema{{
			Key: "order_automation", Name: "Order automation", Audience: "service", AssignmentMode: "system_managed",
			Permissions: []manifestmodel.RolePermission{{PermissionKey: "order.expire", DataScope: identitysdk.DataScopeAll}},
		}},
		SchedulerDefinitions: []map[string]any{{
			"key": "expire-orders", "name": "Expire orders", "status": "enabled", "target_type": "business_action", "target_key": "order.expire", "target_object": "order", "run_as_role": "order_automation",
			"payload_json": `{}`, "schedule_type": "interval", "interval_seconds": 60, "max_attempts": 3, "timeout_seconds": 30,
		}},
	}
	state := newValidationState(manifest, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	if len(state.errs) != 0 {
		t.Fatalf("valid Scheduler Business Action rejected: %v", state.errs)
	}

	for name, mutate := range map[string]func(*manifestmodel.ManifestSchema){
		"unknown action": func(candidate *manifestmodel.ManifestSchema) {
			candidate.SchedulerDefinitions[0]["target_key"] = "order.missing"
		},
		"wrong object": func(candidate *manifestmodel.ManifestSchema) {
			candidate.SchedulerDefinitions[0]["target_object"] = "customer"
		},
		"human role": func(candidate *manifestmodel.ManifestSchema) {
			candidate.Roles[0].Audience, candidate.Roles[0].AssignmentMode = "user", "manual"
		},
		"missing grant": func(candidate *manifestmodel.ManifestSchema) { candidate.Roles[0].Permissions = nil },
		"record action": func(candidate *manifestmodel.ManifestSchema) {
			candidate.Actions[0].Kind = definitionmodel.ActionKindRecordOperation
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := manifest
			candidate.Actions = append([]definitionmodel.ActionSchema(nil), manifest.Actions...)
			candidate.Roles = append([]manifestmodel.RoleSchema(nil), manifest.Roles...)
			candidate.Roles[0].Permissions = append([]manifestmodel.RolePermission(nil), manifest.Roles[0].Permissions...)
			candidate.SchedulerDefinitions = []map[string]any{cloneManifestSchedulerDefinition(manifest.SchedulerDefinitions[0])}
			mutate(&candidate)
			state := newValidationState(candidate, nil)
			state.validateSchedulerOwnershipAndDefinitions()
			if len(state.errs) == 0 {
				t.Fatal("invalid Scheduler Business Action was accepted")
			}
		})
	}
}

func cloneManifestSchedulerDefinition(source map[string]any) map[string]any {
	result := make(map[string]any, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func TestManifestSchedulerDefinitionRejectsRetiredAndNonApplicableFields(t *testing.T) {
	base := map[string]any{
		"key": "workflow", "name": "Workflow", "status": "enabled",
		"target_type": "workflow", "target_key": "scheduled:daily",
		"schedule_type": "interval", "interval_seconds": 60,
		"max_attempts": 1, "timeout_seconds": 300,
	}
	for _, field := range []string{"trigger_type", "interval_minutes", "interval_hours", "operation", "dispatch_mode", "condition_json", "retry_backoff", "idempotency_keys"} {
		t.Run(field, func(t *testing.T) {
			definition := cloneManifestSchedulerDefinition(base)
			definition[field] = "value"
			state := newValidationState(manifestmodel.ManifestSchema{SchedulerDefinitions: []map[string]any{definition}}, nil)
			state.validateSchedulerOwnershipAndDefinitions()
			if diagnostic := state.errs.Error(); !strings.Contains(diagnostic, "backend.scheduler.field_unsupported") {
				t.Fatalf("field %q was not rejected by the Scheduler contract: %v", field, state.errs)
			}
		})
	}

	definition := cloneManifestSchedulerDefinition(base)
	definition["payload_json"] = `{}`
	state := newValidationState(manifestmodel.ManifestSchema{SchedulerDefinitions: []map[string]any{definition}}, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	if diagnostic := state.errs.Error(); !strings.Contains(diagnostic, "backend.scheduler.field_not_applicable") {
		t.Fatalf("workflow payload was not rejected: %v", state.errs)
	}
}

func TestManifestSchedulerReferencesUseDownstreamOwnerContracts(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "customer"}},
		Workflows: []definitionmodel.WorkflowSchema{{
			Key: "daily", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "scheduled"},
		}},
		Reports:      []reportmodel.ReportSchema{{Key: "orders"}},
		Integrations: connectormodel.IntegrationSchema{Connections: []connectormodel.ConnectionSchema{{Key: "erp"}}},
		SchedulerDefinitions: []map[string]any{
			{"key": "workflow", "name": "Workflow", "status": "enabled", "target_type": "workflow", "target_key": "scheduled:daily", "schedule_type": "interval", "interval_seconds": 60, "max_attempts": 1, "timeout_seconds": 300},
			{"key": "snapshot", "name": "Snapshot", "status": "enabled", "target_type": "report_snapshot_refresh", "target_key": "orders", "schedule_type": "daily_at", "time_of_day": "03:00", "timezone": "UTC", "max_attempts": 1, "timeout_seconds": 300},
			{"key": "http", "name": "HTTP", "status": "enabled", "target_type": "http", "target_key": "sync", "connection_key": "erp", "schedule_type": "cron", "schedule_expression": "0 3 * * *", "timezone": "UTC", "max_attempts": 1, "timeout_seconds": 300},
		},
	}
	state := newValidationState(manifest, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	if len(state.errs) != 0 {
		t.Fatalf("valid Scheduler owner references rejected: %v", state.errs)
	}

	manifest.SchedulerDefinitions[0]["target_key"] = "scheduled:missing"
	manifest.SchedulerDefinitions[1]["target_key"] = "missing"
	manifest.SchedulerDefinitions[2]["connection_key"] = "missing"
	state = newValidationState(manifest, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	diagnostics := state.errs.Error()
	for _, expected := range []string{`unknown workflow "missing"`, `unknown report "missing"`, `unknown Integration connection "missing"`} {
		if !strings.Contains(diagnostics, expected) {
			t.Fatalf("missing %q in %v", expected, state.errs)
		}
	}
}
