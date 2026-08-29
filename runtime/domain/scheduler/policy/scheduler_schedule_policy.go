package policy

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerschedule "github.com/domainry/domainry-scheduler-sdk/schedule"
	"github.com/robfig/cron/v3"
)

func SchedulerScheduleWindowSuffix(definition recordmodel.Record, now time.Time) string {
	return schedulerschedule.WindowSuffix(definition.Data, now)
}

func SchedulerScheduleNextRunAt(definition recordmodel.Record, now time.Time) time.Time {
	return schedulerschedule.Next(definition.Data, now)
}

func SchedulerScheduleType(definition recordmodel.Record) string {
	return schedulerschedule.Type(definition.Data)
}

func scheduleLocation(definition recordmodel.Record) *time.Location {
	timezone := strings.TrimSpace(fmt.Sprint(definition.Data["timezone"]))
	if timezone == "" || timezone == "<nil>" {
		return time.UTC
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

func SchedulerScheduleIntervalSeconds(definition recordmodel.Record) int {
	return schedulerschedule.IntervalSeconds(definition.Data)
}

func scheduleClock(definition recordmodel.Record) (int, int, int) {
	for _, field := range []string{"time_of_day", "schedule_time", "daily_at", "weekly_at", "monthly_at"} {
		if hour, minute, second, ok := SchedulerParseClock(fmt.Sprint(definition.Data[field])); ok {
			return hour, minute, second
		}
	}
	if hour, minute, second, ok := SchedulerParseClock(fmt.Sprint(definition.Data["schedule_expression"])); ok {
		return hour, minute, second
	}
	return 0, 0, 0
}

func SchedulerParseClock(raw string) (int, int, int, bool) {
	return schedulerschedule.ParseClock(raw)
}

func scheduleWeekday(definition recordmodel.Record) time.Weekday {
	raw := strings.ToLower(strings.TrimSpace(SchedulerFirstNonEmptyString(
		fmt.Sprint(definition.Data["day_of_week"]),
		fmt.Sprint(definition.Data["weekday"]),
		fmt.Sprint(definition.Data["week_day"]),
	)))
	weekday, ok := SchedulerParseWeekday(raw)
	if !ok {
		return time.Monday
	}
	return weekday
}

func SchedulerParseWeekday(raw string) (time.Weekday, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "sun", "sunday":
		return time.Sunday, true
	case "1", "mon", "monday":
		return time.Monday, true
	case "2", "tue", "tues", "tuesday":
		return time.Tuesday, true
	case "3", "wed", "wednesday":
		return time.Wednesday, true
	case "4", "thu", "thur", "thurs", "thursday":
		return time.Thursday, true
	case "5", "fri", "friday":
		return time.Friday, true
	case "6", "sat", "saturday":
		return time.Saturday, true
	default:
		return time.Monday, false
	}
}

func scheduleMonthDay(definition recordmodel.Record) int {
	for _, field := range []string{"day_of_month", "month_day"} {
		if day := intValue(definition.Data[field], 0); day > 0 {
			if day > 31 {
				return 31
			}
			return day
		}
	}
	return 1
}

func SchedulerCronSchedule(definition recordmodel.Record) (cron.Schedule, bool) {
	expression := strings.TrimSpace(fmt.Sprint(definition.Data["schedule_expression"]))
	if expression == "" || expression == "<nil>" {
		return nil, false
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	schedule, err := parser.Parse(expression)
	return schedule, err == nil
}

func nextDailyWallClock(now time.Time, loc *time.Location, hour int, minute int, second int) time.Time {
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, second, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}

func nextWeeklyWallClock(now time.Time, loc *time.Location, weekday time.Weekday, hour int, minute int, second int) time.Time {
	for offset := 0; offset <= 7; offset++ {
		day := now.AddDate(0, 0, offset)
		if day.Weekday() != weekday {
			continue
		}
		candidate := time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, loc)
		if candidate.After(now) {
			return candidate
		}
	}
	day := now.AddDate(0, 0, 7)
	return time.Date(day.Year(), day.Month(), day.Day(), hour, minute, second, 0, loc)
}

func nextMonthlyWallClock(now time.Time, loc *time.Location, day int, hour int, minute int, second int) time.Time {
	candidate := monthlyWallClock(now.Year(), now.Month(), loc, day, hour, minute, second)
	if !candidate.After(now) {
		nextMonth := now.AddDate(0, 1, 0)
		candidate = monthlyWallClock(nextMonth.Year(), nextMonth.Month(), loc, day, hour, minute, second)
	}
	return candidate
}

func monthlyWallClock(year int, month time.Month, loc *time.Location, day int, hour int, minute int, second int) time.Time {
	lastDay := time.Date(year, month+1, 0, hour, minute, second, 0, loc).Day()
	if day > lastDay {
		day = lastDay
	}
	if day <= 0 {
		day = 1
	}
	return time.Date(year, month, day, hour, minute, second, 0, loc)
}

func SchedulerFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" && trimmed != "<nil>" {
			return trimmed
		}
	}
	return ""
}

func intValue(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if value, err := typed.Int64(); err == nil {
			return int(value)
		}
	case string:
		var out int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &out); err == nil {
			return out
		}
	}
	return fallback
}
