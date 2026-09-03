package recordtimer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimermodel "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/model"
	recordtimerpolicy "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type RecordTimerSchedule = recordtimermodel.Schedule
type RecordTimerLease = recordtimermodel.Lease
type RecordTimerBusinessCalendar = recordtimerpolicy.BusinessCalendar
type StandardRecordTimerBusinessCalendar = recordtimerpolicy.StandardBusinessCalendar

func (s *RecordTimerApplicationService) BuildRecordTimerMutationFromSource(ctx context.Context, workspaceID string, request RecordTimerSchedule, source recordmodel.Record, calendar RecordTimerBusinessCalendar, now time.Time) (transactionmodel.RecordMutationCommit, error) {
	resolved, err := recordtimerpolicy.ResolveSchedule(ctx, request, source, calendar)
	if err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	return s.BuildRecordTimerMutation(ctx, workspaceID, resolved, now)
}

func (s *RecordTimerApplicationService) Schedule(ctx context.Context, workspaceID string, request RecordTimerSchedule, source recordmodel.Record, calendar RecordTimerBusinessCalendar, now time.Time, scope principalmodel.SystemScope) (recordmodel.Record, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return recordmodel.Record{}, recordTimerError(apperror.KindForbidden, "backend.system_scope_required", err)
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

// BuildRecordTimerMutation returns the canonical record commit that callers
// append to the same mutation batch as the source business record.
func (s *RecordTimerApplicationService) BuildRecordTimerMutation(ctx context.Context, workspaceID string, request RecordTimerSchedule, now time.Time) (transactionmodel.RecordMutationCommit, error) {
	if err := ctx.Err(); err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	request = recordtimerpolicy.NormalizeSchedule(request)
	if err := recordtimerpolicy.ValidateSchedule(request); err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	object, err := s.objectForPrincipal(ctx, recordTimerWorkerPrincipal(), "record_timer")
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
func (s *RecordTimerApplicationService) BuildRecordTimerTerminalMutation(ctx context.Context, timer recordmodel.Record, terminalStatus string, now time.Time) (transactionmodel.RecordMutationCommit, error) {
	if terminalStatus != "cancelled" && terminalStatus != "superseded" {
		return transactionmodel.RecordMutationCommit{}, recordTimerError(apperror.KindBadRequest, "backend.record_timer.terminal_status_invalid", nil)
	}
	object, err := s.objectForPrincipal(ctx, recordTimerWorkerPrincipal(), "record_timer")
	if err != nil {
		return transactionmodel.RecordMutationCommit{}, err
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	previousStatus := strings.TrimSpace(fmt.Sprint(timer.Data["status"]))
	if previousStatus != "scheduled" {
		return transactionmodel.RecordMutationCommit{}, recordTimerError(apperror.KindConflict, "backend.record_timer.not_scheduled", nil)
	}
	data := make(map[string]any, len(timer.Data))
	for key, value := range timer.Data {
		data[key] = value
	}
	timer.Data = data
	timer.Data["status"] = terminalStatus
	timer.Data["cancelled_at"] = now.UTC().Format(time.RFC3339Nano)
	timer.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	return transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: timer, Conditions: map[string]any{"status": previousStatus, "fencing_token": recordTimerInt(timer.Data["fencing_token"], 0)}}, nil
}

func (s *RecordTimerApplicationService) BuildRecordTimerSupersedeMutations(ctx context.Context, workspaceID string, current recordmodel.Record, replacement RecordTimerSchedule, now time.Time) ([]transactionmodel.RecordMutationCommit, error) {
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

func (s *RecordTimerApplicationService) CancelRecordTimers(ctx context.Context, workspaceID, objectKey, recordID, purpose string, now time.Time, scope principalmodel.SystemScope) (int, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return 0, recordTimerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	object, err := s.objectForPrincipal(ctx, recordTimerWorkerPrincipal(), "record_timer")
	if err != nil {
		return 0, err
	}
	filters := map[string]any{"object_key": strings.TrimSpace(objectKey), "record_id": strings.TrimSpace(recordID), "status": "scheduled"}
	if purpose = strings.TrimSpace(purpose); purpose != "" {
		filters["purpose"] = purpose
	}
	cancelled := 0
	for {
		page, listErr := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 500, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Filters: filters, Sort: []recordmodel.RecordSortRule{{Field: "due_at", Direction: "asc"}, {Field: "id", Direction: "asc"}}})
		if listErr != nil {
			return cancelled, recordTimerInternalError("list record timers to cancel", listErr)
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

func (s *RecordTimerApplicationService) recordTimerEventObject(ctx context.Context) (definitionmodel.ObjectSchema, error) {
	return s.objectForPrincipal(ctx, recordTimerWorkerPrincipal(), "record_timer_event")
}

func buildRecordTimerEventCommit(object definitionmodel.ObjectSchema, timer recordmodel.Record, eventType, workerID, errorCode, message, nextDueAt string, now time.Time) transactionmodel.RecordMutationCommit {
	attempt := recordTimerInt(timer.Data["attempt"], 0)
	token := recordTimerInt(timer.Data["fencing_token"], 0)
	stamp := now.UTC().Format(time.RFC3339Nano)
	hash := sha256.Sum256([]byte(strings.Join([]string{timer.ID, eventType, stamp, workerID, strconv.Itoa(attempt), strconv.Itoa(token)}, "\x00")))
	event := recordmodel.Record{ID: "record_timer_event_" + hex.EncodeToString(hash[:12]), CreatedAt: stamp, UpdatedAt: stamp, Data: map[string]any{
		"record_timer_id": timer.ID, "event_type": eventType, "attempt": attempt, "fencing_token": token,
		"worker_id": strings.TrimSpace(workerID), "error_code": strings.TrimSpace(errorCode), "message": strings.TrimSpace(message), "next_due_at": strings.TrimSpace(nextDueAt),
	}}
	return transactionmodel.RecordMutationCommit{Operation: "create", Object: object, Record: event}
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
