package scheduler

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"context"
	"fmt"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

func (s *SchedulerApplicationService) rescheduleDefinitionCursor(ctx context.Context, workspaceID string, definition recordmodel.Record, nextRunAt time.Time) (recordmodel.Record, map[string]any, error) {
	cursorObject, err := s.ownerObject(ctx, "scheduler_cursor")
	if err != nil {
		return recordmodel.Record{}, nil, err
	}
	now := s.worker.Clock.Now().UTC()
	cursor, found, err := s.repository.GetRecord(ctx, workspaceID, cursorObject, definition.ID)
	if err != nil {
		return recordmodel.Record{}, nil, internalError("get scheduler cursor", err)
	}
	operation := "create"
	optimistic := transactionmodel.OptimisticPrecondition{}
	before := map[string]any(nil)
	if found {
		operation = "update"
		optimistic.ExpectedUpdatedAt = cursor.UpdatedAt
		before = recordvalidation.RecordCloneData(cursor.Data)
	} else {
		cursor = recordmodel.Record{ID: definition.ID, CreatedAt: now.Format(time.RFC3339), Data: map[string]any{"scheduler_definition_key": definition.ID}}
	}
	cursor.Data["scheduler_definition_key"] = definition.ID
	cursor.Data["next_run_at"] = nextRunAt.UTC().Format(time.RFC3339)
	cursor.UpdatedAt = now.Format(time.RFC3339)
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, []transactionmodel.RecordMutationCommit{{Operation: operation, Object: cursorObject, Record: cursor, Optimistic: optimistic}}); err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
			return recordmodel.Record{}, nil, conflict("backend.scheduler.reschedule_conflict")
		}
		return recordmodel.Record{}, nil, internalError("reschedule scheduler definition", err)
	}
	return cursor, before, nil
}

func (s *SchedulerApplicationService) dueDefinitions(ctx context.Context, workspaceID string, _ definitionmodel.ObjectSchema, now time.Time) ([]recordmodel.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definitions, err := s.definitions.ListSchedulerDefinitions(ctx)
	if err != nil {
		return nil, internalError("list scheduler job definitions", err)
	}
	due := []recordmodel.Record{}
	cursors, err := s.schedulerCursorMap(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, record := range definitions {
		record.Data = recordvalidation.RecordCloneData(record.Data)
		if cursor, found := cursors[record.ID]; found {
			for _, key := range []string{"next_run_at", "last_run_at", "last_run_status"} {
				if value, ok := cursor.Data[key]; ok {
					record.Data[key] = value
				}
			}
		}
		if strings.TrimSpace(fmt.Sprint(record.Data["status"])) != "enabled" {
			continue
		}
		if !schedulerDefinitionDue(record, now) {
			continue
		}
		targetType := schedulerDefinitionTargetType(record)
		if targetType != "workflow" && targetType != "report_export" && targetType != "report_snapshot_refresh" {
			continue
		}
		targetKey := existingStringBefore(record, "target_key")
		if targetType == "workflow" && targetKey != "scheduled:*" && !strings.HasPrefix(targetKey, "scheduled:") {
			continue
		}
		if (targetType == "report_export" || targetType == "report_snapshot_refresh") && targetKey == "" {
			continue
		}
		due = append(due, record)
	}
	return due, nil
}

func (s *SchedulerApplicationService) schedulerCursorMap(ctx context.Context, workspaceID string) (map[string]recordmodel.Record, error) {
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "scheduler_cursor")
	if err != nil {
		return nil, err
	}
	out := map[string]recordmodel.Record{}
	afterID := ""
	for {
		result, err := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 500, SkipTotal: true, AfterID: afterID, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}})
		if err != nil {
			return nil, internalError("list scheduler cursors", err)
		}
		for _, cursor := range result.Items {
			key := strings.TrimSpace(fmt.Sprint(cursor.Data["scheduler_definition_key"]))
			if key == "" || key == "<nil>" {
				key = cursor.ID
			}
			out[key] = cursor
		}
		if !result.HasNext {
			break
		}
		afterID = result.Items[len(result.Items)-1].ID
	}
	return out, nil
}

func (s *SchedulerApplicationService) DueDefinitions(ctx context.Context, object definitionmodel.ObjectSchema, now time.Time, scope principalmodel.SystemScope) ([]recordmodel.Record, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	return s.dueDefinitions(ctx, principalmodel.InstallationWorkspaceID, object, now)
}

func schedulerDefinitionTargetType(record recordmodel.Record) string {
	return strings.ToLower(strings.TrimSpace(fmt.Sprint(record.Data["target_type"])))
}

func schedulerRunID(definitionKey string, triggerSource string, now time.Time) string {
	base := "jobrun_" + schedulerpolicy.SchedulerSlug(definitionKey)
	if strings.TrimSpace(triggerSource) == "manual_run" {
		return base + "_manual_" + fmt.Sprint(now.UnixNano())
	}
	return base + "_" + now.Format("20060102")
}

func schedulerRunIDForDefinition(definition recordmodel.Record, triggerSource string, now time.Time) string {
	definitionKey := strings.TrimSpace(fmt.Sprint(definition.Data["key"]))
	if definitionKey == "" || definitionKey == "<nil>" {
		definitionKey = definition.ID
	}
	base := "jobrun_" + schedulerpolicy.SchedulerSlug(definitionKey)
	if strings.TrimSpace(triggerSource) == "manual_run" {
		return base + "_manual_" + fmt.Sprint(now.UnixNano())
	}
	return base + "_" + schedulerDefinitionWindowSuffix(definition, now)
}

func RunIDForDefinition(definition recordmodel.Record, triggerSource string, now time.Time) string {
	return schedulerRunIDForDefinition(definition, triggerSource, now)
}

func schedulerManualIdempotencyKey(definition recordmodel.Record, callerKey string) string {
	key, _ := idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "scheduler.manual", ResourceType: "scheduler_definition", TargetID: definition.ID, Payload: map[string]any{"caller_key": strings.TrimSpace(callerKey)}})
	return key
}

func schedulerManualRunID(definition recordmodel.Record, key string) string {
	definitionKey := valueOrDefault(strings.TrimSpace(fmt.Sprint(definition.Data["key"])), definition.ID)
	if len(key) > 20 {
		key = key[:20]
	}
	return "jobrun_" + schedulerpolicy.SchedulerSlug(definitionKey) + "_manual_" + key
}

func schedulerCommandIdempotencyKey(scope, target, callerKey string) string {
	key, _ := idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "scheduler." + strings.TrimSpace(scope), ResourceType: "scheduler_command", TargetID: strings.TrimSpace(target), Payload: map[string]any{"caller_key": strings.TrimSpace(callerKey)}})
	return key
}

func schedulerDefinitionWindowSuffix(definition recordmodel.Record, now time.Time) string {
	return schedulerpolicy.SchedulerScheduleWindowSuffix(definition, now)
}

func schedulerScheduledRunTime(definition recordmodel.Record, triggerSource string, now time.Time, maxCatchupWindows int) time.Time {
	if strings.TrimSpace(triggerSource) == "manual_run" {
		return now.UTC()
	}
	if definitionLimit := schedulerpolicy.SchedulerInt(definition.Data["max_catchup_windows"], 0); definitionLimit > 0 {
		maxCatchupWindows = definitionLimit
	}
	dueAt, ok := schedulerDefinitionNextRunAt(definition)
	if !ok || dueAt.After(now) {
		return now.UTC()
	}
	switch schedulerMissedWindowPolicy(definition) {
	case "catch_up_one":
		return dueAt.UTC()
	case "catch_up_bounded":
		return schedulerBoundedCatchupWindow(definition, dueAt, now, maxCatchupWindows).UTC()
	default:
		return now.UTC()
	}
}

func ScheduledRunTime(definition recordmodel.Record, triggerSource string, now time.Time, maxCatchupWindows int) time.Time {
	return schedulerScheduledRunTime(definition, triggerSource, now, maxCatchupWindows)
}

func schedulerDefinitionNextRunAt(definition recordmodel.Record) (time.Time, bool) {
	raw := strings.TrimSpace(fmt.Sprint(definition.Data["next_run_at"]))
	if raw == "" || raw == "<nil>" {
		return time.Time{}, false
	}
	dueAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return dueAt.UTC(), true
}

func schedulerMissedWindowPolicy(definition recordmodel.Record) string {
	policy := strings.ToLower(strings.TrimSpace(fmt.Sprint(definition.Data["missed_window_policy"])))
	switch policy {
	case "catch_up_one", "catch_up_bounded":
		return policy
	default:
		return "skip"
	}
}

func schedulerBoundedCatchupWindow(definition recordmodel.Record, dueAt time.Time, now time.Time, maxCatchupWindows int) time.Time {
	if maxCatchupWindows <= 0 {
		maxCatchupWindows = 1
	}
	windows := []time.Time{dueAt.UTC()}
	cursor := dueAt.UTC()
	for attempts := 0; attempts < 1000; attempts++ {
		next := schedulerNextRunAt(definition, cursor).UTC()
		// SchedulerScheduleNextRunAt guarantees a strictly advancing cursor.
		if next.After(now) {
			break
		}
		windows = append(windows, next)
		if len(windows) > maxCatchupWindows {
			windows = windows[len(windows)-maxCatchupWindows:]
		}
		cursor = next
	}
	return windows[0]
}

func schedulerDefinitionDue(record recordmodel.Record, now time.Time) bool {
	raw := strings.TrimSpace(fmt.Sprint(record.Data["next_run_at"]))
	if raw == "" || raw == "<nil>" {
		return true
	}
	dueAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return !dueAt.After(now)
}

func schedulerNextRunAt(definition recordmodel.Record, now time.Time) time.Time {
	return schedulerpolicy.SchedulerScheduleNextRunAt(definition, now)
}

func schedulerDefinitionCursorAnchor(definition recordmodel.Record, run recordmodel.Record, now time.Time) time.Time {
	if schedulerMissedWindowPolicy(definition) != "catch_up_bounded" {
		return now
	}
	raw := strings.TrimSpace(fmt.Sprint(run.Data["scheduled_for"]))
	if raw == "" || raw == "<nil>" {
		return now
	}
	scheduledFor, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return now
	}
	return scheduledFor.UTC()
}
func (s *SchedulerApplicationService) ClaimDueRecordTimers(ctx context.Context, workspaceID string, now time.Time, limit int, scope principalmodel.SystemScope) ([]RecordTimerLease, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return nil, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	limit = schedulerpolicy.SchedulerLimit(limit)
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return nil, err
	}
	query := recordmodel.RecordListQuery{
		Page: 1, PageSize: limit, Scope: "all_records",
		Filters: map[string]any{"status": "scheduled", "due_at__lte": now.UTC().Format(time.RFC3339Nano)},
		Sort:    []recordmodel.RecordSortRule{{Field: "priority", Direction: "desc"}, {Field: "sequence", Direction: "asc"}, {Field: "created_at", Direction: "asc"}, {Field: "id", Direction: "asc"}},
	}
	page, err := s.repository.ListRecords(ctx, workspaceID, object, query)
	if err != nil {
		return nil, internalError("list due record timers", err)
	}
	candidates := append([]recordmodel.Record(nil), page.Items...)
	if len(candidates) < limit {
		query.PageSize = limit - len(candidates)
		query.Filters = map[string]any{"status": "leased", "due_at__lte": now.UTC().Format(time.RFC3339Nano), "lease_expires_at__lte": now.UTC().Format(time.RFC3339Nano)}
		expired, expiredErr := s.repository.ListRecords(ctx, workspaceID, object, query)
		if expiredErr != nil {
			return nil, internalError("list expired record timer leases", expiredErr)
		}
		candidates = append(candidates, expired.Items...)
	}
	leases := make([]RecordTimerLease, 0, len(candidates))
	owner := s.worker.WorkerID.String()
	claimCommits := make([]transactionmodel.RecordMutationCommit, 0, len(candidates))
	for _, candidate := range candidates {
		updated, lease, conditions, eligible := prepareRecordTimerClaim(candidate, owner, now, s.leaseTTL())
		if !eligible {
			continue
		}
		claimCommits = append(claimCommits, transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: updated, Conditions: conditions})
		leases = append(leases, lease)
	}
	if len(claimCommits) == 0 {
		return nil, nil
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, claimCommits); err == nil {
		return leases, nil
	}
	// A competing worker may invalidate one row and roll back the optimistic
	// batch. Fall back to row-level CAS so uncontended candidates still progress.
	leases = leases[:0]
	for _, candidate := range candidates {
		updated, lease, conditions, eligible := prepareRecordTimerClaim(candidate, owner, now, s.leaseTTL())
		if !eligible {
			continue
		}
		claimed, updateErr := s.repository.UpdateRecordWhere(ctx, workspaceID, object, updated, conditions)
		if updateErr != nil {
			return nil, internalError("claim due record timer", updateErr)
		}
		if claimed {
			leases = append(leases, lease)
		}
	}
	return leases, nil
}

func prepareRecordTimerClaim(candidate recordmodel.Record, owner string, now time.Time, leaseTTL time.Duration) (recordmodel.Record, RecordTimerLease, map[string]any, bool) {
	previousStatus := strings.TrimSpace(fmt.Sprint(candidate.Data["status"]))
	previousLeaseExpires := strings.TrimSpace(fmt.Sprint(candidate.Data["lease_expires_at"]))
	if previousStatus == "leased" {
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, previousLeaseExpires)
		if parseErr != nil || expiresAt.After(now) {
			return recordmodel.Record{}, RecordTimerLease{}, nil, false
		}
	}
	data := make(map[string]any, len(candidate.Data))
	for key, value := range candidate.Data {
		data[key] = value
	}
	candidate.Data = data
	previousToken := schedulerpolicy.SchedulerInt(candidate.Data["fencing_token"], 0)
	candidate.Data["status"] = "leased"
	candidate.Data["lease_owner"] = owner
	candidate.Data["lease_expires_at"] = now.Add(leaseTTL).UTC().Format(time.RFC3339Nano)
	candidate.Data["fencing_token"] = previousToken + 1
	candidate.Data["attempt"] = schedulerpolicy.SchedulerInt(candidate.Data["attempt"], 0) + 1
	candidate.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	conditions := map[string]any{"status": previousStatus, "fencing_token": previousToken}
	if previousStatus == "leased" {
		conditions["lease_expires_at"] = previousLeaseExpires
	}
	return candidate, RecordTimerLease{Record: candidate, Owner: owner, Token: previousToken + 1}, conditions, true
}
