package policy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
)

const (
	CodeIdentityRequired      = "backend.business_calendar.identity_required"
	CodeTimezoneInvalid       = "backend.business_calendar.timezone_invalid"
	CodeWeeklyScheduleInvalid = "backend.business_calendar.weekly_schedule_invalid"
	CodeDateInvalid           = "backend.business_calendar.date_invalid"
	CodeDateDuplicate         = "backend.business_calendar.date_duplicate"
	CodeIntervalInvalid       = "backend.business_calendar.interval_invalid"
	CodeSearchExhausted       = "backend.business_calendar.search_exhausted"
)

var businessCalendarKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]*$`)

type ValidationError struct {
	Code string
	Path string
}

func (e *ValidationError) Error() string {
	if strings.TrimSpace(e.Path) == "" {
		return e.Code
	}
	return e.Path + ": " + e.Code
}

func ValidationCode(err error) string {
	var validation *ValidationError
	if errors.As(err, &validation) {
		return validation.Code
	}
	return ""
}

func validationError(code, path string) error {
	return &ValidationError{Code: code, Path: path}
}

// Normalize returns a detached canonical definition used by validation,
// hashing, and timer execution.
func Normalize(value businesscalendarmodel.BusinessCalendarSchema) businesscalendarmodel.BusinessCalendarSchema {
	value.Key = strings.TrimSpace(value.Key)
	value.Name = strings.TrimSpace(value.Name)
	value.Revision = strings.TrimSpace(value.Revision)
	value.Timezone = strings.TrimSpace(value.Timezone)
	value.Holidays = append([]string(nil), value.Holidays...)
	value.WeeklyWorkingIntervals = append([]businesscalendarmodel.BusinessCalendarWeeklySchedule(nil), value.WeeklyWorkingIntervals...)
	for index := range value.WeeklyWorkingIntervals {
		value.WeeklyWorkingIntervals[index].Weekday = strings.ToLower(strings.TrimSpace(value.WeeklyWorkingIntervals[index].Weekday))
		value.WeeklyWorkingIntervals[index].Intervals = normalizeIntervals(value.WeeklyWorkingIntervals[index].Intervals)
	}
	value.DateExceptions = append([]businesscalendarmodel.BusinessCalendarDateException(nil), value.DateExceptions...)
	for index := range value.DateExceptions {
		value.DateExceptions[index].Date = strings.TrimSpace(value.DateExceptions[index].Date)
		value.DateExceptions[index].Intervals = normalizeIntervals(value.DateExceptions[index].Intervals)
	}
	for index := range value.Holidays {
		value.Holidays[index] = strings.TrimSpace(value.Holidays[index])
	}
	return value
}

func normalizeIntervals(values []businesscalendarmodel.BusinessCalendarTimeInterval) []businesscalendarmodel.BusinessCalendarTimeInterval {
	result := append([]businesscalendarmodel.BusinessCalendarTimeInterval(nil), values...)
	for index := range result {
		result[index].Start = strings.TrimSpace(result[index].Start)
		result[index].End = strings.TrimSpace(result[index].End)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Start < result[j].Start })
	return result
}

func Validate(value businesscalendarmodel.BusinessCalendarSchema) error {
	value = Normalize(value)
	if !businessCalendarKeyPattern.MatchString(value.Key) {
		return validationError(CodeIdentityRequired, "key")
	}
	if value.Name == "" {
		return validationError(CodeIdentityRequired, "name")
	}
	if value.Revision == "" {
		return validationError(CodeIdentityRequired, "revision")
	}
	if _, err := time.LoadLocation(value.Timezone); err != nil {
		return validationError(CodeTimezoneInvalid, "timezone")
	}
	if len(value.WeeklyWorkingIntervals) == 0 || len(value.WeeklyWorkingIntervals) > 7 {
		return validationError(CodeWeeklyScheduleInvalid, "weekly_working_intervals")
	}
	weekdays := map[string]bool{}
	for index, day := range value.WeeklyWorkingIntervals {
		path := fmt.Sprintf("weekly_working_intervals[%d]", index)
		if _, valid := parseWeekday(day.Weekday); !valid || weekdays[day.Weekday] {
			return validationError(CodeWeeklyScheduleInvalid, path+".weekday")
		}
		weekdays[day.Weekday] = true
		if len(day.Intervals) == 0 || len(day.Intervals) > 8 {
			return validationError(CodeWeeklyScheduleInvalid, path+".intervals")
		}
		if err := validateIntervals(day.Intervals, path+".intervals"); err != nil {
			return err
		}
	}
	if len(value.Holidays) > 366 || len(value.DateExceptions) > 366 {
		return validationError(CodeDateInvalid, "holidays")
	}
	dates := map[string]bool{}
	for index, date := range value.Holidays {
		if !validDate(date) {
			return validationError(CodeDateInvalid, fmt.Sprintf("holidays[%d]", index))
		}
		if dates[date] {
			return validationError(CodeDateDuplicate, fmt.Sprintf("holidays[%d]", index))
		}
		dates[date] = true
	}
	for index, exception := range value.DateExceptions {
		path := fmt.Sprintf("date_exceptions[%d]", index)
		if !validDate(exception.Date) {
			return validationError(CodeDateInvalid, path+".date")
		}
		if dates[exception.Date] {
			return validationError(CodeDateDuplicate, path+".date")
		}
		dates[exception.Date] = true
		if len(exception.Intervals) > 8 {
			return validationError(CodeIntervalInvalid, path+".intervals")
		}
		if err := validateIntervals(exception.Intervals, path+".intervals"); err != nil {
			return err
		}
	}
	return nil
}

func validateIntervals(values []businesscalendarmodel.BusinessCalendarTimeInterval, path string) error {
	previousEnd := -1
	for index, interval := range values {
		start, startOK := parseClock(interval.Start, false)
		end, endOK := parseClock(interval.End, true)
		if !startOK || !endOK || start >= end || start < previousEnd {
			return validationError(CodeIntervalInvalid, fmt.Sprintf("%s[%d]", path, index))
		}
		previousEnd = end
	}
	return nil
}

func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func parseClock(value string, allowEndOfDay bool) (int, bool) {
	if allowEndOfDay && value == "24:00" {
		return 24 * 60, true
	}
	parsed, err := time.Parse("15:04", value)
	if err != nil || parsed.Format("15:04") != value {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

func parseWeekday(value string) (time.Weekday, bool) {
	values := map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
		"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
	}
	weekday, valid := values[strings.ToLower(strings.TrimSpace(value))]
	return weekday, valid
}

// AddBusinessDuration adds elapsed working time using local, half-open
// intervals. It is deterministic for one immutable calendar revision and
// bounded to ten calendar years so a malformed or permanently closed future
// cannot spin forever.
func AddBusinessDuration(ctx context.Context, value businesscalendarmodel.BusinessCalendarSchema, base time.Time, offset time.Duration) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	value = Normalize(value)
	if err := Validate(value); err != nil {
		return time.Time{}, err
	}
	location, _ := time.LoadLocation(value.Timezone)
	current := base.In(location)
	if offset == 0 {
		return current, nil
	}
	remaining := uint64(offset)
	direction := 1
	if offset < 0 {
		direction = -1
		// Negating MinInt64 overflows time.Duration. Convert its magnitude
		// without first negating the minimum signed value.
		remaining = uint64(-(offset + 1)) + 1
	}
	for searchedDays := 0; searchedDays <= 3660; searchedDays++ {
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
		windows := workingWindows(value, current, location)
		if direction > 0 {
			for _, window := range windows {
				if !current.Before(window.end) {
					continue
				}
				if current.Before(window.start) {
					current = window.start
				}
				available := uint64(window.end.Sub(current))
				if remaining <= available {
					return current.Add(time.Duration(remaining)), nil
				}
				remaining -= available
				current = window.end
			}
			current = localDayStart(current, location).AddDate(0, 0, 1)
			continue
		}
		for index := len(windows) - 1; index >= 0; index-- {
			window := windows[index]
			if !current.After(window.start) {
				continue
			}
			if current.After(window.end) {
				current = window.end
			}
			available := uint64(current.Sub(window.start))
			if remaining <= available {
				return current.Add(-time.Duration(remaining)), nil
			}
			remaining -= available
			current = window.start
		}
		current = localDayStart(current, location).Add(-time.Nanosecond)
	}
	return time.Time{}, validationError(CodeSearchExhausted, "weekly_working_intervals")
}

type workingWindow struct{ start, end time.Time }

func workingWindows(value businesscalendarmodel.BusinessCalendarSchema, day time.Time, location *time.Location) []workingWindow {
	date := day.In(location).Format("2006-01-02")
	for _, exception := range value.DateExceptions {
		if exception.Date == date {
			return materializeWindows(day, exception.Intervals, location)
		}
	}
	for _, holiday := range value.Holidays {
		if holiday == date {
			return nil
		}
	}
	weekday := day.In(location).Weekday()
	for _, weekly := range value.WeeklyWorkingIntervals {
		candidate, _ := parseWeekday(weekly.Weekday)
		if candidate == weekday {
			return materializeWindows(day, weekly.Intervals, location)
		}
	}
	return nil
}

func materializeWindows(day time.Time, intervals []businesscalendarmodel.BusinessCalendarTimeInterval, location *time.Location) []workingWindow {
	local := day.In(location)
	result := make([]workingWindow, 0, len(intervals))
	for _, interval := range intervals {
		startMinutes, _ := parseClock(interval.Start, false)
		endMinutes, _ := parseClock(interval.End, true)
		start := time.Date(local.Year(), local.Month(), local.Day(), startMinutes/60, startMinutes%60, 0, 0, location)
		end := time.Date(local.Year(), local.Month(), local.Day(), endMinutes/60, endMinutes%60, 0, 0, location)
		if endMinutes == 24*60 {
			end = localDayStart(local, location).AddDate(0, 0, 1)
		}
		result = append(result, workingWindow{
			start: start,
			end:   end,
		})
	}
	return result
}

func localDayStart(value time.Time, location *time.Location) time.Time {
	local := value.In(location)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
}
