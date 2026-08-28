package policy

import (
	"encoding/json"
	"testing"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func schedulerDefinition(data map[string]any) recordmodel.Record {
	return recordmodel.Record{Data: data}
}

func TestSchedulerScheduleTypeAndWindowSuffixMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 34, 56, 0, time.UTC)
	for _, scheduleType := range []string{"interval", "daily_at", "weekly_at", "monthly_at", "cron"} {
		definition := schedulerDefinition(map[string]any{"schedule_type": " " + scheduleType + " ", "interval_seconds": 60})
		if got := SchedulerScheduleType(definition); got != scheduleType {
			t.Fatalf("schedule type %q = %q", scheduleType, got)
		}
	}
	for _, test := range []struct {
		expression string
		want       string
	}{{"hourly", "legacy_hourly"}, {"@hourly", "legacy_hourly"}, {"weekly", "legacy_weekly"}, {"@weekly", "legacy_weekly"}, {"monthly", "legacy_monthly"}, {"@monthly", "legacy_monthly"}, {"daily", "legacy_daily"}, {"@daily", "legacy_daily"}, {"@midnight", "legacy_daily"}, {"", "legacy_daily"}, {"5m", "interval"}, {"@every 5m", "cron"}, {"0 1 * * *", "cron"}, {"invalid", "legacy_daily"}} {
		definition := schedulerDefinition(map[string]any{"schedule_expression": test.expression})
		if got := SchedulerScheduleType(definition); got != test.want {
			t.Fatalf("schedule expression %q = %q, want %q", test.expression, got, test.want)
		}
	}
	windowCases := []struct {
		data   map[string]any
		prefix string
	}{
		{map[string]any{"schedule_type": "interval", "interval_seconds": 60}, "interval60_"},
		{map[string]any{"schedule_type": "interval"}, "interval86400_"},
		{map[string]any{"schedule_type": "weekly_at"}, "2026W"},
		{map[string]any{"schedule_expression": "weekly"}, "2026W"},
		{map[string]any{"schedule_type": "monthly_at"}, "202607"},
		{map[string]any{"schedule_expression": "monthly"}, "202607"},
		{map[string]any{"schedule_type": "cron", "schedule_expression": "0 1 * * *"}, "202607191234"},
		{map[string]any{"schedule_expression": "hourly"}, "2026071912"},
		{map[string]any{"schedule_expression": "daily"}, "20260719"},
	}
	for _, test := range windowCases {
		got := SchedulerScheduleWindowSuffix(schedulerDefinition(test.data), now)
		if len(got) < len(test.prefix) || got[:len(test.prefix)] != test.prefix {
			t.Fatalf("window %#v = %q, want prefix %q", test.data, got, test.prefix)
		}
	}
}

func TestSchedulerScheduleNextRunAtMatrix(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 30, 0, 0, time.UTC)
	tests := []struct {
		name string
		data map[string]any
		want time.Time
	}{
		{name: "interval", data: map[string]any{"schedule_type": "interval", "interval_seconds": 30}, want: now.Add(30 * time.Second)},
		{name: "interval fallback", data: map[string]any{"schedule_type": "interval"}, want: now.Add(24 * time.Hour)},
		{name: "daily same day", data: map[string]any{"schedule_type": "daily_at", "time_of_day": "13:00"}, want: time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)},
		{name: "daily next day", data: map[string]any{"schedule_type": "daily_at", "time_of_day": "12:00"}, want: time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)},
		{name: "weekly", data: map[string]any{"schedule_type": "weekly_at", "weekday": "monday", "weekly_at": "09:00"}, want: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)},
		{name: "monthly current", data: map[string]any{"schedule_type": "monthly_at", "day_of_month": 20, "monthly_at": "09:00"}, want: time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)},
		{name: "monthly next", data: map[string]any{"schedule_type": "monthly_at", "day_of_month": 1, "monthly_at": "09:00"}, want: time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)},
		{name: "cron", data: map[string]any{"schedule_type": "cron", "schedule_expression": "0 13 * * *"}, want: time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)},
		{name: "invalid cron", data: map[string]any{"schedule_type": "cron", "schedule_expression": "invalid"}, want: now.AddDate(0, 0, 1)},
		{name: "hourly", data: map[string]any{"schedule_expression": "hourly"}, want: now.Add(time.Hour)},
		{name: "weekly legacy", data: map[string]any{"schedule_expression": "weekly"}, want: now.AddDate(0, 0, 7)},
		{name: "monthly legacy", data: map[string]any{"schedule_expression": "monthly"}, want: now.AddDate(0, 1, 0)},
		{name: "daily legacy", data: map[string]any{"schedule_expression": "daily"}, want: now.AddDate(0, 0, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := SchedulerScheduleNextRunAt(schedulerDefinition(test.data), now); !got.Equal(test.want) {
				t.Fatalf("next run = %s, want %s", got, test.want)
			}
		})
	}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	timezoneDefinition := schedulerDefinition(map[string]any{"schedule_type": "daily_at", "time_of_day": "21:00", "timezone": "Asia/Shanghai"})
	want := time.Date(2026, 7, 19, 21, 0, 0, 0, shanghai).UTC()
	if got := SchedulerScheduleNextRunAt(timezoneDefinition, now); !got.Equal(want) {
		t.Fatalf("timezone next run = %s, want %s", got, want)
	}
}

func TestSchedulerScheduleParsingHelpers(t *testing.T) {
	for _, test := range []struct {
		data map[string]any
		want int
	}{{map[string]any{"interval_seconds": 2}, 2}, {map[string]any{"interval_minutes": 2}, 120}, {map[string]any{"interval_hours": 2}, 7200}, {map[string]any{"schedule_expression": "hourly"}, 3600}, {map[string]any{"schedule_expression": "@hourly"}, 3600}, {map[string]any{"schedule_expression": "daily"}, 86400}, {map[string]any{"schedule_expression": "@daily"}, 86400}, {map[string]any{"schedule_expression": "@midnight"}, 86400}, {map[string]any{"schedule_expression": "weekly"}, 604800}, {map[string]any{"schedule_expression": "@weekly"}, 604800}, {map[string]any{"schedule_expression": "90s"}, 90}, {map[string]any{"schedule_expression": "-1s"}, 0}, {map[string]any{"schedule_expression": "invalid"}, 0}} {
		if got := SchedulerScheduleIntervalSeconds(schedulerDefinition(test.data)); got != test.want {
			t.Fatalf("interval %#v = %d, want %d", test.data, got, test.want)
		}
	}
	for _, test := range []struct {
		raw                  string
		hour, minute, second int
		ok                   bool
	}{{"13:14:15", 13, 14, 15, true}, {"13:14", 13, 14, 0, true}, {"", 0, 0, 0, false}, {"<nil>", 0, 0, 0, false}, {"13:14 UTC", 0, 0, 0, false}, {"25:00", 0, 0, 0, false}} {
		hour, minute, second, ok := SchedulerParseClock(test.raw)
		if hour != test.hour || minute != test.minute || second != test.second || ok != test.ok {
			t.Fatalf("clock %q = %02d:%02d:%02d/%v", test.raw, hour, minute, second, ok)
		}
	}
	clockDefinitions := []struct {
		data map[string]any
		want [3]int
	}{{map[string]any{"time_of_day": "01:02:03"}, [3]int{1, 2, 3}}, {map[string]any{"schedule_time": "02:03"}, [3]int{2, 3, 0}}, {map[string]any{"daily_at": "03:04"}, [3]int{3, 4, 0}}, {map[string]any{"weekly_at": "04:05"}, [3]int{4, 5, 0}}, {map[string]any{"monthly_at": "05:06"}, [3]int{5, 6, 0}}, {map[string]any{"schedule_expression": "06:07"}, [3]int{6, 7, 0}}, {map[string]any{}, [3]int{0, 0, 0}}}
	for _, test := range clockDefinitions {
		hour, minute, second := scheduleClock(schedulerDefinition(test.data))
		if [3]int{hour, minute, second} != test.want {
			t.Fatalf("schedule clock %#v = %v, want %v", test.data, [3]int{hour, minute, second}, test.want)
		}
	}
	weekdayCases := map[time.Weekday][]string{
		time.Sunday: {"0", "sun", "sunday"}, time.Monday: {"1", "mon", "monday"}, time.Tuesday: {"2", "tue", "tues", "tuesday"},
		time.Wednesday: {"3", "wed", "wednesday"}, time.Thursday: {"4", "thu", "thur", "thurs", "thursday"},
		time.Friday: {"5", "fri", "friday"}, time.Saturday: {"6", "sat", "saturday"},
	}
	for want, values := range weekdayCases {
		for _, raw := range values {
			got, ok := SchedulerParseWeekday(" " + raw + " ")
			if !ok || got != want {
				t.Fatalf("weekday %q = %s/%v, want %s", raw, got, ok, want)
			}
		}
	}
	if got, ok := SchedulerParseWeekday("invalid"); ok || got != time.Monday {
		t.Fatalf("invalid weekday = %s/%v", got, ok)
	}
	if scheduleWeekday(schedulerDefinition(map[string]any{})) != time.Monday || scheduleWeekday(schedulerDefinition(map[string]any{"day_of_week": "friday"})) != time.Friday {
		t.Fatal("schedule weekday fallback mismatch")
	}
	for _, test := range []struct {
		data map[string]any
		want int
	}{{map[string]any{"day_of_month": 12}, 12}, {map[string]any{"month_day": 15}, 15}, {map[string]any{"day_of_month": 40}, 31}, {map[string]any{"day_of_month": -1}, 1}, {map[string]any{}, 1}} {
		if got := scheduleMonthDay(schedulerDefinition(test.data)); got != test.want {
			t.Fatalf("month day %#v = %d, want %d", test.data, got, test.want)
		}
	}
}

func TestSchedulerCronLocationWallClockAndValueHelpers(t *testing.T) {
	if _, ok := SchedulerCronSchedule(schedulerDefinition(map[string]any{})); ok {
		t.Fatal("empty cron accepted")
	}
	if _, ok := SchedulerCronSchedule(schedulerDefinition(map[string]any{"schedule_expression": ""})); ok {
		t.Fatal("blank cron accepted")
	}
	if _, ok := SchedulerCronSchedule(schedulerDefinition(map[string]any{"schedule_expression": "invalid"})); ok {
		t.Fatal("invalid cron accepted")
	}
	if _, ok := SchedulerCronSchedule(schedulerDefinition(map[string]any{"schedule_expression": "@hourly"})); !ok {
		t.Fatal("descriptor cron rejected")
	}
	if scheduleLocation(schedulerDefinition(map[string]any{})) != time.UTC || scheduleLocation(schedulerDefinition(map[string]any{"timezone": ""})) != time.UTC || scheduleLocation(schedulerDefinition(map[string]any{"timezone": "invalid"})) != time.UTC {
		t.Fatal("timezone fallback mismatch")
	}
	if got := scheduleLocation(schedulerDefinition(map[string]any{"timezone": "Asia/Shanghai"})); got.String() != "Asia/Shanghai" {
		t.Fatalf("timezone = %s", got)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if got := nextDailyWallClock(now, time.UTC, 13, 0, 0); !got.Equal(time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("daily current = %s", got)
	}
	if got := nextWeeklyWallClock(now, time.UTC, time.Sunday, 13, 0, 0); !got.Equal(time.Date(2026, 7, 19, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("weekly same day = %s", got)
	}
	if got := nextWeeklyWallClock(now, time.UTC, time.Sunday, 11, 0, 0); !got.Equal(time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("weekly fallback = %s", got)
	}
	if got := nextWeeklyWallClock(now, time.UTC, time.Weekday(99), 11, 0, 0); !got.Equal(time.Date(2026, 7, 26, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("invalid weekday fallback = %s", got)
	}
	if got := monthlyWallClock(2026, time.February, time.UTC, 31, 1, 2, 3); got.Day() != 28 {
		t.Fatalf("monthly clamp high = %s", got)
	}
	if got := monthlyWallClock(2026, time.February, time.UTC, 0, 1, 2, 3); got.Day() != 1 {
		t.Fatalf("monthly clamp low = %s", got)
	}
	if got := nextMonthlyWallClock(now, time.UTC, 20, 1, 2, 3); !got.Equal(time.Date(2026, 7, 20, 1, 2, 3, 0, time.UTC)) {
		t.Fatalf("monthly current = %s", got)
	}
	if got := SchedulerFirstNonEmptyString("", " <nil> ", " value "); got != "value" || SchedulerFirstNonEmptyString("", " ") != "" {
		t.Fatal("first non-empty string mismatch")
	}
	for _, test := range []struct {
		value any
		want  int
	}{{1, 1}, {int64(2), 2}, {3.9, 3}, {json.Number("4"), 4}, {json.Number("bad"), 9}, {" 5 ", 5}, {"bad", 9}, {true, 9}} {
		if got := intValue(test.value, 9); got != test.want {
			t.Fatalf("int value %#v = %d, want %d", test.value, got, test.want)
		}
	}
}

func TestSchedulerScheduleIntervalSecondsAcceptsDecodedJSONNumber(t *testing.T) {
	definition := schedulerDefinition(map[string]any{"interval_seconds": json.Number("900")})
	if got := SchedulerScheduleIntervalSeconds(definition); got != 900 {
		t.Fatalf("interval seconds = %d, want 900", got)
	}
}
