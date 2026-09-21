package policy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	businesscalendarmodel "github.com/domainry/domainry-runtime/runtime/domain/businesscalendar/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimermodel "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/model"
)

type recordTimerCalendarProbe struct{ called bool }

func (p *recordTimerCalendarProbe) Resolve(_ context.Context, key string) (businesscalendarmodel.BusinessCalendarSchema, error) {
	p.called = key == "weekday"
	return weekdayCalendar(), nil
}

type recordTimerErrorCalendar struct{ err error }

func (c recordTimerErrorCalendar) Resolve(context.Context, string) (businesscalendarmodel.BusinessCalendarSchema, error) {
	return businesscalendarmodel.BusinessCalendarSchema{}, c.err
}

func weekdayCalendar() businesscalendarmodel.BusinessCalendarSchema {
	week := []businesscalendarmodel.BusinessCalendarWeeklySchedule{}
	for _, weekday := range []string{"monday", "tuesday", "wednesday", "thursday", "friday"} {
		week = append(week, businesscalendarmodel.BusinessCalendarWeeklySchedule{Weekday: weekday, Intervals: []businesscalendarmodel.BusinessCalendarTimeInterval{{Start: "09:00", End: "18:00"}}})
	}
	return businesscalendarmodel.BusinessCalendarSchema{Key: "weekday", Name: "Weekday", Revision: "2026.1", Timezone: "Asia/Shanghai", WeeklyWorkingIntervals: week}
}

func TestRecordTimerSchedulePolicyOwnsDefaultsAndValidation(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	schedule := NormalizeSchedule(recordtimermodel.Schedule{
		TimerKey: " timer ", ObjectKey: " order ", RecordID: " one ", Purpose: " expiry ", DueAt: now,
		TargetType: " action ", TargetKey: " expire ",
	})
	if schedule.TimerKey != "timer" || schedule.ScheduleMode != "absolute" || schedule.Timezone != "UTC" || schedule.PayloadJSON != "{}" || schedule.MaxAttempts != 10 {
		t.Fatalf("normalized schedule=%+v", schedule)
	}
	if err := ValidateSchedule(schedule); err != nil {
		t.Fatal(err)
	}
	schedule.TargetType = "unknown"
	if err := ValidateSchedule(schedule); apperror.CodeOf(err) != "backend.record_timer.target_invalid" {
		t.Fatalf("invalid target error=%v", err)
	}
}

func TestRecordTimerScheduleRejectsInvalidOrUnboundedPayloadBeforePersistence(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	valid := NormalizeSchedule(recordtimermodel.Schedule{
		TimerKey: "timer", ObjectKey: "order", RecordID: "one", Purpose: "expiry", DueAt: now,
		TargetType: "action", TargetKey: "expire",
	})
	for _, payload := range []string{"[]", "null", "{", `{"value":"` + strings.Repeat("x", MaximumPayloadJSONBytes) + `"}`} {
		candidate := valid
		candidate.PayloadJSON = payload
		if err := ValidateSchedule(candidate); apperror.CodeOf(err) != "backend.record_timer.payload_invalid" {
			t.Fatalf("payload length=%d code=%q err=%v", len(payload), apperror.CodeOf(err), err)
		}
	}
	exact := valid
	exact.PayloadJSON = `{"value":"` + strings.Repeat("x", MaximumPayloadJSONBytes-len(`{"value":""}`)) + `"}`
	if len(exact.PayloadJSON) != MaximumPayloadJSONBytes {
		t.Fatalf("test payload length=%d", len(exact.PayloadJSON))
	}
	if err := ValidateSchedule(exact); err != nil {
		t.Fatalf("exact maximum payload rejected: %v", err)
	}
}

func TestRecordTimerScheduleResolutionBelongsToDomainPolicy(t *testing.T) {
	base := time.Date(2026, 7, 24, 9, 30, 0, 0, time.UTC)
	source := recordmodel.Record{Data: map[string]any{"starts_at": base.Format(time.RFC3339Nano)}}
	absolute, err := ResolveSchedule(t.Context(), recordtimermodel.Schedule{ScheduleMode: "absolute", DueAt: base, Timezone: "UTC"}, source, nil)
	if err != nil || !absolute.DueAt.Equal(base) {
		t.Fatalf("absolute schedule=%#v err=%v", absolute, err)
	}
	relative, err := ResolveSchedule(t.Context(), recordtimermodel.Schedule{ScheduleMode: "relative_field", SourceField: "starts_at", OffsetSeconds: -1800, Timezone: "UTC"}, source, nil)
	if err != nil || !relative.DueAt.Equal(base.Add(-30*time.Minute)) {
		t.Fatalf("relative schedule=%#v err=%v", relative, err)
	}
	calendar := &recordTimerCalendarProbe{}
	business, err := ResolveSchedule(t.Context(), recordtimermodel.Schedule{ScheduleMode: "business_calendar", SourceField: "starts_at", OffsetSeconds: 3600, Timezone: "Asia/Shanghai", BusinessCalendarKey: "weekday"}, source, calendar)
	wantBusiness := time.Date(2026, 7, 27, 9, 30, 0, 0, time.FixedZone("CST", 8*60*60))
	if err != nil || !calendar.called || !business.DueAt.Equal(wantBusiness) || business.BusinessCalendarRevision != "2026.1" {
		t.Fatalf("business schedule=%#v called=%v err=%v", business, calendar.called, err)
	}
	for _, request := range []recordtimermodel.Schedule{
		{ScheduleMode: "relative_field", SourceField: "missing", Timezone: "UTC"},
		{Timezone: "bad/zone"},
		{ScheduleMode: "business_calendar", DueAt: base, Timezone: "UTC", BusinessCalendarKey: "24x7", OffsetSeconds: 1},
	} {
		if _, err := ResolveSchedule(t.Context(), request, source, nil); err == nil {
			t.Fatalf("invalid schedule resolved: %+v", request)
		}
	}
	request := recordtimermodel.Schedule{ScheduleMode: "business_calendar", DueAt: base, BusinessCalendarKey: "weekday", OffsetSeconds: 1}
	if _, err := ResolveSchedule(t.Context(), request, source, recordTimerErrorCalendar{err: errors.New("calendar")}); err == nil {
		t.Fatal("calendar error accepted")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	catalog, err := NewBusinessCalendarCatalog([]businesscalendarmodel.BusinessCalendarSchema{weekdayCalendar()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSchedule(cancelled, request, source, catalog); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled schedule err=%v", err)
	}
}

func TestDeclarativeBusinessCalendarCatalogEdges(t *testing.T) {
	calendar, err := NewBusinessCalendarCatalog([]businesscalendarmodel.BusinessCalendarSchema{weekdayCalendar()})
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := calendar.Resolve(cancelled, "weekday"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled calendar err=%v", err)
	}
	if _, err := calendar.Resolve(t.Context(), "unknown"); err == nil {
		t.Fatal("unknown calendar accepted")
	}
	if _, err := NewBusinessCalendarCatalog([]businesscalendarmodel.BusinessCalendarSchema{weekdayCalendar(), weekdayCalendar()}); err == nil {
		t.Fatal("duplicate calendar accepted")
	}
}

func TestRecordTimerClaimAndRetryPoliciesAreFencedAndBounded(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	candidate := recordmodel.Record{ID: "timer-1", Data: map[string]any{
		"status": "leased", "lease_expires_at": now.Add(-time.Second).Format(time.RFC3339Nano),
		"fencing_token": 4, "attempt": 2, "retry_delay_seconds": 10, "retry_max_delay_seconds": 15,
	}}
	updated, lease, conditions, eligible := PrepareClaim(candidate, "worker-a", now, time.Minute)
	if !eligible || lease.Token != 5 || updated.Data["attempt"] != 3 || conditions["fencing_token"] != 4 {
		t.Fatalf("updated=%+v lease=%+v conditions=%+v eligible=%v", updated, lease, conditions, eligible)
	}
	if delay := RetryDelay(updated); delay != 15*time.Second {
		t.Fatalf("retry delay=%v", delay)
	}
	active := candidate
	active.Data = map[string]any{"status": "leased", "lease_expires_at": now.Add(time.Minute).Format(time.RFC3339Nano)}
	if _, _, _, eligible := PrepareClaim(active, "worker-b", now, time.Minute); eligible {
		t.Fatal("active lease was reclaimed")
	}
}
