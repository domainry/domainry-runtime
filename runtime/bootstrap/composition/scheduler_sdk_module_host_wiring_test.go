package composition

import (
	"encoding/json"
	"testing"
	"time"

	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func TestSchedulerSDKDefinitionCarriesEveryHTTPCalendarAndPolicyField(t *testing.T) {
	definition := schedulerSDKDefinition(SchedulerPublishedDefinition{Key: "partner_sync", UpdatedAt: "2026-08-29T12:00:00Z", Data: map[string]any{
		"name": "Partner sync", "description": "Synchronize the primary partner", "i18n": map[string]any{"zh-CN": map[string]any{"name": "合作方同步"}}, "status": "enabled", "next_run_at": "2026-09-01T01:30:00.123Z",
		"schedule_type": "weekly_at", "time_of_day": "09:30", "day_of_week": "monday", "timezone": "Asia/Shanghai",
		"target_type": "http", "target_key": "sync", "connection_key": "partner_primary", "payload_json": `{"full":true}`,
		"missed_window_policy": "catch_up_bounded", "max_catchup_windows": 4, "max_attempts": 5, "timeout_seconds": 45, "retry_delay_seconds": 7, "retry_max_delay_seconds": 70,
	}}, nil)
	if definition.Schedule.TimeOfDay != "09:30" || definition.Schedule.DayOfWeek != "monday" {
		t.Fatalf("schedule=%#v", definition.Schedule)
	}
	if definition.Target.Type != "http" || definition.Target.ConnectionKey != "partner_primary" || definition.Target.Operation != "sync" || definition.Target.DispatchMode != "runtime_callback" {
		t.Fatalf("target=%#v", definition.Target)
	}
	if definition.Name != "Partner sync" || definition.Description != "Synchronize the primary partner" || definition.Status != "enabled" || string(definition.I18n["zh-CN"]) != `{"name":"合作方同步"}` {
		t.Fatalf("presentation metadata=%#v", definition)
	}
	wantNextRun, _ := time.Parse(time.RFC3339Nano, "2026-09-01T01:30:00.123Z")
	if !definition.InitialNextRunAt.Equal(wantNextRun) {
		t.Fatalf("next run=%v want=%v", definition.InitialNextRunAt, wantNextRun)
	}
	if definition.Policy.Misfire != "catch_up_bounded" || definition.Policy.MaxCatchupWindows != 4 || definition.Policy.MaxAttempts != 5 || definition.Policy.Timeout != 45*time.Second || definition.Policy.RetryInitial != 7*time.Second || definition.Policy.RetryMax != 70*time.Second {
		t.Fatalf("policy=%#v", definition.Policy)
	}
	if string(definition.Target.Payload) != `{"full":true}` {
		t.Fatalf("payload=%s", definition.Target.Payload)
	}
}

func TestSchedulerSDKDefinitionMapsRuntimeOwnerAndPublishedPolicyDefaults(t *testing.T) {
	definition := schedulerSDKDefinition(SchedulerPublishedDefinition{Key: "daily", Data: map[string]any{"name": "Daily", "status": "enabled", "schedule_type": "interval", "interval_seconds": 60, "target_type": "workflow", "target_key": "scheduled:daily"}}, nil)
	if definition.Target.Type != "runtime_operation" || definition.Target.Owner != "workflow" || definition.Target.Operation != "scheduled:daily" {
		t.Fatalf("target=%#v", definition.Target)
	}
	if len(definition.Target.Payload) != 0 {
		t.Fatalf("workflow payload must remain absent: %s", definition.Target.Payload)
	}
	if definition.Policy.Misfire != "skip" || definition.Policy.MaxAttempts != 1 || definition.Policy.Timeout != 300*time.Second || definition.Policy.RetryInitial != 30*time.Second || definition.Policy.RetryMax != 900*time.Second {
		t.Fatalf("defaults=%#v", definition.Policy)
	}
}

func TestSchedulerSDKDefinitionPreservesBusinessActionAuthorizationTarget(t *testing.T) {
	definition := schedulerSDKDefinition(SchedulerPublishedDefinition{Key: "expire-orders", UpdatedAt: "release-7", Data: map[string]any{
		"status": "enabled", "schedule_type": "interval", "interval_seconds": 60,
		"target_type": "business_action", "target_key": "order.expire", "target_object": "order", "run_as_role": "order_automation", "payload_json": `{"status":"expired"}`,
	}}, nil)
	if definition.Target.Type != "runtime_operation" || definition.Target.Owner != "business_action" || definition.Target.Operation != "order.expire" || definition.Target.ObjectKey != "order" || definition.Target.RunAsRole != "order_automation" || string(definition.Target.Payload) != `{"status":"expired"}` {
		t.Fatalf("target=%#v", definition.Target)
	}
}

func TestSchedulerSDKDefinitionMapsEveryScheduleVariant(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want func(scheduleType string, expression string, interval int, timeOfDay string, dayOfWeek string, dayOfMonth int) bool
	}{
		{name: "cron", data: map[string]any{"schedule_type": "cron", "schedule_expression": "0 3 * * *"}, want: func(kind, expression string, _ int, _, _ string, _ int) bool {
			return kind == "cron" && expression == "0 3 * * *"
		}},
		{name: "daily", data: map[string]any{"schedule_type": "daily_at", "time_of_day": "03:00"}, want: func(kind, _ string, _ int, timeOfDay, _ string, _ int) bool {
			return kind == "daily_at" && timeOfDay == "03:00"
		}},
		{name: "monthly", data: map[string]any{"schedule_type": "monthly_at", "time_of_day": "04:00", "day_of_month": 12}, want: func(kind, _ string, _ int, timeOfDay, _ string, day int) bool {
			return kind == "monthly_at" && timeOfDay == "04:00" && day == 12
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := map[string]any{"name": test.name, "status": "enabled", "target_type": "report_snapshot_refresh", "target_key": "report", "max_attempts": 1, "timeout_seconds": 300, "timezone": "UTC"}
			for key, value := range test.data {
				data[key] = value
			}
			definition := schedulerSDKDefinition(SchedulerPublishedDefinition{Key: test.name, Data: data}, nil)
			schedule := definition.Schedule
			if !test.want(schedule.Type, schedule.Expression, schedule.IntervalSeconds, schedule.TimeOfDay, schedule.DayOfWeek, schedule.DayOfMonth) {
				t.Fatalf("schedule=%#v", schedule)
			}
		})
	}
}

func TestSchedulerSDKDefinitionEmbedsImmutableBusinessCalendarProtocol(t *testing.T) {
	calendar := businesscalendarmodel.BusinessCalendarSchema{
		Key: "cn_operations", Revision: "2026.09", Timezone: "Asia/Shanghai",
		WeeklyWorkingIntervals: []businesscalendarmodel.BusinessCalendarWeeklySchedule{{Weekday: "monday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "18:00"}}}},
		Holidays:               []string{"2026-10-01"},
		DateExceptions:         []businesscalendarmodel.BusinessCalendarDateException{{Date: "2026-10-10", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "10:00", End: "16:00"}}}},
	}
	definition := schedulerSDKDefinition(SchedulerPublishedDefinition{Key: "daily", Data: map[string]any{
		"name": "Daily", "status": "enabled", "schedule_type": "daily_at", "time_of_day": "09:00", "timezone": "Asia/Shanghai",
		"business_calendar_key": "cn_operations", "non_working_day_policy": "roll_forward", "target_type": "workflow", "target_key": "scheduled:daily",
	}}, map[string]businesscalendarmodel.BusinessCalendarSchema{calendar.Key: calendar})
	if definition.Schedule.NonWorkingDayPolicy != schedulersdk.NonWorkingDayRollForward || definition.Schedule.BusinessCalendar == nil {
		t.Fatalf("schedule=%#v", definition.Schedule)
	}
	snapshot := definition.Schedule.BusinessCalendar
	if snapshot.Key != calendar.Key || snapshot.Revision != calendar.Revision || snapshot.Timezone != calendar.Timezone || len(snapshot.WeeklyWorkingIntervals) != 1 || len(snapshot.Holidays) != 1 || len(snapshot.DateExceptions) != 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestSchedulerSDKI18nIgnoresNonObjectDefenseInDepth(t *testing.T) {
	if got := schedulerSDKI18n(map[string]any{"i18n": "invalid"}); got != nil {
		t.Fatalf("i18n=%#v", got)
	}
	if got := schedulerSDKI18n(map[string]any{"i18n": map[string]json.RawMessage{"en": json.RawMessage(`{"name":"Daily"}`)}}); string(got["en"]) != `{"name":"Daily"}` {
		t.Fatalf("i18n=%#v", got)
	}
}
