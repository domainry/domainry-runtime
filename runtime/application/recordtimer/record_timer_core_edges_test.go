package recordtimer

import (
	"context"
	"errors"
	"testing"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimerpolicy "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func recordTimerValidSchedule(now time.Time) RecordTimerSchedule {
	return RecordTimerSchedule{
		TimerKey: " timer ", ObjectKey: " object ", RecordID: " record ", Purpose: " purpose ",
		ScheduleMode: "absolute", DueAt: now.Add(time.Hour), Timezone: "UTC", TargetType: "action", TargetKey: " action ",
		PayloadJSON: "{}", MaxAttempts: 3, RetryDelaySeconds: 2, RetryMaxDelaySeconds: 8,
	}
}

func recordTimerTestService(repository *recordTimerRepositoryFake, now time.Time, includeObject bool) *RecordTimerApplicationService {
	objects := []definitionmodel.ObjectSchema{}
	if includeObject {
		objects = append(objects, definitionmodel.ObjectSchema{Key: "record_timer"})
	}
	return NewRecordTimerApplicationServiceWithWorker(
		recordTimerSchemaStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: objects}}, nil, repository,
		workerplatform.Dependencies{Clock: recordTimerFixedClock{now: now}},
	)
}

func TestBuildAndScheduleRecordTimerEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	request := recordTimerValidSchedule(now)
	repository := &recordTimerRepositoryFake{}
	service := recordTimerTestService(repository, now, true)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.BuildRecordTimerMutation(cancelled, "workspace", request, now); err == nil {
		t.Fatal("cancelled build accepted")
	}
	invalid := request
	invalid.TimerKey = ""
	if _, err := service.BuildRecordTimerMutation(t.Context(), "workspace", invalid, now); err == nil {
		t.Fatal("invalid build accepted")
	}
	if _, err := recordTimerTestService(repository, now, false).BuildRecordTimerMutation(t.Context(), "workspace", request, now); err == nil {
		t.Fatal("missing object accepted")
	}
	commit, err := service.BuildRecordTimerMutation(t.Context(), "workspace", request, time.Time{})
	if err != nil || commit.Operation != "create" || commit.Record.CreatedAt != now.Format(time.RFC3339Nano) {
		t.Fatalf("commit=%#v err=%v", commit, err)
	}
	if _, err := service.BuildRecordTimerMutationFromSource(t.Context(), "workspace", RecordTimerSchedule{ScheduleMode: "relative_field", SourceField: "missing"}, recordmodel.Record{}, nil, now); err == nil {
		t.Fatal("source resolution error accepted")
	}
	if built, err := service.BuildRecordTimerMutationFromSource(t.Context(), "workspace", request, recordmodel.Record{}, nil, now); err != nil || built.Record.ID == "" {
		t.Fatalf("built=%#v err=%v", built, err)
	}
	if _, err := service.Schedule(t.Context(), "workspace", request, recordmodel.Record{}, nil, now, principalmodel.SystemScope{}); err == nil {
		t.Fatal("schedule without system scope accepted")
	}
	if _, err := service.Schedule(t.Context(), "workspace", invalid, recordmodel.Record{}, nil, now, recordTimerRuntimeScope()); err == nil {
		t.Fatal("invalid schedule accepted")
	}
	repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
		return errors.New("commit")
	}
	repository.get = func(_ context.Context, _ string, _ definitionmodel.ObjectSchema, id string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{ID: id}, true, nil
	}
	if existing, err := service.Schedule(t.Context(), "workspace", request, recordmodel.Record{}, nil, now, recordTimerRuntimeScope()); err != nil || existing.ID == "" {
		t.Fatalf("idempotent existing=%#v err=%v", existing, err)
	}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, nil
	}
	if _, err := service.Schedule(t.Context(), "workspace", request, recordmodel.Record{}, nil, now, recordTimerRuntimeScope()); err == nil {
		t.Fatal("commit error with absent record lost")
	}
	repository.get = func(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, errors.New("get")
	}
	if _, err := service.Schedule(t.Context(), "workspace", request, recordmodel.Record{}, nil, now, recordTimerRuntimeScope()); err == nil {
		t.Fatal("commit/get error lost")
	}
	repository.commit = nil
	if scheduled, err := service.Schedule(t.Context(), "workspace", request, recordmodel.Record{}, nil, now, recordTimerRuntimeScope()); err != nil || scheduled.ID == "" {
		t.Fatalf("scheduled=%#v err=%v", scheduled, err)
	}
}

func TestRecordTimerTerminalAndSupersedeEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	service := recordTimerTestService(&recordTimerRepositoryFake{}, now, true)
	timer := recordmodel.Record{ID: "timer", Data: map[string]any{"status": "scheduled", "fencing_token": 2, "nested": "value"}}
	if _, err := service.BuildRecordTimerTerminalMutation(t.Context(), timer, "invalid", now); err == nil {
		t.Fatal("invalid terminal status accepted")
	}
	if _, err := recordTimerTestService(&recordTimerRepositoryFake{}, now, false).BuildRecordTimerTerminalMutation(t.Context(), timer, "cancelled", now); err == nil {
		t.Fatal("missing timer object accepted")
	}
	invalidTimer := cloneRecordTimer(timer)
	invalidTimer.Data["status"] = "leased"
	if _, err := service.BuildRecordTimerTerminalMutation(t.Context(), invalidTimer, "cancelled", now); err == nil {
		t.Fatal("non-scheduled timer cancelled")
	}
	terminal, err := service.BuildRecordTimerTerminalMutation(t.Context(), timer, "cancelled", time.Time{})
	if err != nil || terminal.Record.Data["status"] != "cancelled" || timer.Data["status"] != "scheduled" {
		t.Fatalf("terminal=%#v source=%#v err=%v", terminal, timer, err)
	}
	if _, err := service.BuildRecordTimerSupersedeMutations(t.Context(), "workspace", invalidTimer, recordTimerValidSchedule(now), now); err == nil {
		t.Fatal("invalid current timer superseded")
	}
	badReplacement := recordTimerValidSchedule(now)
	badReplacement.TimerKey = ""
	if _, err := service.BuildRecordTimerSupersedeMutations(t.Context(), "workspace", timer, badReplacement, now); err == nil {
		t.Fatal("invalid replacement accepted")
	}
	mutations, err := service.BuildRecordTimerSupersedeMutations(t.Context(), "workspace", timer, recordTimerValidSchedule(now), now)
	if err != nil || len(mutations) != 2 || mutations[0].Record.Data["status"] != "superseded" || mutations[1].Record.Data["supersedes_timer_id"] != timer.ID {
		t.Fatalf("mutations=%#v err=%v", mutations, err)
	}
}

func TestCancelRecordTimersEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	timer := recordmodel.Record{ID: "timer", Data: map[string]any{"status": "scheduled", "fencing_token": 1}}
	if _, err := recordTimerTestService(&recordTimerRepositoryFake{}, now, true).CancelRecordTimers(t.Context(), "workspace", "object", "record", "", now, principalmodel.SystemScope{}); err == nil {
		t.Fatal("cancel without system scope accepted")
	}
	if _, err := recordTimerTestService(&recordTimerRepositoryFake{}, now, false).CancelRecordTimers(t.Context(), "workspace", "object", "record", "", now, recordTimerRuntimeScope()); err == nil {
		t.Fatal("cancel without object accepted")
	}
	repository := &recordTimerRepositoryFake{list: func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, errors.New("list")
	}}
	if _, err := recordTimerTestService(repository, now, true).CancelRecordTimers(t.Context(), "workspace", "object", "record", " purpose ", now, recordTimerRuntimeScope()); err == nil {
		t.Fatal("list error lost")
	}
	repository.list = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{}, nil
	}
	if count, err := recordTimerTestService(repository, now, true).CancelRecordTimers(t.Context(), "workspace", "object", "record", "", now, recordTimerRuntimeScope()); err != nil || count != 0 {
		t.Fatalf("empty count=%d err=%v", count, err)
	}
	repository.list = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		invalid := cloneRecordTimer(timer)
		invalid.Data["status"] = "leased"
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{invalid}}, nil
	}
	if _, err := recordTimerTestService(repository, now, true).CancelRecordTimers(t.Context(), "workspace", "object", "record", "", now, recordTimerRuntimeScope()); err == nil {
		t.Fatal("terminal build error lost")
	}
	repository.list = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{timer}}, nil
	}
	repository.commit = func(context.Context, string, []transactionmodel.RecordMutationCommit) error {
		return errors.New("commit")
	}
	if _, err := recordTimerTestService(repository, now, true).CancelRecordTimers(t.Context(), "workspace", "object", "record", "", now, recordTimerRuntimeScope()); err == nil {
		t.Fatal("cancel commit error lost")
	}
	calls := 0
	repository.commit = nil
	repository.list = func(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		calls++
		if calls == 1 {
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{timer}}, nil
		}
		return recordmodel.RecordPageResult{}, nil
	}
	if count, err := recordTimerTestService(repository, now, true).CancelRecordTimers(t.Context(), "workspace", "object", "record", "", now, recordTimerRuntimeScope()); err != nil || count != 1 {
		t.Fatalf("cancelled count=%d err=%v", count, err)
	}
}

func TestRecordTimerNormalizationValidationAndHelpersEdges(t *testing.T) {
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	normalized := recordtimerpolicy.NormalizeSchedule(RecordTimerSchedule{
		TimerKey: " timer ", ObjectKey: " object ", RecordID: " record ", Purpose: " purpose ",
		ScheduleMode: " absolute ", DueAt: now, SourceField: " source ", Timezone: " UTC ", BusinessCalendarKey: " calendar ",
		TargetType: " action ", TargetKey: " target ", PayloadJSON: " {\"a\":1} ", SupersedesTimerID: " old ",
		MaxAttempts: 2, RetryDelaySeconds: 3, RetryMaxDelaySeconds: 4,
	})
	if normalized.TimerKey != "timer" || normalized.PayloadJSON != "{\"a\":1}" || normalized.MaxAttempts != 2 {
		t.Fatalf("normalized=%+v", normalized)
	}
	valid := recordTimerValidSchedule(now)
	valid = recordtimerpolicy.NormalizeSchedule(valid)
	if err := recordtimerpolicy.ValidateSchedule(valid); err != nil {
		t.Fatalf("valid schedule=%v", err)
	}
	requiredMutations := []func(*RecordTimerSchedule){
		func(v *RecordTimerSchedule) { v.TimerKey = "" }, func(v *RecordTimerSchedule) { v.ObjectKey = "" },
		func(v *RecordTimerSchedule) { v.RecordID = "" }, func(v *RecordTimerSchedule) { v.Purpose = "" },
		func(v *RecordTimerSchedule) { v.TargetKey = "" }, func(v *RecordTimerSchedule) { v.DueAt = time.Time{} },
	}
	for _, mutate := range requiredMutations {
		candidate := valid
		mutate(&candidate)
		if recordtimerpolicy.ValidateSchedule(candidate) == nil {
			t.Fatalf("required candidate accepted: %+v", candidate)
		}
	}
	for _, mode := range []string{"relative_field", "business_calendar", "invalid"} {
		candidate := valid
		candidate.ScheduleMode = mode
		candidate.SourceField = "source"
		candidate.BusinessCalendarKey = "calendar"
		if mode == "invalid" {
			if recordtimerpolicy.ValidateSchedule(candidate) == nil {
				t.Fatal("invalid mode accepted")
			}
		} else if err := recordtimerpolicy.ValidateSchedule(candidate); err != nil {
			t.Fatalf("mode %s: %v", mode, err)
		}
	}
	for _, mutate := range []func(*RecordTimerSchedule){
		func(v *RecordTimerSchedule) { v.ScheduleMode, v.SourceField = "relative_field", "" },
		func(v *RecordTimerSchedule) { v.ScheduleMode, v.BusinessCalendarKey = "business_calendar", "" },
		func(v *RecordTimerSchedule) { v.TargetType = "invalid" },
		func(v *RecordTimerSchedule) { v.MaxAttempts = 0 }, func(v *RecordTimerSchedule) { v.MaxAttempts = 101 },
		func(v *RecordTimerSchedule) { v.RetryDelaySeconds = 0 },
		func(v *RecordTimerSchedule) { v.RetryMaxDelaySeconds = 1 },
		func(v *RecordTimerSchedule) { v.RetryMaxDelaySeconds = 86401 },
		func(v *RecordTimerSchedule) { v.Timezone = "bad/zone" },
	} {
		candidate := valid
		mutate(&candidate)
		if recordtimerpolicy.ValidateSchedule(candidate) == nil {
			t.Fatalf("invalid candidate accepted: %+v", candidate)
		}
	}
	workflow := valid
	workflow.TargetType = "workflow"
	if err := recordtimerpolicy.ValidateSchedule(workflow); err != nil {
		t.Fatalf("workflow target=%v", err)
	}
	retryRecord := recordmodel.Record{Data: map[string]any{"attempt": 3, "retry_delay_seconds": 10, "retry_max_delay_seconds": 15}}
	if delay := recordtimerpolicy.RetryDelay(retryRecord); delay != 15*time.Second {
		t.Fatalf("clamped delay=%v", delay)
	}
	original := recordmodel.Record{ID: "record", Data: map[string]any{"status": "scheduled"}}
	cloned := cloneRecordTimer(original)
	cloned.Data["status"] = "changed"
	if original.Data["status"] != "scheduled" {
		t.Fatal("clone mutated original")
	}
	firstID := recordTimerID(" workspace ", valid)
	if firstID == "" || firstID != recordTimerID("workspace", valid) || valid.String() == "" {
		t.Fatalf("id=%q string=%q", firstID, valid.String())
	}
}
