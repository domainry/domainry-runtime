package integrationtest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerprojection "github.com/domainry/domainry-runtime/runtime/domain/scheduler/projection"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

type gymTimerFixtureSchema struct {
	snapshot appschemamodel.ApplicationSchemaSnapshot
}

func (s gymTimerFixtureSchema) SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return s.snapshot
}

type gymTimerFixtureRuntime struct {
	repository recordpersistence.RecordStore
	objects    map[string]definitionmodel.ObjectSchema
	mu         sync.Mutex
	alerts     map[string]int
}

func (r *gymTimerFixtureRuntime) ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return workflowmodel.WorkflowProcessResult{}, nil
}

func (r *gymTimerFixtureRuntime) ExecuteRecordTimer(ctx context.Context, execution schedulerapplication.RecordTimerExecution, _ principalmodel.Principal) error {
	switch execution.TargetKey {
	case "waitlist.offer_timeout":
		entry, found, err := r.repository.GetRecord(ctx, execution.WorkspaceID, r.objects["gym_waitlist_entry"], execution.RecordID)
		if err != nil || !found {
			return err
		}
		expired := gymTimerCloneRecord(entry)
		expired.Data["status"], expired.UpdatedAt = "expired", time.Now().UTC().Format(time.RFC3339Nano)
		updated, err := r.repository.UpdateRecordWhere(ctx, execution.WorkspaceID, r.objects["gym_waitlist_entry"], expired, map[string]any{"status": "notified"})
		if err != nil || !updated {
			return err
		}
		page, err := r.repository.ListRecords(ctx, execution.WorkspaceID, r.objects["gym_waitlist_entry"], recordmodel.RecordListQuery{
			Page: 1, PageSize: 1, Filters: map[string]any{"session_id": entry.Data["session_id"], "status": "waiting"},
			Sort: []recordmodel.RecordSortRule{{Field: "position", Direction: "asc"}, {Field: "id", Direction: "asc"}},
		})
		if err != nil || len(page.Items) == 0 {
			return err
		}
		next := gymTimerCloneRecord(page.Items[0])
		next.Data["status"], next.UpdatedAt = "notified", time.Now().UTC().Format(time.RFC3339Nano)
		_, err = r.repository.UpdateRecordWhere(ctx, execution.WorkspaceID, r.objects["gym_waitlist_entry"], next, map[string]any{"status": "waiting"})
		return err
	case "ticket.escalate":
		ticket, found, err := r.repository.GetRecord(ctx, execution.WorkspaceID, r.objects["gym_service_ticket"], execution.RecordID)
		if err != nil || !found {
			return err
		}
		escalated := gymTimerCloneRecord(ticket)
		escalated.Data["status"], escalated.UpdatedAt = "escalated", time.Now().UTC().Format(time.RFC3339Nano)
		updated, err := r.repository.UpdateRecordWhere(ctx, execution.WorkspaceID, r.objects["gym_service_ticket"], escalated, map[string]any{"status": "open"})
		if err != nil {
			return err
		}
		if updated {
			r.mu.Lock()
			r.alerts[execution.RecordID]++
			r.mu.Unlock()
		}
		return nil
	default:
		return fmt.Errorf("unsupported gym timer fixture target %q", execution.TargetKey)
	}
}

func gymTimerCloneRecord(record recordmodel.Record) recordmodel.Record {
	data := make(map[string]any, len(record.Data))
	for key, value := range record.Data {
		data[key] = value
	}
	record.Data = data
	return record
}

func gymTimerFixtureObjects() []definitionmodel.ObjectSchema {
	objects := append([]definitionmodel.ObjectSchema(nil), schedulerprojection.SchedulerSystemObjects()...)
	return append(objects,
		definitionmodel.ObjectSchema{Key: "gym_waitlist_entry", Fields: []definitionmodel.FieldSchema{{Key: "session_id", Type: "text"}, {Key: "status", Type: "text"}, {Key: "position", Type: "number"}}},
		definitionmodel.ObjectSchema{Key: "gym_class_session", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}},
		definitionmodel.ObjectSchema{Key: "gym_service_ticket", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}},
	)
}

func gymTimerFixtureService(t *testing.T) (*schedulerapplication.SchedulerApplicationService, recordpersistence.RecordStore, *gymTimerFixtureRuntime, []definitionmodel.ObjectSchema) {
	t.Helper()
	store := openSchedulerRuntimeTestStore(t)
	t.Cleanup(func() { store.Close() })
	objects := gymTimerFixtureObjects()
	if err := metadataStore(store).SyncManifestStorage(t.Context(), manifestmodel.ManifestSchema{Objects: objects}); err != nil {
		t.Fatal(err)
	}
	repository := recordLegacyStore(store)
	objectMap := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		objectMap[object.Key] = object
	}
	runtime := &gymTimerFixtureRuntime{repository: repository, objects: objectMap, alerts: map[string]int{}}
	service := schedulerapplication.NewSchedulerApplicationServiceWithWorker(gymTimerFixtureSchema{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: objects}}, runtime, repository, nil, workerplatform.Dependencies{WorkerID: workerplatform.WorkerID("gym-timer-fixture")})
	service.ConfigureWorker(schedulerapplication.WorkerConfig{Enabled: true, BatchSize: 100, LeaseTTL: time.Minute})
	return service, repository, runtime, objects
}

func gymTimerInsertRecord(t *testing.T, repository recordpersistence.RecordStore, object definitionmodel.ObjectSchema, id string, data map[string]any, now time.Time) {
	t.Helper()
	stamp := now.UTC().Format(time.RFC3339Nano)
	if err := repository.InsertRecord(t.Context(), "default", object, recordmodel.Record{ID: id, Data: data, CreatedAt: stamp, UpdatedAt: stamp}); err != nil {
		t.Fatal(err)
	}
}

func gymTimerSchedule(t *testing.T, service *schedulerapplication.SchedulerApplicationService, now time.Time, request schedulerapplication.RecordTimerSchedule) recordmodel.Record {
	t.Helper()
	timer, err := service.ScheduleRecordTimer(t.Context(), "default", request, recordmodel.Record{ID: request.RecordID}, nil, now, schedulerRuntimeSystemScope())
	if err != nil {
		t.Fatal(err)
	}
	return timer
}

func TestGymWaitlistTimeoutAdvancesOnlyNextEntryAndRejectsLateConfirmation(t *testing.T) {
	service, repository, _, objects := gymTimerFixtureService(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	waitlist := schedulerRuntimeObjectByKey(t, objects, "gym_waitlist_entry")
	for index, status := range []string{"notified", "waiting", "waiting"} {
		gymTimerInsertRecord(t, repository, waitlist, fmt.Sprintf("wait-%d", index+1), map[string]any{"session_id": "class-1", "status": status, "position": index + 1}, now)
	}
	gymTimerSchedule(t, service, now, schedulerapplication.RecordTimerSchedule{TimerKey: "wait-1-timeout", ObjectKey: waitlist.Key, RecordID: "wait-1", Purpose: "offer_timeout", DueAt: now, TargetType: "action", TargetKey: "waitlist.offer_timeout", Sequence: 1})
	processed, err := service.ProcessDueRecordTimers(t.Context(), "default", now, 10, schedulerRuntimePrincipal(), schedulerRuntimeSystemScope())
	if err != nil || processed != 1 {
		t.Fatalf("waitlist timer processed=%d err=%v", processed, err)
	}
	for id, want := range map[string]string{"wait-1": "expired", "wait-2": "notified", "wait-3": "waiting"} {
		record, found, getErr := repository.GetRecord(t.Context(), "default", waitlist, id)
		if getErr != nil || !found || record.Data["status"] != want {
			t.Fatalf("waitlist %s=%#v found=%v err=%v want=%s", id, record, found, getErr, want)
		}
	}
	late, _, _ := repository.GetRecord(t.Context(), "default", waitlist, "wait-1")
	late = gymTimerCloneRecord(late)
	late.Data["status"] = "confirmed"
	for attempt := range 2 {
		updated, updateErr := repository.UpdateRecordWhere(t.Context(), "default", waitlist, late, map[string]any{"status": "notified"})
		if updateErr != nil || updated {
			t.Fatalf("late confirmation attempt %d updated=%v err=%v", attempt, updated, updateErr)
		}
	}
}

func TestGymClassCancellationCancelsEveryFutureAttendanceTimer(t *testing.T) {
	service, repository, _, objects := gymTimerFixtureService(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	classSession := schedulerRuntimeObjectByKey(t, objects, "gym_class_session")
	gymTimerInsertRecord(t, repository, classSession, "class-1", map[string]any{"status": "scheduled"}, now)
	for index, purpose := range []string{"check_in_open", "check_in_close", "no_show", "attendance_finalize"} {
		gymTimerSchedule(t, service, now, schedulerapplication.RecordTimerSchedule{TimerKey: purpose, ObjectKey: classSession.Key, RecordID: "class-1", Purpose: purpose, DueAt: now.Add(time.Duration(index+1) * time.Hour), TargetType: "action", TargetKey: "attendance.window", Sequence: int64(index)})
	}
	cancelled, err := service.CancelRecordTimers(t.Context(), "default", classSession.Key, "class-1", "", now, schedulerRuntimeSystemScope())
	if err != nil || cancelled != 4 {
		t.Fatalf("cancelled attendance timers=%d err=%v", cancelled, err)
	}
	page, err := repository.ListRecords(t.Context(), "default", schedulerRuntimeObjectByKey(t, objects, "record_timer"), recordmodel.RecordListQuery{Page: 1, PageSize: 10, Filters: map[string]any{"object_key": classSession.Key, "record_id": "class-1", "status": "scheduled"}})
	if err != nil || page.Total != 0 {
		t.Fatalf("future attendance timers=%d err=%v", page.Total, err)
	}
}

func TestGymUrgentTicketResponseCancelsAlertAndUnansweredEscalatesOnce(t *testing.T) {
	service, repository, runtime, objects := gymTimerFixtureService(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	tickets := schedulerRuntimeObjectByKey(t, objects, "gym_service_ticket")
	for _, id := range []string{"ticket-responded", "ticket-unanswered"} {
		gymTimerInsertRecord(t, repository, tickets, id, map[string]any{"status": "open"}, now)
		gymTimerSchedule(t, service, now, schedulerapplication.RecordTimerSchedule{TimerKey: id + ":sla", ObjectKey: tickets.Key, RecordID: id, Purpose: "response_sla", DueAt: now.Add(2 * time.Hour), TargetType: "action", TargetKey: "ticket.escalate"})
	}
	responded, _, _ := repository.GetRecord(t.Context(), "default", tickets, "ticket-responded")
	responded = gymTimerCloneRecord(responded)
	responded.Data["status"], responded.UpdatedAt = "responded", now.Add(time.Hour).Format(time.RFC3339Nano)
	updated, err := repository.UpdateRecordWhere(t.Context(), "default", tickets, responded, map[string]any{"status": "open"})
	if err != nil || !updated {
		t.Fatalf("respond ticket updated=%v err=%v", updated, err)
	}
	if cancelled, err := service.CancelRecordTimers(t.Context(), "default", tickets.Key, "ticket-responded", "response_sla", now.Add(time.Hour), schedulerRuntimeSystemScope()); err != nil || cancelled != 1 {
		t.Fatalf("cancel responded ticket timer=%d err=%v", cancelled, err)
	}
	for range 2 {
		if _, err := service.ProcessDueRecordTimers(t.Context(), "default", now.Add(3*time.Hour), 10, schedulerRuntimePrincipal(), schedulerRuntimeSystemScope()); err != nil {
			t.Fatal(err)
		}
	}
	runtime.mu.Lock()
	respondedAlerts, unansweredAlerts := runtime.alerts["ticket-responded"], runtime.alerts["ticket-unanswered"]
	runtime.mu.Unlock()
	if respondedAlerts != 0 || unansweredAlerts != 1 {
		t.Fatalf("ticket alerts responded=%d unanswered=%d", respondedAlerts, unansweredAlerts)
	}
}

func TestGymCardPackageExpiryAndNinetyDayReviewUseRelativeRecordTimers(t *testing.T) {
	service, repository, _, objects := gymTimerFixtureService(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	for _, testCase := range []struct {
		key       string
		objectKey string
		field     string
		base      time.Time
		offset    int
		want      time.Time
	}{
		{key: "card-expiry", objectKey: "gym_member_card", field: "expires_at", base: now.Add(30 * 24 * time.Hour), want: now.Add(30 * 24 * time.Hour)},
		{key: "package-expiry", objectKey: "gym_training_package", field: "expires_at", base: now.Add(60 * 24 * time.Hour), want: now.Add(60 * 24 * time.Hour)},
		{key: "inactive-review", objectKey: "gym_member", field: "last_entry_at", base: now, offset: 90 * 24 * 60 * 60, want: now.Add(90 * 24 * time.Hour)},
	} {
		timer, err := service.ScheduleRecordTimer(t.Context(), "default", schedulerapplication.RecordTimerSchedule{
			TimerKey: testCase.key, ObjectKey: testCase.objectKey, RecordID: testCase.key, Purpose: testCase.key,
			ScheduleMode: "relative_field", SourceField: testCase.field, OffsetSeconds: testCase.offset,
			TargetType: "action", TargetKey: "lifecycle.review",
		}, recordmodel.Record{ID: testCase.key, Data: map[string]any{testCase.field: testCase.base.Format(time.RFC3339Nano)}}, nil, now, schedulerRuntimeSystemScope())
		if err != nil {
			t.Fatalf("schedule %s: %v", testCase.key, err)
		}
		if timer.Data["due_at"] != testCase.want.Format(time.RFC3339Nano) {
			t.Fatalf("timer %s due_at=%v want=%s", testCase.key, timer.Data["due_at"], testCase.want.Format(time.RFC3339Nano))
		}
	}
	page, err := repository.ListRecords(t.Context(), "default", schedulerRuntimeObjectByKey(t, objects, "record_timer"), recordmodel.RecordListQuery{Page: 1, PageSize: 10, Filters: map[string]any{"status": "scheduled"}})
	if err != nil || page.Total != 3 {
		t.Fatalf("lifecycle timers=%d err=%v", page.Total, err)
	}
}
