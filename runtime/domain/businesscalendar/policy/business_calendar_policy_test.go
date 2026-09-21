package policy

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
)

func testCalendar() businesscalendarmodel.BusinessCalendarSchema {
	return businesscalendarmodel.BusinessCalendarSchema{
		Key: "cn_ops", Name: "China operations", Revision: "2026.2", Timezone: "Asia/Shanghai",
		WeeklyWorkingIntervals: []businesscalendarmodel.BusinessCalendarWeeklySchedule{
			{Weekday: "monday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "12:00"}, {Start: "13:00", End: "18:00"}}},
			{Weekday: "tuesday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "18:00"}}},
			{Weekday: "wednesday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "18:00"}}},
			{Weekday: "thursday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "18:00"}}},
			{Weekday: "friday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "18:00"}}},
		},
		Holidays: []string{"2026-10-01", "2026-10-02"},
		DateExceptions: []businesscalendarmodel.BusinessCalendarDateException{
			{Date: "2026-10-03", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "10:00", End: "16:00"}}},
			{Date: "2026-10-05", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{}},
		},
	}
}

func TestBusinessCalendarValidationIsClosedAndBounded(t *testing.T) {
	if err := Validate(testCalendar()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*businesscalendarmodel.BusinessCalendarSchema)
		code string
	}{
		{"identity", func(value *businesscalendarmodel.BusinessCalendarSchema) { value.Revision = "" }, CodeIdentityRequired},
		{"timezone", func(value *businesscalendarmodel.BusinessCalendarSchema) { value.Timezone = "Mars/Olympus" }, CodeTimezoneInvalid},
		{"weekday duplicate", func(value *businesscalendarmodel.BusinessCalendarSchema) {
			value.WeeklyWorkingIntervals = append(value.WeeklyWorkingIntervals, value.WeeklyWorkingIntervals[0])
		}, CodeWeeklyScheduleInvalid},
		{"overlap", func(value *businesscalendarmodel.BusinessCalendarSchema) {
			value.WeeklyWorkingIntervals[0].Intervals[1].Start = "11:00"
		}, CodeIntervalInvalid},
		{"date", func(value *businesscalendarmodel.BusinessCalendarSchema) { value.Holidays[0] = "2026-02-30" }, CodeDateInvalid},
		{"date duplicate", func(value *businesscalendarmodel.BusinessCalendarSchema) {
			value.DateExceptions[0].Date = value.Holidays[0]
		}, CodeDateDuplicate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := testCalendar()
			test.edit(&value)
			if err := Validate(value); ValidationCode(err) != test.code {
				t.Fatalf("error=%v code=%s", err, ValidationCode(err))
			}
		})
	}
}

func TestBusinessCalendarAddsWorkingDurationAcrossLunchHolidayAndException(t *testing.T) {
	location, _ := time.LoadLocation("Asia/Shanghai")
	base := time.Date(2026, 9, 30, 17, 0, 0, 0, location)
	got, err := AddBusinessDuration(t.Context(), testCalendar(), base, 3*time.Hour)
	want := time.Date(2026, 10, 3, 12, 0, 0, 0, location)
	if err != nil || !got.Equal(want) {
		t.Fatalf("forward=%v want=%v err=%v", got, want, err)
	}
	backward, err := AddBusinessDuration(t.Context(), testCalendar(), want, -3*time.Hour)
	if err != nil || !backward.Equal(base) {
		t.Fatalf("backward=%v want=%v err=%v", backward, base, err)
	}
	zero, err := AddBusinessDuration(t.Context(), testCalendar(), base, 0)
	if err != nil || !zero.Equal(base) {
		t.Fatalf("zero=%v err=%v", zero, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := AddBusinessDuration(cancelled, testCalendar(), base, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled err=%v", err)
	}
}

func TestBusinessCalendarUsesWallClockIntervalsAcrossDST(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	calendar := businesscalendarmodel.BusinessCalendarSchema{
		Key: "ny_ops", Name: "New York operations", Revision: "1", Timezone: location.String(),
		WeeklyWorkingIntervals: []businesscalendarmodel.BusinessCalendarWeeklySchedule{
			{Weekday: "sunday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "17:00"}}},
		},
	}
	for _, test := range []struct {
		name string
		base time.Time
		want time.Time
	}{
		{name: "spring forward", base: time.Date(2026, 3, 8, 8, 0, 0, 0, location), want: time.Date(2026, 3, 8, 10, 0, 0, 0, location)},
		{name: "fall back", base: time.Date(2026, 11, 1, 8, 0, 0, 0, location), want: time.Date(2026, 11, 1, 10, 0, 0, 0, location)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, addErr := AddBusinessDuration(t.Context(), calendar, test.base, time.Hour)
			if addErr != nil || !got.Equal(test.want) {
				t.Fatalf("got=%v want=%v err=%v", got, test.want, addErr)
			}
		})
	}
}

func TestBusinessCalendarExhaustsClosedFutureAndMinimumDuration(t *testing.T) {
	calendar := businesscalendarmodel.BusinessCalendarSchema{
		Key: "closed", Name: "Closed", Revision: "1", Timezone: "UTC",
		WeeklyWorkingIntervals: []businesscalendarmodel.BusinessCalendarWeeklySchedule{
			{Weekday: "monday", Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "10:00"}}},
		},
	}
	base := time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)
	for _, offset := range []time.Duration{time.Duration(math.MinInt64), 11 * 365 * 24 * time.Hour} {
		if _, err := AddBusinessDuration(t.Context(), calendar, base, offset); ValidationCode(err) != CodeSearchExhausted {
			t.Fatalf("offset=%v err=%v", offset, err)
		}
	}
}
