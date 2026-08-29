// Package recordtimer is the Runtime-owned boundary for record-scoped delayed
// actions. It is intentionally separate from the external recurrence Scheduler.
package recordtimer

import (
	"context"
	"time"

	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type RecordTimerSchedule struct {
	TimerKey, ObjectKey, RecordID, Purpose                            string
	ScheduleMode                                                      string
	DueAt                                                             time.Time
	SourceField                                                       string
	OffsetSeconds                                                     int
	Timezone, BusinessCalendarKey, TargetType, TargetKey, PayloadJSON string
	Priority                                                          int
	Sequence                                                          int64
	MaxAttempts, RetryDelaySeconds, RetryMaxDelaySeconds              int
	SupersedesTimerID                                                 string
}

type RecordTimerBusinessCalendar interface {
	AddBusinessDuration(context.Context, string, time.Time, time.Duration, *time.Location) (time.Time, error)
}

type StandardRecordTimerBusinessCalendar struct{}

func (StandardRecordTimerBusinessCalendar) AddBusinessDuration(ctx context.Context, key string, base time.Time, offset time.Duration, location *time.Location) (time.Time, error) {
	return (schedulerapplication.StandardRecordTimerBusinessCalendar{}).AddBusinessDuration(ctx, key, base, offset, location)
}

type RecordTimerApplicationService struct {
	delegate *schedulerapplication.SchedulerApplicationService
}

func NewRecordTimerApplicationService(delegate *schedulerapplication.SchedulerApplicationService) *RecordTimerApplicationService {
	if delegate == nil {
		return nil
	}
	return &RecordTimerApplicationService{delegate: delegate}
}

func (s *RecordTimerApplicationService) Schedule(ctx context.Context, workspaceID string, request RecordTimerSchedule, source recordmodel.Record, calendar RecordTimerBusinessCalendar, now time.Time, scope principalmodel.SystemScope) (recordmodel.Record, error) {
	return s.delegate.ScheduleRecordTimer(ctx, workspaceID, schedulerapplication.RecordTimerSchedule{
		TimerKey: request.TimerKey, ObjectKey: request.ObjectKey, RecordID: request.RecordID, Purpose: request.Purpose,
		ScheduleMode: request.ScheduleMode, DueAt: request.DueAt, SourceField: request.SourceField, OffsetSeconds: request.OffsetSeconds,
		Timezone: request.Timezone, BusinessCalendarKey: request.BusinessCalendarKey, TargetType: request.TargetType, TargetKey: request.TargetKey,
		PayloadJSON: request.PayloadJSON, Priority: request.Priority, Sequence: request.Sequence, MaxAttempts: request.MaxAttempts,
		RetryDelaySeconds: request.RetryDelaySeconds, RetryMaxDelaySeconds: request.RetryMaxDelaySeconds, SupersedesTimerID: request.SupersedesTimerID,
	}, source, calendar, now, scope)
}

func (s *RecordTimerApplicationService) ProcessDueForAllWorkspaces(ctx context.Context, now time.Time, limit int, principal principalmodel.Principal, scope principalmodel.SystemScope) (int, error) {
	return s.delegate.ProcessDueRecordTimersForAllWorkspaces(ctx, now, limit, principal, scope)
}

func (s *RecordTimerApplicationService) InspectFailure(ctx context.Context, id string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.delegate.InspectRecordTimerFailure(ctx, id, principal)
}

func (s *RecordTimerApplicationService) RetryFailure(ctx context.Context, id, reason string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.delegate.RetryRecordTimerFailure(ctx, id, reason, principal)
}

func (s *RecordTimerApplicationService) ResolveFailure(ctx context.Context, id, reason string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return s.delegate.ResolveRecordTimerFailure(ctx, id, reason, principal)
}
