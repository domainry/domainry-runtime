package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimermodel "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/model"
)

type recordTimerCalendarProbe struct{ called bool }

func (p *recordTimerCalendarProbe) AddBusinessDuration(_ context.Context, key string, base time.Time, offset time.Duration, location *time.Location) (time.Time, error) {
	p.called = key == "weekday" && location.String() == "Asia/Shanghai"
	return base.Add(offset + 24*time.Hour), nil
}

type recordTimerErrorCalendar struct{ err error }

func (c recordTimerErrorCalendar) AddBusinessDuration(context.Context, string, time.Time, time.Duration, *time.Location) (time.Time, error) {
	return time.Time{}, c.err
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

func TestRecordTimerScheduleResolutionBelongsToDomainPolicy(t *testing.T) {
	base := time.Date(2026, 7, 21, 9, 30, 0, 0, time.UTC)
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
	if err != nil || !calendar.called || !business.DueAt.Equal(base.Add(25*time.Hour)) {
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
	request := recordtimermodel.Schedule{ScheduleMode: "business_calendar", DueAt: base, Timezone: "UTC", BusinessCalendarKey: "24x7", OffsetSeconds: 1}
	if _, err := ResolveSchedule(t.Context(), request, source, recordTimerErrorCalendar{err: errors.New("calendar")}); err == nil {
		t.Fatal("calendar error accepted")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ResolveSchedule(cancelled, request, source, StandardBusinessCalendar{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled schedule err=%v", err)
	}
}

func TestStandardBusinessCalendarEdges(t *testing.T) {
	calendar := StandardBusinessCalendar{}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := calendar.AddBusinessDuration(cancelled, "24x7", time.Now(), time.Hour, time.UTC); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled calendar err=%v", err)
	}
	base := time.Date(2026, 7, 24, 23, 0, 0, 0, time.UTC)
	if got, err := calendar.AddBusinessDuration(t.Context(), "24x7", base, 30*time.Minute, time.UTC); err != nil || !got.Equal(base.Add(30*time.Minute)) {
		t.Fatalf("24x7=%v err=%v", got, err)
	}
	if _, err := calendar.AddBusinessDuration(t.Context(), "unknown", base, time.Hour, time.UTC); err == nil {
		t.Fatal("unknown calendar accepted")
	}
	forward, err := calendar.AddBusinessDuration(t.Context(), "weekday", base, 90*time.Minute, time.UTC)
	if err != nil || forward.Weekday() != time.Monday || forward.Hour() != 0 || forward.Minute() != 30 {
		t.Fatalf("weekday forward=%v err=%v", forward, err)
	}
	backward, err := calendar.AddBusinessDuration(t.Context(), "weekday", time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC), -2*time.Hour, time.UTC)
	if err != nil || backward.Weekday() != time.Friday || backward.Hour() != 23 {
		t.Fatalf("weekday backward=%v err=%v", backward, err)
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
