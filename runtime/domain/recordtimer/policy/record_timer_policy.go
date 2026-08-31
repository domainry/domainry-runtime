package policy

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimermodel "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/model"
)

type BusinessCalendar interface {
	AddBusinessDuration(context.Context, string, time.Time, time.Duration, *time.Location) (time.Time, error)
}

type StandardBusinessCalendar struct{}

func (StandardBusinessCalendar) AddBusinessDuration(ctx context.Context, key string, base time.Time, offset time.Duration, _ *time.Location) (time.Time, error) {
	if err := ctx.Err(); err != nil {
		return time.Time{}, err
	}
	switch strings.TrimSpace(key) {
	case "24x7":
		return base.Add(offset), nil
	case "weekday":
	default:
		return time.Time{}, fmt.Errorf("unknown business calendar %q", key)
	}
	direction := time.Duration(1)
	if offset < 0 {
		direction = -1
		offset = -offset
	}
	current := base
	for offset > 0 {
		step := time.Hour
		if offset < step {
			step = offset
		}
		candidate := current.Add(direction * step)
		if candidate.Weekday() != time.Saturday && candidate.Weekday() != time.Sunday {
			offset -= step
		}
		current = candidate
	}
	return current, nil
}

func ResolveSchedule(ctx context.Context, request recordtimermodel.Schedule, source recordmodel.Record, calendar BusinessCalendar) (recordtimermodel.Schedule, error) {
	request = NormalizeSchedule(request)
	location, err := time.LoadLocation(request.Timezone)
	if err != nil {
		return recordtimermodel.Schedule{}, policyError("backend.record_timer.timezone_invalid", err)
	}
	base := request.DueAt
	if request.ScheduleMode == "relative_field" || request.ScheduleMode == "business_calendar" && request.SourceField != "" {
		raw := strings.TrimSpace(fmt.Sprint(source.Data[request.SourceField]))
		parsed, parseErr := time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			return recordtimermodel.Schedule{}, policyError("backend.record_timer.source_datetime_invalid", parseErr)
		}
		base = parsed
	}
	switch request.ScheduleMode {
	case "absolute":
	case "relative_field":
		request.DueAt = base.Add(time.Duration(request.OffsetSeconds) * time.Second)
	case "business_calendar":
		if calendar == nil {
			return recordtimermodel.Schedule{}, policyError("backend.record_timer.business_calendar_required", nil)
		}
		request.DueAt, err = calendar.AddBusinessDuration(ctx, request.BusinessCalendarKey, base.In(location), time.Duration(request.OffsetSeconds)*time.Second, location)
		if err != nil {
			return recordtimermodel.Schedule{}, policyError("backend.record_timer.business_calendar_invalid", err)
		}
	}
	return request, nil
}

func NormalizeSchedule(request recordtimermodel.Schedule) recordtimermodel.Schedule {
	request.TimerKey, request.ObjectKey, request.RecordID = strings.TrimSpace(request.TimerKey), strings.TrimSpace(request.ObjectKey), strings.TrimSpace(request.RecordID)
	request.Purpose, request.ScheduleMode = strings.TrimSpace(request.Purpose), strings.TrimSpace(request.ScheduleMode)
	request.SourceField, request.Timezone, request.BusinessCalendarKey = strings.TrimSpace(request.SourceField), strings.TrimSpace(request.Timezone), strings.TrimSpace(request.BusinessCalendarKey)
	request.TargetType, request.TargetKey = strings.TrimSpace(request.TargetType), strings.TrimSpace(request.TargetKey)
	request.PayloadJSON, request.SupersedesTimerID = strings.TrimSpace(request.PayloadJSON), strings.TrimSpace(request.SupersedesTimerID)
	if request.ScheduleMode == "" {
		request.ScheduleMode = "absolute"
	}
	if request.Timezone == "" {
		request.Timezone = "UTC"
	}
	if request.PayloadJSON == "" {
		request.PayloadJSON = "{}"
	}
	if request.MaxAttempts == 0 {
		request.MaxAttempts = 10
	}
	if request.RetryDelaySeconds == 0 {
		request.RetryDelaySeconds = 1
	}
	if request.RetryMaxDelaySeconds == 0 {
		request.RetryMaxDelaySeconds = 60
	}
	return request
}

func ValidateSchedule(request recordtimermodel.Schedule) error {
	if request.TimerKey == "" || request.ObjectKey == "" || request.RecordID == "" || request.Purpose == "" || request.TargetKey == "" || request.DueAt.IsZero() {
		return policyError("backend.record_timer.required", nil)
	}
	if request.ScheduleMode != "absolute" && request.ScheduleMode != "relative_field" && request.ScheduleMode != "business_calendar" {
		return policyError("backend.record_timer.schedule_mode_invalid", nil)
	}
	if request.ScheduleMode == "relative_field" && request.SourceField == "" || request.ScheduleMode == "business_calendar" && request.BusinessCalendarKey == "" {
		return policyError("backend.record_timer.schedule_source_required", nil)
	}
	if request.TargetType != "action" && request.TargetType != "workflow" {
		return policyError("backend.record_timer.target_invalid", nil)
	}
	if request.MaxAttempts < 1 || request.MaxAttempts > 100 || request.RetryDelaySeconds < 1 || request.RetryMaxDelaySeconds < request.RetryDelaySeconds || request.RetryMaxDelaySeconds > 86400 {
		return policyError("backend.record_timer.retry_policy_invalid", nil)
	}
	if _, err := time.LoadLocation(request.Timezone); err != nil {
		return policyError("backend.record_timer.timezone_invalid", err)
	}
	return nil
}

func RetryDelay(record recordmodel.Record) time.Duration {
	base := intValue(record.Data["retry_delay_seconds"], 1)
	maximum := intValue(record.Data["retry_max_delay_seconds"], 60)
	attempt := intValue(record.Data["attempt"], 1)
	delay := int64(base)
	for step := 1; step < attempt && delay < int64(maximum); step++ {
		delay *= 2
		if delay > int64(maximum) {
			delay = int64(maximum)
		}
	}
	return time.Duration(delay) * time.Second
}

func PrepareClaim(candidate recordmodel.Record, owner string, now time.Time, leaseTTL time.Duration) (recordmodel.Record, recordtimermodel.Lease, map[string]any, bool) {
	previousStatus := strings.TrimSpace(fmt.Sprint(candidate.Data["status"]))
	previousLeaseExpires := strings.TrimSpace(fmt.Sprint(candidate.Data["lease_expires_at"]))
	if previousStatus == "leased" {
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, previousLeaseExpires)
		if parseErr != nil || expiresAt.After(now) {
			return recordmodel.Record{}, recordtimermodel.Lease{}, nil, false
		}
	}
	data := make(map[string]any, len(candidate.Data))
	for key, value := range candidate.Data {
		data[key] = value
	}
	candidate.Data = data
	previousToken := intValue(candidate.Data["fencing_token"], 0)
	candidate.Data["status"] = "leased"
	candidate.Data["lease_owner"] = owner
	candidate.Data["lease_expires_at"] = now.Add(leaseTTL).UTC().Format(time.RFC3339Nano)
	candidate.Data["fencing_token"] = previousToken + 1
	candidate.Data["attempt"] = intValue(candidate.Data["attempt"], 0) + 1
	candidate.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	conditions := map[string]any{"status": previousStatus, "fencing_token": previousToken}
	if previousStatus == "leased" {
		conditions["lease_expires_at"] = previousLeaseExpires
	}
	return candidate, recordtimermodel.Lease{Record: candidate, Owner: owner, Token: previousToken + 1}, conditions, true
}

func intValue(value any, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(value)))
	if err != nil {
		return fallback
	}
	return parsed
}

func policyError(code string, cause error) error {
	return apperror.New(apperror.KindBadRequest, code, cause, nil)
}
