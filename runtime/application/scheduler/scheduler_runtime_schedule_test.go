package scheduler

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	schedulervalidation "github.com/domainry/domainry-runtime/runtime/domain/scheduler/validation"

	"strings"
	"testing"
	"time"

	apperror "github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestRunIDUsesStableScheduleWindows(t *testing.T) {
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "scheduler_test", Data: map[string]any{"key": "scheduler_test", "schedule_expression": "hourly"}}
	first := RunIDForDefinition(definition, "scheduler", now)
	second := RunIDForDefinition(definition, "scheduler", now.Add(20*time.Minute))
	nextWindow := RunIDForDefinition(definition, "scheduler", now.Add(time.Hour))
	if first != second {
		t.Fatalf("expected same hourly window to reuse run id, got %q and %q", first, second)
	}
	if first == nextWindow {
		t.Fatalf("expected next hourly window to use a different run id, got %q", nextWindow)
	}
	if manualA, manualB := RunIDForDefinition(definition, "manual_run", now), RunIDForDefinition(definition, "manual_run", now.Add(time.Nanosecond)); manualA == manualB {
		t.Fatalf("expected manual scheduler runs to get unique run ids, got %q", manualA)
	}
}

func TestRunIDDefaultsToDailyWindow(t *testing.T) {
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	definition := recordmodel.Record{ID: "scheduler_daily", Data: map[string]any{"key": "scheduler_daily"}}
	first := RunIDForDefinition(definition, "scheduler", now)
	if sameDay := RunIDForDefinition(definition, "scheduler", now.Add(6*time.Hour)); first != sameDay {
		t.Fatalf("expected default daily window to reuse run id within a day, got %q and %q", first, sameDay)
	}
	if nextDay := RunIDForDefinition(definition, "scheduler", now.Add(24*time.Hour)); first == nextDay {
		t.Fatalf("expected next daily window to use a different run id, got %q", nextDay)
	}
}

func TestScheduleNextRunAtCalendarVariants(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		data map[string]any
		want time.Time
	}{
		{"interval", time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC), map[string]any{"schedule_type": "interval", "interval_minutes": 15}, time.Date(2026, 7, 9, 16, 45, 0, 0, time.UTC)},
		{"daily timezone", time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC), map[string]any{"schedule_type": "daily_at", "timezone": "Asia/Shanghai", "time_of_day": "01:00"}, time.Date(2026, 7, 9, 17, 0, 0, 0, time.UTC)},
		{"weekly", time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC), map[string]any{"schedule_type": "weekly_at", "timezone": "UTC", "day_of_week": "friday", "time_of_day": "08:00"}, time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)},
		{"month end", time.Date(2026, 2, 20, 10, 0, 0, 0, time.UTC), map[string]any{"schedule_type": "monthly_at", "timezone": "UTC", "day_of_month": 31, "time_of_day": "09:00"}, time.Date(2026, 2, 28, 9, 0, 0, 0, time.UTC)},
		{"cron", time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC), map[string]any{"schedule_type": "cron", "schedule_expression": "*/15 * * * *", "timezone": "UTC"}, time.Date(2026, 7, 9, 16, 45, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := schedulerpolicy.SchedulerScheduleNextRunAt(recordmodel.Record{Data: tt.data}, tt.now); !got.Equal(tt.want) {
				t.Fatalf("next run = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDailyAtSpringForwardDSTStaysMonotonic(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load New York location: %v", err)
	}
	now := time.Date(2026, 3, 8, 1, 30, 0, 0, loc)
	definition := recordmodel.Record{Data: map[string]any{"schedule_type": "daily_at", "timezone": "America/New_York", "time_of_day": "02:30"}}
	next := schedulerpolicy.SchedulerScheduleNextRunAt(definition, now)
	if !next.After(now.UTC()) {
		t.Fatalf("expected DST next run to stay after now=%s, got %s", now.UTC(), next)
	}
	if following := schedulerpolicy.SchedulerScheduleNextRunAt(definition, next); !following.After(next) {
		t.Fatalf("expected following run to advance after %s, got %s", next, following)
	}
}

func TestBoundedCatchupUsesRecentMissedWindows(t *testing.T) {
	dueAt := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	definition := recordmodel.Record{Data: map[string]any{"key": "daily_catchup", "schedule_expression": "daily", "next_run_at": dueAt.Format(time.RFC3339), "missed_window_policy": "catch_up_bounded", "max_catchup_windows": 3}}
	scheduledFor := ScheduledRunTime(definition, "scheduler", now, 10)
	want := time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC)
	if !scheduledFor.Equal(want) {
		t.Fatalf("expected bounded catch-up to start at %s, got %s", want, scheduledFor)
	}
	if runID := RunIDForDefinition(definition, "scheduler", scheduledFor); !strings.HasSuffix(runID, "_20260707") {
		t.Fatalf("expected catch-up run id to use scheduled window, got %s", runID)
	}
}

func TestValidateDefinitionData(t *testing.T) {
	base := map[string]any{"target_type": "workflow", "target_key": "scheduled:*", "schedule_expression": "daily", "timezone": "UTC", "max_attempts": 3, "timeout_seconds": 300}
	invalidCron := recordvalidation.RecordCloneData(base)
	invalidCron["schedule_type"] = "cron"
	invalidCron["schedule_expression"] = "bad cron"
	if err := schedulervalidation.SchedulerValidateDefinitionContract(t.Context(), invalidCron); apperror.CodeOf(err) != "backend.scheduler.cron_invalid" {
		t.Fatalf("expected invalid cron error, got %v", err)
	}
	unsupported := recordvalidation.RecordCloneData(base)
	unsupported["target_type"] = "notification"
	if err := schedulervalidation.SchedulerValidateDefinitionContract(t.Context(), unsupported); apperror.CodeOf(err) != "backend.scheduler.target_type_unsupported" {
		t.Fatalf("expected unsupported target error, got %v", err)
	}
	valid := recordvalidation.RecordCloneData(base)
	valid["schedule_type"] = "daily_at"
	valid["time_of_day"] = "09:30"
	valid["timezone"] = "Asia/Shanghai"
	delete(valid, "schedule_expression")
	if err := schedulervalidation.SchedulerValidateDefinitionContract(t.Context(), valid); err != nil {
		t.Fatalf("expected valid daily_at definition, got %v", err)
	}
}

func TestNextRetryAtSupportsCappedExponentialBackoff(t *testing.T) {
	now := time.Date(2026, 7, 9, 16, 30, 0, 0, time.UTC)
	run := recordmodel.Record{Data: map[string]any{"retry_backoff": "capped_exponential", "retry_delay_seconds": 30, "retry_max_delay_seconds": 100}}
	if got, want := NextRetryAt(run, 4, now), now.Add(100*time.Second); !got.Equal(want) {
		t.Fatalf("expected capped exponential retry %s, got %s", want, got)
	}
}
