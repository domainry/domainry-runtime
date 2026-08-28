package validation

import (
	"context"
	"errors"
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestSchedulerDefinitionEnvelopeAndTargetMatrix(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := SchedulerValidateDefinitionContract(cancelled, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error=%v", err)
	}
	if err := SchedulerValidateDefinitionContract(t.Context(), validSchedulerDefinition()); err != nil {
		t.Fatalf("valid contract: %v", err)
	}

	assertSchedulerCode(t, map[string]any{}, "backend.scheduler.target_type_required")
	assertSchedulerCode(t, map[string]any{"target_type": ""}, "backend.scheduler.target_type_required")
	assertSchedulerCode(t, map[string]any{"target_type": "script"}, "backend.scheduler.target_type_unsupported")
	assertSchedulerCode(t, map[string]any{"target_type": "workflow"}, "backend.scheduler.target_key_required")
	assertSchedulerCode(t, map[string]any{"target_type": "workflow", "target_key": ""}, "backend.scheduler.target_key_required")
	assertSchedulerCode(t, map[string]any{"target_type": "workflow", "target_key": "plain"}, "backend.scheduler.workflow_target_invalid")
	assertSchedulerCode(t, map[string]any{"target_type": "report_export", "target_key": "scheduled:report"}, "backend.scheduler.report_target_invalid")
	report := validSchedulerDefinition()
	report["target_type"], report["target_key"] = "report_export", "sales_report"
	if err := SchedulerValidateDefinitionContract(t.Context(), report); err != nil {
		t.Fatalf("report target: %v", err)
	}
	wildcard := validSchedulerDefinition()
	wildcard["target_key"] = "scheduled:*"
	if err := SchedulerValidateDefinitionContract(t.Context(), wildcard); err != nil {
		t.Fatalf("workflow wildcard: %v", err)
	}
}

func TestSchedulerDefinitionLimitsAndPolicies(t *testing.T) {
	for _, test := range []struct {
		field string
		value any
		code  string
	}{
		{"timezone", "Mars/Olympus", "backend.scheduler.timezone_invalid"},
		{"max_attempts", 0, "backend.scheduler.max_attempts_invalid"},
		{"max_attempts", 101, "backend.scheduler.max_attempts_invalid"},
		{"timeout_seconds", 0, "backend.scheduler.timeout_invalid"},
		{"timeout_seconds", 86401, "backend.scheduler.timeout_invalid"},
	} {
		data := validSchedulerDefinition()
		data[test.field] = test.value
		assertSchedulerCode(t, data, test.code)
	}
	for _, value := range []any{1, 100} {
		data := validSchedulerDefinition()
		data["max_attempts"], data["timeout_seconds"] = value, value
		if err := SchedulerValidateDefinitionContract(t.Context(), data); err != nil {
			t.Fatalf("valid limit %v: %v", value, err)
		}
	}
	for _, value := range []any{-1, 101} {
		data := validSchedulerDefinition()
		data["missed_window_policy"], data["max_catchup_windows"] = "catch_up_bounded", value
		assertSchedulerCode(t, data, "backend.scheduler.max_catchup_windows_invalid")
	}
	for _, policy := range []string{"skip", "catch_up_one", "catch_up_bounded", "CATCH_UP_ONE"} {
		data := validSchedulerDefinition()
		data["missed_window_policy"], data["max_catchup_windows"] = policy, 100
		if err := SchedulerValidateDefinitionContract(t.Context(), data); err != nil {
			t.Fatalf("policy %s: %v", policy, err)
		}
	}
	data := validSchedulerDefinition()
	data["missed_window_policy"] = "catch_everything"
	assertSchedulerCode(t, data, "backend.scheduler.missed_window_policy_invalid")
	for _, field := range []string{"timezone", "missed_window_policy", "schedule_type"} {
		data = validSchedulerDefinition()
		data[field] = ""
		if field == "schedule_type" {
			data["schedule_expression"] = "daily"
		}
		if err := SchedulerValidateDefinitionContract(t.Context(), data); err != nil {
			t.Fatalf("empty %s: %v", field, err)
		}
	}
	data = validSchedulerDefinition()
	delete(data, "timezone")
	if err := SchedulerValidateDefinitionContract(t.Context(), data); err != nil {
		t.Fatalf("missing timezone: %v", err)
	}
	data = validSchedulerDefinition()
	delete(data, "schedule_type")
	data["schedule_expression"] = "daily"
	if err := SchedulerValidateDefinitionContract(t.Context(), data); err != nil {
		t.Fatalf("missing schedule type: %v", err)
	}
	if schedulerMissedWindowPolicy(recordmodel.Record{}) != "skip" || schedulerMissedWindowPolicy(recordmodel.Record{Data: map[string]any{"missed_window_policy": "CATCH_UP_ONE"}}) != "catch_up_one" {
		t.Fatal("missed-window normalization")
	}
}

func TestSchedulerDefinitionScheduleMatrix(t *testing.T) {
	valid := []map[string]any{
		{"schedule_type": "interval", "interval_seconds": 1},
		{"schedule_type": "daily_at", "time_of_day": "08:30"},
		{"schedule_type": "weekly_at", "time_of_day": "08:30:15", "day_of_week": "monday"},
		{"schedule_type": "monthly_at", "time_of_day": "08:30", "day_of_month": 1},
		{"schedule_type": "cron", "schedule_expression": "0 8 * * *"},
		{"schedule_type": "", "schedule_expression": "hourly"},
		{"schedule_type": "", "schedule_expression": "daily"},
		{"schedule_type": "", "schedule_expression": "weekly"},
		{"schedule_type": "", "schedule_expression": "monthly"},
	}
	for index, schedule := range valid {
		data := validSchedulerDefinition()
		for key, value := range schedule {
			data[key] = value
		}
		if err := SchedulerValidateDefinitionContract(t.Context(), data); err != nil {
			t.Fatalf("valid schedule %d %#v: %v", index, schedule, err)
		}
	}

	invalid := []struct {
		values map[string]any
		code   string
	}{
		{map[string]any{"schedule_type": "interval", "interval_seconds": 0}, "backend.scheduler.interval_invalid"},
		{map[string]any{"schedule_type": "daily_at", "time_of_day": "25:00"}, "backend.scheduler.time_of_day_invalid"},
		{map[string]any{"schedule_type": "weekly_at", "time_of_day": "bad", "day_of_week": "monday"}, "backend.scheduler.time_of_day_invalid"},
		{map[string]any{"schedule_type": "weekly_at", "time_of_day": "08:00", "day_of_week": "funday"}, "backend.scheduler.day_of_week_invalid"},
		{map[string]any{"schedule_type": "monthly_at", "time_of_day": "bad", "day_of_month": 1}, "backend.scheduler.time_of_day_invalid"},
		{map[string]any{"schedule_type": "monthly_at", "time_of_day": "08:00", "day_of_month": 0}, "backend.scheduler.day_of_month_invalid"},
		{map[string]any{"schedule_type": "monthly_at", "time_of_day": "08:00", "day_of_month": 32}, "backend.scheduler.day_of_month_invalid"},
		{map[string]any{"schedule_type": "cron", "schedule_expression": "not cron"}, "backend.scheduler.cron_invalid"},
		{map[string]any{"schedule_type": "unsupported", "schedule_expression": "daily"}, "backend.scheduler.schedule_type_invalid"},
	}
	for _, test := range invalid {
		data := validSchedulerDefinition()
		for key, value := range test.values {
			data[key] = value
		}
		assertSchedulerCode(t, data, test.code)
	}
}

func TestSchedulerValidationErrorParams(t *testing.T) {
	err := badRequest("code", " field ", "value", "", "ignored", "orphan")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Params["field"] != "value" || len(appErr.Params) != 1 {
		t.Fatalf("error=%#v", err)
	}
}

func validSchedulerDefinition() map[string]any {
	return map[string]any{
		"target_type": "workflow", "target_key": "scheduled:daily", "timezone": "UTC",
		"schedule_type": "interval", "interval_seconds": 60, "max_attempts": 3, "timeout_seconds": 300,
	}
}

func assertSchedulerCode(t *testing.T, data map[string]any, code string) {
	t.Helper()
	if err := SchedulerValidateDefinitionContract(t.Context(), data); apperror.CodeOf(err) != code {
		t.Fatalf("data=%#v error=%v code=%s want=%s", data, err, apperror.CodeOf(err), code)
	}
}
