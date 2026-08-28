package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type RecordTimerSchedule struct {
	TimerKey             string
	ObjectKey            string
	RecordID             string
	Purpose              string
	ScheduleMode         string
	DueAt                time.Time
	SourceField          string
	OffsetSeconds        int
	Timezone             string
	BusinessCalendarKey  string
	TargetType           string
	TargetKey            string
	PayloadJSON          string
	Priority             int
	Sequence             int64
	MaxAttempts          int
	RetryDelaySeconds    int
	RetryMaxDelaySeconds int
	SupersedesTimerID    string
}

type RecordTimerLease struct {
	Record recordmodel.Record
	Owner  string
	Token  int
}

type RecordTimerBusinessCalendar interface {
	AddBusinessDuration(context.Context, string, time.Time, time.Duration, *time.Location) (time.Time, error)
}

type StandardRecordTimerBusinessCalendar struct{}

func (StandardRecordTimerBusinessCalendar) AddBusinessDuration(ctx context.Context, key string, base time.Time, offset time.Duration, _ *time.Location) (time.Time, error) {
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

func (s *SchedulerApplicationService) BuildRecordTimerMutationFromSource(ctx context.Context, workspaceID string, request RecordTimerSchedule, source recordmodel.Record, calendar RecordTimerBusinessCalendar, now time.Time) (transactionmodel.RecordMutationCommit, error) {
	resolved, err := ResolveRecordTimerSchedule(ctx, request, source, calendar)
	if err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	return s.BuildRecordTimerMutation(ctx, workspaceID, resolved, now)
}

func (s *SchedulerApplicationService) ScheduleRecordTimer(ctx context.Context, workspaceID string, request RecordTimerSchedule, source recordmodel.Record, calendar RecordTimerBusinessCalendar, now time.Time, scope principalmodel.SystemScope) (recordmodel.Record, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return recordmodel.Record{}, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	commit, err := s.BuildRecordTimerMutationFromSource(ctx, workspaceID, request, source, calendar, now)
	if err != nil {
		return recordmodel.Record{}, err
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, []transactionmodel.RecordMutationCommit{commit}); err != nil {
		if existing, found, getErr := s.repository.GetRecord(ctx, workspaceID, commit.Object, commit.Record.ID); getErr == nil && found {
			return existing, nil
		}
		return recordmodel.Record{}, err
	}
	return commit.Record, nil
}

func ResolveRecordTimerSchedule(ctx context.Context, request RecordTimerSchedule, source recordmodel.Record, calendar RecordTimerBusinessCalendar) (RecordTimerSchedule, error) {
	request = normalizeRecordTimerSchedule(request)
	location, err := time.LoadLocation(request.Timezone)
	if err != nil {
		return RecordTimerSchedule{}, schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_timezone_invalid", err)
	}
	base := request.DueAt
	if request.ScheduleMode == "relative_field" || request.ScheduleMode == "business_calendar" && request.SourceField != "" {
		raw := strings.TrimSpace(fmt.Sprint(source.Data[request.SourceField]))
		parsed, parseErr := time.Parse(time.RFC3339Nano, raw)
		if parseErr != nil {
			return RecordTimerSchedule{}, schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_source_datetime_invalid", parseErr)
		}
		base = parsed
	}
	switch request.ScheduleMode {
	case "absolute":
	case "relative_field":
		request.DueAt = base.Add(time.Duration(request.OffsetSeconds) * time.Second)
	case "business_calendar":
		if calendar == nil {
			return RecordTimerSchedule{}, schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_business_calendar_required", nil)
		}
		request.DueAt, err = calendar.AddBusinessDuration(ctx, request.BusinessCalendarKey, base.In(location), time.Duration(request.OffsetSeconds)*time.Second, location)
		if err != nil {
			return RecordTimerSchedule{}, schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_business_calendar_invalid", err)
		}
	}
	return request, nil
}

// BuildRecordTimerMutation returns the canonical record commit that callers
// append to the same mutation batch as the source business record.
func (s *SchedulerApplicationService) BuildRecordTimerMutation(ctx context.Context, workspaceID string, request RecordTimerSchedule, now time.Time) (transactionmodel.RecordMutationCommit, error) {
	if err := ctx.Err(); err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	request = normalizeRecordTimerSchedule(request)
	if err := validateRecordTimerSchedule(request); err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	record := recordmodel.Record{ID: recordTimerID(workspaceID, request), CreatedAt: stamp, UpdatedAt: stamp, Data: map[string]any{
		"timer_key": request.TimerKey, "object_key": request.ObjectKey, "record_id": request.RecordID, "purpose": request.Purpose,
		"status": "scheduled", "schedule_mode": request.ScheduleMode, "due_at": request.DueAt.UTC().Format(time.RFC3339Nano),
		"source_field": request.SourceField, "offset_seconds": request.OffsetSeconds, "timezone": request.Timezone,
		"business_calendar_key": request.BusinessCalendarKey, "target_type": request.TargetType, "target_key": request.TargetKey,
		"payload_json": request.PayloadJSON, "priority": request.Priority, "sequence": request.Sequence,
		"lease_owner": "", "lease_expires_at": "", "fencing_token": 0, "attempt": 0,
		"max_attempts": request.MaxAttempts, "retry_delay_seconds": request.RetryDelaySeconds, "retry_max_delay_seconds": request.RetryMaxDelaySeconds,
		"last_error": "", "failed_at": "", "supersedes_timer_id": request.SupersedesTimerID, "fired_at": "", "cancelled_at": "",
	}}
	return transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: record}, nil
}

// BuildRecordTimerTerminalMutation lets a source mutation cancel or supersede
// its future timer in the same canonical mutation batch.
func (s *SchedulerApplicationService) BuildRecordTimerTerminalMutation(ctx context.Context, timer recordmodel.Record, terminalStatus string, now time.Time) (transactionmodel.RecordMutationCommit, error) {
	if terminalStatus != "cancelled" && terminalStatus != "superseded" {
		return transactionmodel.RecordMutationCommit{}, schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_terminal_status_invalid", nil)
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	previousStatus := strings.TrimSpace(fmt.Sprint(timer.Data["status"]))
	if previousStatus != "scheduled" {
		return transactionmodel.RecordMutationCommit{}, schedulerError(apperror.KindConflict, "backend.scheduler.record_timer_not_scheduled", nil)
	}
	data := make(map[string]any, len(timer.Data))
	for key, value := range timer.Data {
		data[key] = value
	}
	timer.Data = data
	timer.Data["status"] = terminalStatus
	timer.Data["cancelled_at"] = now.UTC().Format(time.RFC3339Nano)
	timer.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	return transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: timer, Conditions: map[string]any{"status": previousStatus, "fencing_token": schedulerpolicy.SchedulerInt(timer.Data["fencing_token"], 0)}}, nil
}

func (s *SchedulerApplicationService) BuildRecordTimerSupersedeMutations(ctx context.Context, workspaceID string, current recordmodel.Record, replacement RecordTimerSchedule, now time.Time) ([]transactionmodel.RecordMutationCommit, error) {
	terminal, err := s.BuildRecordTimerTerminalMutation(ctx, current, "superseded", now)
	if err != nil {
		return nil, err
	}
	replacement.SupersedesTimerID = current.ID
	created, err := s.BuildRecordTimerMutation(ctx, workspaceID, replacement, now)
	if err != nil {
		return nil, err
	}
	return []transactionmodel.RecordMutationCommit{terminal, created}, nil
}

func (s *SchedulerApplicationService) CancelRecordTimers(ctx context.Context, workspaceID, objectKey, recordID, purpose string, now time.Time, scope principalmodel.SystemScope) (int, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return 0, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return 0, err
	}
	filters := map[string]any{"object_key": strings.TrimSpace(objectKey), "record_id": strings.TrimSpace(recordID), "status": "scheduled"}
	if purpose = strings.TrimSpace(purpose); purpose != "" {
		filters["purpose"] = purpose
	}
	cancelled := 0
	for {
		page, listErr := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 500, Scope: "all_records", Filters: filters, Sort: []recordmodel.RecordSortRule{{Field: "due_at", Direction: "asc"}, {Field: "id", Direction: "asc"}}})
		if listErr != nil {
			return cancelled, internalError("list record timers to cancel", listErr)
		}
		if len(page.Items) == 0 {
			return cancelled, nil
		}
		commits := make([]transactionmodel.RecordMutationCommit, 0, len(page.Items))
		for _, timer := range page.Items {
			commit, buildErr := s.BuildRecordTimerTerminalMutation(ctx, timer, "cancelled", now)
			if buildErr != nil {
				return cancelled, buildErr
			}
			commits = append(commits, commit)
		}
		if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, commits); err != nil {
			return cancelled, err
		}
		cancelled += len(commits)
	}
}

// ClaimDueRecordTimers performs an ordered optimistic claim. Database ordering
// is priority DESC, sequence ASC, created_at ASC, id ASC; compare-and-set closes
// races between worker instances and fencing rejects stale completion.
func normalizeRecordTimerSchedule(request RecordTimerSchedule) RecordTimerSchedule {
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

func validateRecordTimerSchedule(request RecordTimerSchedule) error {
	if request.TimerKey == "" || request.ObjectKey == "" || request.RecordID == "" || request.Purpose == "" || request.TargetKey == "" || request.DueAt.IsZero() {
		return schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_required", nil)
	}
	if request.ScheduleMode != "absolute" && request.ScheduleMode != "relative_field" && request.ScheduleMode != "business_calendar" {
		return schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_schedule_mode_invalid", nil)
	}
	if request.ScheduleMode == "relative_field" && request.SourceField == "" || request.ScheduleMode == "business_calendar" && request.BusinessCalendarKey == "" {
		return schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_schedule_source_required", nil)
	}
	if request.TargetType != "action" && request.TargetType != "workflow" {
		return schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_target_invalid", nil)
	}
	if request.MaxAttempts < 1 || request.MaxAttempts > 100 || request.RetryDelaySeconds < 1 || request.RetryMaxDelaySeconds < request.RetryDelaySeconds || request.RetryMaxDelaySeconds > 86400 {
		return schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_retry_policy_invalid", nil)
	}
	if _, err := time.LoadLocation(request.Timezone); err != nil {
		return schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_timezone_invalid", err)
	}
	return nil
}

func recordTimerRetryDelay(record recordmodel.Record) time.Duration {
	base := schedulerpolicy.SchedulerInt(record.Data["retry_delay_seconds"], 1)
	maximum := schedulerpolicy.SchedulerInt(record.Data["retry_max_delay_seconds"], 60)
	attempt := schedulerpolicy.SchedulerInt(record.Data["attempt"], 1)
	delay := int64(base)
	for step := 1; step < attempt && delay < int64(maximum); step++ {
		delay *= 2
		if delay > int64(maximum) {
			delay = int64(maximum)
		}
	}
	return time.Duration(delay) * time.Second
}

func cloneRecordTimer(record recordmodel.Record) recordmodel.Record {
	data := make(map[string]any, len(record.Data))
	for key, value := range record.Data {
		data[key] = value
	}
	record.Data = data
	return record
}

func recordTimerID(workspaceID string, request RecordTimerSchedule) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.TrimSpace(workspaceID), request.TimerKey, request.ObjectKey, request.RecordID, request.Purpose}, ":")))
	return "record_timer:" + hex.EncodeToString(sum[:])[:24]
}

func (request RecordTimerSchedule) String() string {
	return fmt.Sprintf("%s:%s:%s:%s", request.TimerKey, request.ObjectKey, request.RecordID, request.Purpose)
}
