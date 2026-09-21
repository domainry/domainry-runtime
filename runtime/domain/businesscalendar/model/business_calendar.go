package model

// BusinessCalendarSchema is one immutable, source-controlled calendar
// revision. Existing timers retain the revision that produced their due time;
// publishing a later revision never rewrites already-created timers.
type BusinessCalendarSchema struct {
	Key                    string                           `json:"key"`
	Name                   string                           `json:"name"`
	Revision               string                           `json:"revision"`
	Timezone               string                           `json:"timezone"`
	WeeklyWorkingIntervals []BusinessCalendarWeeklySchedule `json:"weekly_working_intervals"`
	Holidays               []string                         `json:"holidays,omitempty"`
	DateExceptions         []BusinessCalendarDateException  `json:"date_exceptions,omitempty"`
}

type BusinessCalendarWeeklySchedule struct {
	Weekday   string                         `json:"weekday"`
	Intervals []BusinessCalendarTimeInterval `json:"intervals"`
}

// BusinessCalendarDateException replaces the weekly schedule for one local
// date. An empty interval list closes the date; a non-empty list can open a
// weekend or define temporary working hours.
type BusinessCalendarDateException struct {
	Date      string                         `json:"date"`
	Intervals []BusinessCalendarTimeInterval `json:"intervals"`
}

// BusinessCalendarTimeInterval is a same-local-day half-open interval. End may
// be 24:00; Start must be between 00:00 and 23:59.
type BusinessCalendarTimeInterval struct {
	Start string `json:"start"`
	End   string `json:"end"`
}
