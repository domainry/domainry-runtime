package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type recordTimerCalendarProbe struct{ called bool }

func (p *recordTimerCalendarProbe) AddBusinessDuration(_ context.Context, key string, base time.Time, offset time.Duration, location *time.Location) (time.Time, error) {
	p.called = key == "weekday" && location.String() == "Asia/Shanghai"
	return base.Add(offset + 24*time.Hour), nil
}

func TestResolveRecordTimerScheduleSupportsAbsoluteRelativeAndBusinessCalendar(t *testing.T) {
	base := time.Date(2026, 7, 21, 9, 30, 0, 0, time.UTC)
	source := recordmodel.Record{Data: map[string]any{"starts_at": base.Format(time.RFC3339Nano)}}
	absolute, err := ResolveRecordTimerSchedule(t.Context(), RecordTimerSchedule{ScheduleMode: "absolute", DueAt: base, Timezone: "UTC"}, source, nil)
	if err != nil || !absolute.DueAt.Equal(base) {
		t.Fatalf("absolute schedule=%#v err=%v", absolute, err)
	}
	relative, err := ResolveRecordTimerSchedule(t.Context(), RecordTimerSchedule{ScheduleMode: "relative_field", SourceField: "starts_at", OffsetSeconds: -1800, Timezone: "UTC"}, source, nil)
	if err != nil || !relative.DueAt.Equal(base.Add(-30*time.Minute)) {
		t.Fatalf("relative schedule=%#v err=%v", relative, err)
	}
	calendar := &recordTimerCalendarProbe{}
	business, err := ResolveRecordTimerSchedule(t.Context(), RecordTimerSchedule{ScheduleMode: "business_calendar", SourceField: "starts_at", OffsetSeconds: 3600, Timezone: "Asia/Shanghai", BusinessCalendarKey: "weekday"}, source, calendar)
	if err != nil || !calendar.called || !business.DueAt.Equal(base.Add(25*time.Hour)) {
		t.Fatalf("business schedule=%#v called=%v err=%v", business, calendar.called, err)
	}
	if _, err := ResolveRecordTimerSchedule(t.Context(), RecordTimerSchedule{ScheduleMode: "relative_field", SourceField: "missing", Timezone: "UTC"}, source, nil); err == nil {
		t.Fatal("invalid relative source datetime accepted")
	}
}

func TestRecordTimerRetryPolicyDefaultsValidationAndBackoff(t *testing.T) {
	request := normalizeRecordTimerSchedule(RecordTimerSchedule{})
	if request.MaxAttempts != 10 || request.RetryDelaySeconds != 1 || request.RetryMaxDelaySeconds != 60 {
		t.Fatalf("retry defaults=%+v", request)
	}
	base := recordmodel.Record{Data: map[string]any{"attempt": 1, "retry_delay_seconds": 5, "retry_max_delay_seconds": 20}}
	if delay := recordTimerRetryDelay(base); delay != 5*time.Second {
		t.Fatalf("first retry delay=%v", delay)
	}
	base.Data["attempt"] = 2
	if delay := recordTimerRetryDelay(base); delay != 10*time.Second {
		t.Fatalf("second retry delay=%v", delay)
	}
	base.Data["attempt"] = 100
	if delay := recordTimerRetryDelay(base); delay != 20*time.Second {
		t.Fatalf("capped retry delay=%v", delay)
	}
	valid := RecordTimerSchedule{TimerKey: "timer", ObjectKey: "record", RecordID: "1", Purpose: "notify", DueAt: time.Now().UTC(), Timezone: "UTC", TargetType: "action", TargetKey: "notify", MaxAttempts: 1, RetryDelaySeconds: 1, RetryMaxDelaySeconds: 1}
	for _, mutate := range []func(*RecordTimerSchedule){
		func(value *RecordTimerSchedule) { value.MaxAttempts = 101 },
		func(value *RecordTimerSchedule) { value.RetryDelaySeconds = -1 },
		func(value *RecordTimerSchedule) { value.RetryMaxDelaySeconds = 0 },
		func(value *RecordTimerSchedule) { value.RetryMaxDelaySeconds = 86401 },
	} {
		invalid := valid
		mutate(&invalid)
		if err := validateRecordTimerSchedule(invalid); err == nil {
			t.Fatalf("invalid retry policy accepted: %+v", invalid)
		}
	}
}

func TestFailRecordTimerReleasesLeaseRetriesAndFencesStaleWorker(t *testing.T) {
	now := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	object := definitionmodel.ObjectSchema{Key: "record_timer"}
	schema := schedulerSchemaStub{snapshot: metadatamodel.MetadataSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{object}}}
	var saved recordmodel.Record
	var conditions map[string]any
	repository := &schedulerRepositoryFake{update: func(_ context.Context, workspaceID string, actualObject definitionmodel.ObjectSchema, record recordmodel.Record, actualConditions map[string]any) (bool, error) {
		if workspaceID != "workspace-a" || actualObject.Key != "record_timer" {
			t.Fatalf("failure persistence workspace=%q object=%q", workspaceID, actualObject.Key)
		}
		saved, conditions = record, actualConditions
		return true, nil
	}}
	service := NewSchedulerApplicationService(schema, nil, repository, nil)
	lease := RecordTimerLease{Owner: "worker-a", Token: 7, Record: recordmodel.Record{ID: "timer-1", Data: map[string]any{
		"status": "leased", "lease_owner": "worker-a", "lease_expires_at": now.Add(time.Minute).Format(time.RFC3339Nano), "fencing_token": 7,
		"attempt": 2, "max_attempts": 3, "retry_delay_seconds": 5, "retry_max_delay_seconds": 30,
	}}}
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, errors.New("injected"), now, schedulerRuntimeScope()); err != nil {
		t.Fatal(err)
	}
	if saved.Data["status"] != "scheduled" || saved.Data["due_at"] != now.Add(10*time.Second).Format(time.RFC3339Nano) || saved.Data["last_error"] != "backend.internal" || saved.Data["lease_owner"] != "" {
		t.Fatalf("retryable failed timer=%#v", saved)
	}
	if conditions["status"] != "leased" || conditions["lease_owner"] != "worker-a" || conditions["fencing_token"] != 7 {
		t.Fatalf("failure fencing conditions=%#v", conditions)
	}
	lease.Record.Data["attempt"] = 3
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, errors.New("injected"), now, schedulerRuntimeScope()); err != nil {
		t.Fatal(err)
	}
	if saved.Data["status"] != "failed" || saved.Data["failed_at"] != now.Format(time.RFC3339Nano) {
		t.Fatalf("terminal failed timer=%#v", saved)
	}
	repository.update = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.Record, map[string]any) (bool, error) {
		return false, nil
	}
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, errors.New("injected"), now, schedulerRuntimeScope()); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale failure transition=%v", err)
	}
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, nil, now, schedulerRuntimeScope()); err == nil {
		t.Fatal("nil execution failure accepted")
	}
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, errors.New("injected"), now, principalmodel.SystemScope{}); err == nil {
		t.Fatal("failure transition without system scope accepted")
	}
}
