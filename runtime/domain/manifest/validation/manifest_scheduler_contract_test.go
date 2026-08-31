package validation

import (
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestRejectsRuntimeOwnedSchedulerRecordsAndUnsupportedExportTarget(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects:     []definitionmodel.ObjectSchema{{Key: "customer"}, {Key: "job_run"}},
		SeedRecords: nil,
		SchedulerDefinitions: []map[string]any{{
			"key": "nightly", "target_type": "report_export", "target_key": "orders", "schedule_type": "interval", "interval_seconds": 60,
		}},
	}
	state := newValidationState(manifest, nil)
	state.validateSchedulerOwnershipAndDefinitions()
	diagnostics := state.errs.Error()
	if !strings.Contains(diagnostics, `owner-managed Scheduler object "job_run"`) || !strings.Contains(diagnostics, "backend.scheduler.target_type_unsupported") {
		t.Fatalf("Scheduler ownership diagnostics=%v", state.errs)
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
			{"key": "workflow", "target_type": "workflow", "target_key": "scheduled:daily", "schedule_type": "interval", "interval_seconds": 60},
			{"key": "snapshot", "target_type": "report_snapshot_refresh", "target_key": "orders", "schedule_type": "daily_at", "time_of_day": "03:00", "timezone": "UTC"},
			{"key": "http", "target_type": "http", "target_key": "sync", "connection_key": "erp", "operation": "sync", "schedule_type": "cron", "schedule_expression": "0 3 * * *", "timezone": "UTC"},
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
