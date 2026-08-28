package integrationtest

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerprojection "github.com/domainry/domainry-runtime/runtime/domain/scheduler/projection"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"

	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"

	apperror "github.com/domainry/domainry-runtime/runtime/platform/apperror"

	workflowbusiness "github.com/domainry/domainry-runtime/runtime/application/workflow"

	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"

	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSchedulerWorkerStartsWithoutManifestWorkflows(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler object storage: %v", err)
	}
	definition := schedulerRuntimeTestDefinition(time.Now().UTC())
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatalf("insert scheduler definition: %v", err)
	}
	publishSchedulerDefinitionStoreFixture(t, store, definition.ID, definition.Data)
	records := newSchedulerRuntimeTestService(t, store, objects)
	ctx, cancel := context.WithCancel(t.Context())
	done := records.Applications().Workflows.StartWorker(ctx, 5*time.Millisecond, 25)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("scheduler worker did not stop")
		}
	})
	deadline := time.Now().Add(time.Second)
	for {
		runs, err := recordLegacyStore(store).ListRecords(t.Context(), "default", objects[1], recordmodel.RecordListQuery{Page: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("list scheduler runs: %v", err)
		}
		if runs.Total > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("scheduler worker did not claim due definition without manifest workflows")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSchedulerFinishRunMarksTimeoutFailure(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler object storage: %v", err)
	}
	now := time.Now().UTC()
	definition := schedulerRuntimeTestDefinition(now)
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatalf("insert scheduler definition: %v", err)
	}
	service := newSchedulerRuntimeTestService(t, store, objects)
	run, claimed, err := service.Applications().Scheduler.ClaimRun(t.Context(), definition, "scheduler", now, schedulerRuntimeSystemScope())
	if err != nil || !claimed {
		t.Fatalf("claim scheduler run = %v, %v, %v", run.ID, claimed, err)
	}
	run.Data["timeout_seconds"] = 1
	if err := service.Applications().Scheduler.FinishRun(t.Context(), run, nil, nil, now.Add(-2*time.Second), schedulerRuntimeSystemScope()); err != nil {
		t.Fatalf("finish timeout run: %v", err)
	}
	finished, ok, err := recordLegacyStore(store).GetRecord(t.Context(), "default", objects[1], run.ID)
	if err != nil || !ok {
		t.Fatalf("get finished run: %v, %v", ok, err)
	}
	if got := strings.TrimSpace(fmt.Sprint(finished.Data["status"])); got != "retrying" {
		t.Fatalf("expected timeout run to retry, got %q", got)
	}
	if got := strings.TrimSpace(fmt.Sprint(finished.Data["error_category"])); got != "timeout" {
		t.Fatalf("expected timeout error category, got %q", got)
	}
	if strings.TrimSpace(fmt.Sprint(finished.Data["next_retry_at"])) == "" {
		t.Fatalf("expected timeout retry to set next_retry_at, got %#v", finished.Data)
	}
}

func TestSchedulerFinishRunRollsBackStateEventAndDeadLetterOnEventFailure(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 22, 0, 0, 0, time.UTC)
	definition := schedulerRuntimeTestDefinition(now)
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatal(err)
	}
	scheduler := newSchedulerRuntimeTestService(t, store, objects).Applications().Scheduler
	run, claimed, err := scheduler.ClaimRun(t.Context(), definition, "scheduler", now, schedulerRuntimeSystemScope())
	if err != nil || !claimed {
		t.Fatalf("claim=%+v claimed=%v err=%v", run, claimed, err)
	}
	run.Data["max_attempts"] = 1
	if _, err := store.DB().Exec(`CREATE TRIGGER fail_scheduler_finish_event BEFORE INSERT ON job_run_event WHEN NEW.event_type = 'dead_lettered' BEGIN SELECT RAISE(ABORT, 'injected scheduler event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.FinishRun(t.Context(), run, nil, errors.New("terminal failure"), now, schedulerRuntimeSystemScope()); err == nil {
		t.Fatal("expected injected scheduler finish failure")
	}
	current, found, err := recordLegacyStore(store).GetRecord(t.Context(), "default", objects[1], run.ID)
	if err != nil || !found || strings.TrimSpace(fmt.Sprint(current.Data["status"])) != "leased" {
		t.Fatalf("run escaped rollback: current=%+v found=%v err=%v", current, found, err)
	}
	deadLetters, err := recordLegacyStore(store).ListRecords(t.Context(), "default", objects[3], recordmodel.RecordListQuery{Page: 1, PageSize: 10})
	if err != nil || deadLetters.Total != 0 {
		t.Fatalf("dead letter escaped rollback: total=%d err=%v", deadLetters.Total, err)
	}
	currentDefinition, found, err := recordLegacyStore(store).GetRecord(t.Context(), "default", objects[0], definition.ID)
	if err != nil || !found || currentDefinition.UpdatedAt != definition.UpdatedAt || fmt.Sprint(currentDefinition.Data["next_run_at"]) != fmt.Sprint(definition.Data["next_run_at"]) {
		t.Fatalf("definition cursor escaped rollback: current=%+v found=%v err=%v", currentDefinition, found, err)
	}
}

func TestSchedulerOperationalRecordsRejectGenericCRUDStatusMutation(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler object storage: %v", err)
	}
	now := time.Now().UTC()
	definition := schedulerRuntimeTestDefinition(now)
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatalf("insert scheduler definition: %v", err)
	}
	service := newSchedulerRuntimeTestService(t, store, objects)
	run, claimed, err := service.Applications().Scheduler.ClaimRun(t.Context(), definition, "scheduler", now, schedulerRuntimeSystemScope())
	if err != nil || !claimed {
		t.Fatalf("claim scheduler run = %v, %v, %v", run.ID, claimed, err)
	}
	_, err = service.Applications().Records.UpdateRecord(t.Context(), "job_run", run.ID, map[string]any{"status": "cancelled"}, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"job_run.update"}, RecordScope: "all_records", DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "job_run", Scope: "all_records", Read: true, Write: true}}}))
	if err == nil || apperror.CodeOf(err) != "backend.scheduler.runtime_api_required" {
		t.Fatalf("expected scheduler runtime API guard, got %v", err)
	}
}

func TestSchedulerReportExportCreatesBusinessEvidence(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := append(schedulerRuntimeTestObjects(), schedulerReportExportEvidenceObjects()...)
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler object storage: %v", err)
	}
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	definition := schedulerRuntimeTestDefinition(now)
	definition.ID = "job_definition_report_export"
	definition.Data["key"] = "weekly_export"
	definition.Data["target_type"] = "report_export"
	definition.Data["target_key"] = "sales_pipeline"
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", objects[0], definition); err != nil {
		t.Fatalf("insert report export definition: %v", err)
	}
	service := newSchedulerRuntimeTestService(t, store, objects)
	run, claimed, err := service.Applications().Scheduler.ClaimRun(t.Context(), definition, "scheduler", now, schedulerRuntimeSystemScope())
	if err != nil || !claimed {
		t.Fatalf("claim report export run = %v, %v, %v", run.ID, claimed, err)
	}
	if _, err := service.Applications().Scheduler.ProcessClaimedRun(t.Context(), definition, run, 25, workflowbusiness.WorkflowWorkerPrincipal(), now); err != nil {
		t.Fatalf("process report export scheduler run: %v", err)
	}

	for _, objectKey := range []string{"report_definition", "report_query_run", "report_export_audit", "download_task"} {
		object := objectSchemaByKey(t, objects, objectKey)
		page, err := recordLegacyStore(store).ListRecords(t.Context(), "default", object, recordmodel.RecordListQuery{Page: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("list %s evidence: %v", objectKey, err)
		}
		if page.Total != 1 {
			t.Fatalf("expected one %s evidence record, got %d", objectKey, page.Total)
		}
	}
	eventObject := objectSchemaByKey(t, objects, "job_run_event")
	events, err := recordLegacyStore(store).ListRecords(t.Context(), "default", eventObject, recordmodel.RecordListQuery{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list scheduler events: %v", err)
	}
	seen := map[string]bool{}
	for _, event := range events.Items {
		seen[strings.TrimSpace(fmt.Sprint(event.Data["event_type"]))] = true
	}
	for _, eventType := range []string{"report_query_run_created", "report_export_audit_created", "download_task_created"} {
		if !seen[eventType] {
			t.Fatalf("expected report export evidence event %q, got %#v", eventType, seen)
		}
	}
}

func TestSchedulerHealthWarnsForDeadLettersAndStaleLeases(t *testing.T) {
	store := openSchedulerRuntimeTestStore(t)
	defer store.Close()
	objects := schedulerRuntimeTestObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler object storage: %v", err)
	}
	now := time.Now().UTC()
	service := newSchedulerRuntimeTestService(t, store, objects)

	runObject := objects[1]
	deadLetterObject := objects[3]
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", runObject, recordmodel.Record{
		ID:        "jobrun_stale",
		CreatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
		UpdatedAt: now.Add(-10 * time.Minute).Format(time.RFC3339),
		Data: map[string]any{
			"scheduler_definition_key": "job_definition_1",
			"status":                   "leased",
			"lease_expires_at":         now.Add(-5 * time.Minute).Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatalf("insert stale scheduler run: %v", err)
	}
	if err := recordLegacyStore(store).InsertRecord(t.Context(), "default", deadLetterObject, recordmodel.Record{
		ID:        "jobdl_open",
		CreatedAt: now.Add(-9 * time.Minute).Format(time.RFC3339),
		UpdatedAt: now.Add(-9 * time.Minute).Format(time.RFC3339),
		Data: map[string]any{
			"job_run_id":               "jobrun_stale",
			"scheduler_definition_key": "job_definition_1",
			"status":                   "open",
			"reason":                   "test",
			"failed_at":                now.Add(-9 * time.Minute).Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatalf("insert scheduler dead letter: %v", err)
	}

	health := service.Applications().RuntimeStatus.Health(t.Context())
	if health["status"] != "degraded" {
		t.Fatalf("expected degraded scheduler health, got %#v", health)
	}
	checks := health["checks"].(map[string]string)
	if checks["scheduler"] != "warning" {
		t.Fatalf("expected scheduler warning check, got %#v", checks)
	}
	warnings := health["warnings"].(map[string]any)
	schedulerWarnings := warnings["scheduler"].(map[string]any)
	if schedulerWarnings["unresolved_dead_letters"] != 1 || schedulerWarnings["stale_leased_runs"] != 1 {
		t.Fatalf("expected scheduler warnings for dead letters and stale leases, got %#v", schedulerWarnings)
	}
}

func openSchedulerRuntimeTestStore(t *testing.T) *persistence.RuntimeStore {
	t.Helper()
	tempDir := t.TempDir()
	migrationPath := filepath.Join(tempDir, "001_empty.sql")
	if err := os.WriteFile(migrationPath, []byte("-- scheduler runtime test migration\n"), 0644); err != nil {
		t.Fatalf("write migration: %v", err)
	}
	store, err := persistence.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(tempDir, "app.db"),
		MigrationSQL:   migrationPath,
	})
	if err != nil {
		t.Fatalf("open scheduler test store: %v", err)
	}
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatalf("ensure scheduler evidence schema: %v", err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure scheduler runtime schema: %v", err)
	}
	return store
}

func newSchedulerRuntimeTestService(t *testing.T, store *persistence.RuntimeStore, objects []definitionmodel.ObjectSchema) *RuntimeServices {
	t.Helper()
	return runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "scheduler-runtime-test", TemplateVersion: "1", Name: "Scheduler Runtime Test", Objects: objects, Views: nil, Actions: nil, Workflows: nil, AutomationRules: nil, Dictionaries: nil, Integrations: integrationmodel.IntegrationSchema{}, Reports: nil, Entrypoints: nil, Skills: nil, Agents: nil, Store: store})
}

func metadataStore(store *persistence.RuntimeStore) metadatapersistence.MetadataStore {
	return metadatapersistence.NewMetadataStore(store)
}

func schedulerRuntimePrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "scheduler-worker", WorkspaceID: "default"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "scheduler.command"}, RecordScope: "all_records"})
}

func objectSchemaByKey(t *testing.T, objects []definitionmodel.ObjectSchema, key string) definitionmodel.ObjectSchema {
	t.Helper()
	for _, object := range objects {
		if object.Key == key {
			return object
		}
	}
	t.Fatalf("object %s not found", key)
	return definitionmodel.ObjectSchema{}
}

func schedulerRuntimeTestDefinition(now time.Time) recordmodel.Record {
	return recordmodel.Record{
		ID:        "job_definition_1",
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
		Data: map[string]any{
			"key":                     "daily_scan",
			"name":                    "Daily scan",
			"status":                  "enabled",
			"target_type":             "workflow",
			"target_key":              "scheduled:*",
			"schedule_expression":     "daily",
			"timezone":                "UTC",
			"max_attempts":            3,
			"retry_backoff":           "fixed",
			"retry_delay_seconds":     60,
			"retry_max_delay_seconds": 3600,
			"timeout_seconds":         300,
			"next_run_at":             now.Add(-time.Minute).Format(time.RFC3339),
		},
	}
}

func schedulerRuntimeTestObjects() []definitionmodel.ObjectSchema {
	return []definitionmodel.ObjectSchema{
		{
			Key:    "job_definition",
			Name:   "Job Definition",
			Config: map[string]any{"scheduler_runtime": true},
			Fields: []definitionmodel.FieldSchema{
				{Key: "key", Name: "Key", Type: "text"},
				{Key: "name", Name: "Name", Type: "text"},
				{Key: "status", Name: "Status", Type: "text"},
				{Key: "target_type", Name: "Target Type", Type: "text"},
				{Key: "target_key", Name: "Target Key", Type: "text"},
				{Key: "schedule_expression", Name: "Schedule Expression", Type: "text"},
				{Key: "timezone", Name: "Timezone", Type: "text"},
				{Key: "missed_window_policy", Name: "Missed Window Policy", Type: "text"},
				{Key: "max_catchup_windows", Name: "Max Catch-Up Windows", Type: "number"},
				{Key: "max_attempts", Name: "Max Attempts", Type: "number"},
				{Key: "retry_backoff", Name: "Retry Backoff", Type: "text"},
				{Key: "retry_delay_seconds", Name: "Retry Delay Seconds", Type: "number"},
				{Key: "retry_max_delay_seconds", Name: "Max Retry Delay Seconds", Type: "number"},
				{Key: "timeout_seconds", Name: "Timeout Seconds", Type: "number"},
				{Key: "next_run_at", Name: "Next Run At", Type: "datetime"},
			},
		},
		{
			Key:    "job_run",
			Name:   "Job Run",
			Config: map[string]any{"scheduler_runtime": true},
			Fields: []definitionmodel.FieldSchema{
				{Key: "scheduler_definition_key", Name: "Job Definition", Type: "text"},
				{Key: "status", Name: "Status", Type: "text"},
				{Key: "triggered_by", Name: "Triggered By", Type: "text"},
				{Key: "scheduled_for", Name: "Scheduled For", Type: "datetime"},
				{Key: "lease_owner", Name: "Lease Owner", Type: "text"},
				{Key: "lease_expires_at", Name: "Lease Expires At", Type: "datetime"},
				{Key: "fencing_token", Name: "Fencing Token", Type: "number"},
				{Key: "attempt", Name: "Attempt", Type: "number"},
				{Key: "max_attempts", Name: "Max Attempts", Type: "number"},
				{Key: "timeout_seconds", Name: "Timeout Seconds", Type: "number"},
				{Key: "next_retry_at", Name: "Next Retry At", Type: "datetime"},
				{Key: "retry_backoff", Name: "Retry Backoff", Type: "text"},
				{Key: "retry_delay_seconds", Name: "Retry Delay Seconds", Type: "number"},
				{Key: "retry_max_delay_seconds", Name: "Max Retry Delay Seconds", Type: "number"},
				{Key: "retry_backoff_seconds", Name: "Retry Backoff Seconds", Type: "number"},
				{Key: "idempotency_key", Name: "Idempotency Key", Type: "text"},
				{Key: "idempotency_scope", Name: "Idempotency Scope", Type: "text"},
				{Key: "last_command_scope", Name: "Last Command Scope", Type: "text"},
				{Key: "last_command_key", Name: "Last Command Key", Type: "text"},
				{Key: "workflow_key", Name: "Workflow Key", Type: "text"},
				{Key: "target_object", Name: "Target Object", Type: "text"},
				{Key: "payload_json", Name: "Payload JSON", Type: "text"},
				{Key: "result_json", Name: "Result JSON", Type: "text"},
				{Key: "error_message", Name: "Error Message", Type: "text"},
				{Key: "error_category", Name: "Error Category", Type: "text"},
				{Key: "recoverability", Name: "Recoverability", Type: "text"},
			},
		},
		{
			Key:    "job_run_event",
			Name:   "Job Run Event",
			Config: map[string]any{"scheduler_runtime": true},
			Fields: []definitionmodel.FieldSchema{
				{Key: "job_run_id", Name: "Job Run", Type: "text"},
				{Key: "event_type", Name: "Event Type", Type: "text"},
				{Key: "message", Name: "Message", Type: "text"},
				{Key: "metadata_json", Name: "Metadata JSON", Type: "text"},
				{Key: "created_at", Name: "Created At", Type: "datetime"},
			},
		},
		{
			Key:    "job_dead_letter",
			Name:   "Job Dead Letter",
			Config: map[string]any{"scheduler_runtime": true},
			Fields: []definitionmodel.FieldSchema{
				{Key: "job_run_id", Name: "Job Run", Type: "text"},
				{Key: "scheduler_definition_key", Name: "Job Definition", Type: "text"},
				{Key: "status", Name: "Status", Type: "text"},
				{Key: "reason", Name: "Reason", Type: "text"},
				{Key: "last_error", Name: "Last Error", Type: "text"},
				{Key: "failed_at", Name: "Failed At", Type: "datetime"},
				{Key: "resolved_at", Name: "Resolved At", Type: "datetime"},
				{Key: "resolved_by", Name: "Resolved By", Type: "text"},
				{Key: "resolution_note", Name: "Resolution Note", Type: "text"},
				{Key: "resolution_idempotency_key", Name: "Resolution Idempotency Key", Type: "text"},
			},
		},
		schedulerprojection.SchedulerSystemObjects()[4],
		schedulerprojection.SchedulerSystemObjects()[0],
	}
}

func schedulerReportExportEvidenceObjects() []definitionmodel.ObjectSchema {
	return []definitionmodel.ObjectSchema{
		{
			Key:    "report_definition",
			Name:   "Report Definition",
			Fields: schedulerEvidenceFields("name", "report_code", "status", "business_domain", "default_time_grain", "date_filter_label", "owner"),
		},
		{
			Key:    "report_query_run",
			Name:   "Report Query Run",
			Fields: schedulerEvidenceFields("query_number", "query_status", "status", "report_definition", "requested_by", "result_count", "source_record_count"),
		},
		{
			Key:    "report_export_audit",
			Name:   "Report Export Audit",
			Fields: schedulerEvidenceFields("export_number", "export_status", "status", "approval_result", "report_definition", "report_query_run", "row_count", "watermark_text"),
		},
		{
			Key:    "download_task",
			Name:   "Download Task",
			Fields: schedulerEvidenceFields("task_name", "download_status", "status", "token_status", "virus_scan_status", "file_format", "file_name", "report_export_audit"),
		},
	}
}

func schedulerEvidenceFields(keys ...string) []definitionmodel.FieldSchema {
	fields := make([]definitionmodel.FieldSchema, 0, len(keys))
	for _, key := range keys {
		fieldType := "text"
		if strings.HasSuffix(key, "_count") || key == "row_count" {
			fieldType = "number"
		}
		fields = append(fields, definitionmodel.FieldSchema{Key: key, Name: key, Type: fieldType})
	}
	return fields
}
