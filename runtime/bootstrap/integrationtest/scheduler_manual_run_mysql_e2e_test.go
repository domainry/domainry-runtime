package integrationtest

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerprojection "github.com/domainry/domainry-runtime/runtime/domain/scheduler/projection"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

func TestSchedulerManualRunLifecycleOnRealMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		if os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS") == "1" {
			t.Fatal("RUNTIME_MYSQL_TEST_DSN is required when RUNTIME_REQUIRE_REAL_DIALECTS=1")
		}
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	cfg := realDialectMySQLConfig(t, dsn, fmt.Sprintf("runtime_scheduler_manual_%d", time.Now().UnixNano()))
	store := openRuntimePersistenceFixture(t, cfg)
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatalf("ensure scheduler evidence schema: %v", err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure scheduler runtime schema: %v", err)
	}
	objects := schedulerprojection.SchedulerSystemObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatalf("sync scheduler storage: %v", err)
	}
	now := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "weekday_overdue_reminders", CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339), Data: map[string]any{
		"key": "weekday_overdue_reminders", "name": "Weekday overdue reminders", "status": "enabled", "trigger_type": "scheduled",
		"schedule_type": "interval", "interval_seconds": 60, "timezone": "UTC", "target_type": "workflow",
		"target_key": "scheduled:*", "max_attempts": 3, "timeout_seconds": 300,
	}}
	publishSchedulerDefinitionStoreFixture(t, store, definition.ID, definition.Data)
	services := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{
		TemplateID: "scheduler-manual-mysql", TemplateVersion: "1", Name: "Scheduler Manual MySQL",
		Objects: objects, Integrations: integrationmodel.IntegrationSchema{}, Store: store,
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "ops-manager", WorkspaceID: "default"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "scheduler.command"}, RecordScope: "all_records"})
	scheduler := services.Applications().Scheduler
	due := time.Now().UTC().Add(-2 * time.Second).Truncate(time.Second)
	rescheduled, err := scheduler.RescheduleDefinition(t.Context(), definition.ID, due, principal)
	if err != nil || rescheduled.Status != "rescheduled" || rescheduled.Run.Data["next_run_at"] != due.Format(time.RFC3339) {
		t.Fatalf("reschedule scheduler definition = %#v err=%v", rescheduled, err)
	}
	recordStore := recordpersistence.NewRecordStore(store)
	cursorObject := objectSchemaByKey(t, objects, "scheduler_cursor")
	cursor, found, err := recordStore.GetRecord(t.Context(), principal.WorkspaceID, cursorObject, definition.ID)
	if err != nil || !found || cursor.Data["next_run_at"] != due.Format(time.RFC3339) {
		t.Fatalf("persisted scheduler cursor = %#v found=%v err=%v", cursor, found, err)
	}
	result, err := scheduler.RunJob(t.Context(), definition.ID, "mysql-manual-run", principal)
	if err != nil {
		chain := []string{}
		for current := err; current != nil; current = errors.Unwrap(current) {
			chain = append(chain, fmt.Sprintf("%T: %v", current, current))
		}
		t.Fatalf("manual scheduler run: %s", strings.Join(chain, " <- "))
	}
	if result.Run.ID == "" || result.Run.Data["status"] != "succeeded" {
		t.Fatalf("manual scheduler result = %#v", result)
	}
	eventObject := objectSchemaByKey(t, objects, "job_run_event")
	events, err := recordStore.ListRecords(t.Context(), principal.WorkspaceID, eventObject, recordmodel.RecordListQuery{Page: 1, PageSize: 100})
	if err != nil || events.Total == 0 {
		t.Fatalf("manual scheduler events total=%d err=%v", events.Total, err)
	}
	eventTypes := map[string]bool{}
	for _, event := range events.Items {
		if event.Data["job_run_id"] == result.Run.ID {
			eventTypes[fmt.Sprint(event.Data["event_type"])] = true
		}
	}
	for _, eventType := range []string{"created", "lease_acquired", "state_changed"} {
		if !eventTypes[eventType] {
			t.Fatalf("manual scheduler event %q missing from %#v", eventType, events.Items)
		}
	}
	var manualAudits, rescheduleAudits int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND event = 'scheduler_job_manual_run' AND record_id = ?`, principal.WorkspaceID, result.Run.ID).Scan(&manualAudits); err != nil || manualAudits != 1 {
		t.Fatalf("manual scheduler audit count=%d err=%v", manualAudits, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND event = 'scheduler_definition_rescheduled' AND record_id = ?`, principal.WorkspaceID, definition.ID).Scan(&rescheduleAudits); err != nil || rescheduleAudits != 1 {
		t.Fatalf("scheduler reschedule audit count=%d err=%v", rescheduleAudits, err)
	}
	replayed, err := scheduler.RunJob(t.Context(), definition.ID, "mysql-manual-run", principal)
	if err != nil || replayed.Status != "replayed" || replayed.Run.ID != result.Run.ID {
		t.Fatalf("manual scheduler replay = %#v err=%v", replayed, err)
	}
	var replayEvents, replayAudits int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM job_run_event WHERE workspace_id = ? AND job_run_id = ?`, principal.WorkspaceID, result.Run.ID).Scan(&replayEvents); err != nil || replayEvents != len(eventTypes) {
		t.Fatalf("manual scheduler replay event count=%d want=%d err=%v", replayEvents, len(eventTypes), err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND event = 'scheduler_job_manual_run' AND record_id = ?`, principal.WorkspaceID, result.Run.ID).Scan(&replayAudits); err != nil || replayAudits != 1 {
		t.Fatalf("manual scheduler replay audit count=%d err=%v", replayAudits, err)
	}
}
