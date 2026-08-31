package recordtimer

import (
	"context"
	"errors"
	"testing"
	"time"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimerpolicy "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func TestRecordTimerRetryPolicyDefaultsValidationAndBackoff(t *testing.T) {
	request := recordtimerpolicy.NormalizeSchedule(RecordTimerSchedule{})
	if request.MaxAttempts != 10 || request.RetryDelaySeconds != 1 || request.RetryMaxDelaySeconds != 60 {
		t.Fatalf("retry defaults=%+v", request)
	}
	base := recordmodel.Record{Data: map[string]any{"attempt": 1, "retry_delay_seconds": 5, "retry_max_delay_seconds": 20}}
	if delay := recordtimerpolicy.RetryDelay(base); delay != 5*time.Second {
		t.Fatalf("first retry delay=%v", delay)
	}
	base.Data["attempt"] = 2
	if delay := recordtimerpolicy.RetryDelay(base); delay != 10*time.Second {
		t.Fatalf("second retry delay=%v", delay)
	}
	base.Data["attempt"] = 100
	if delay := recordtimerpolicy.RetryDelay(base); delay != 20*time.Second {
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
		if err := recordtimerpolicy.ValidateSchedule(invalid); err == nil {
			t.Fatalf("invalid retry policy accepted: %+v", invalid)
		}
	}
}

func TestFailRecordTimerReleasesLeaseRetriesAndFencesStaleWorker(t *testing.T) {
	now := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	object := definitionmodel.ObjectSchema{Key: "record_timer"}
	schema := recordTimerSchemaStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{object, {Key: "record_timer_event"}}}}
	var saved recordmodel.Record
	var conditions map[string]any
	repository := &recordTimerRepositoryFake{commit: func(_ context.Context, workspaceID string, commits []transactionmodel.RecordMutationCommit) error {
		if workspaceID != "workspace-a" || len(commits) != 2 || commits[0].Object.Key != "record_timer" || commits[1].Object.Key != "record_timer_event" {
			t.Fatalf("failure persistence workspace=%q commits=%#v", workspaceID, commits)
		}
		saved, conditions = commits[0].Record, commits[0].Conditions
		if commits[1].Record.Data["record_timer_id"] != "timer-1" || commits[1].Record.Data["event_type"] == "" {
			t.Fatalf("record timer event=%#v", commits[1].Record)
		}
		return nil
	}}
	service := NewRecordTimerApplicationService(schema, nil, repository)
	lease := RecordTimerLease{Owner: "worker-a", Token: 7, Record: recordmodel.Record{ID: "timer-1", Data: map[string]any{
		"status": "leased", "lease_owner": "worker-a", "lease_expires_at": now.Add(time.Minute).Format(time.RFC3339Nano), "fencing_token": 7,
		"attempt": 2, "max_attempts": 3, "retry_delay_seconds": 5, "retry_max_delay_seconds": 30,
	}}}
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, errors.New("injected"), now, recordTimerRuntimeScope()); err != nil {
		t.Fatal(err)
	}
	if saved.Data["status"] != "scheduled" || saved.Data["due_at"] != now.Add(10*time.Second).Format(time.RFC3339Nano) || saved.Data["last_error"] != "backend.internal" || saved.Data["lease_owner"] != "" {
		t.Fatalf("retryable failed timer=%#v", saved)
	}
	if conditions["status"] != "leased" || conditions["lease_owner"] != "worker-a" || conditions["fencing_token"] != 7 {
		t.Fatalf("failure fencing conditions=%#v", conditions)
	}
	lease.Record.Data["attempt"] = 3
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, errors.New("injected"), now, recordTimerRuntimeScope()); err != nil {
		t.Fatal(err)
	}
	if saved.Data["status"] != "failed" || saved.Data["failed_at"] != now.Format(time.RFC3339Nano) {
		t.Fatalf("terminal failed timer=%#v", saved)
	}
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, nil, now, recordTimerRuntimeScope()); err == nil {
		t.Fatal("nil execution failure accepted")
	}
	if err := service.FailRecordTimer(t.Context(), "workspace-a", lease, errors.New("injected"), now, principalmodel.SystemScope{}); err == nil {
		t.Fatal("failure transition without system scope accepted")
	}
}
