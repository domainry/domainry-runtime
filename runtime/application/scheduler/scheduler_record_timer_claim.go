package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

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
	eventObject, err := s.recordTimerEventObject(ctx)
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
	claimCommits := make([]transactionmodel.RecordMutationCommit, 0, len(candidates)*2)
	for _, candidate := range candidates {
		updated, lease, conditions, eligible := prepareRecordTimerClaim(candidate, owner, now, s.leaseTTL())
		if !eligible {
			continue
		}
		claimCommits = append(claimCommits, transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: updated, Conditions: conditions})
		claimCommits = append(claimCommits, buildRecordTimerEventCommit(eventObject, updated, "claimed", owner, "", "", "", now))
		leases = append(leases, lease)
	}
	if len(claimCommits) == 0 {
		return nil, nil
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, claimCommits); err == nil {
		return leases, nil
	}
	leases = leases[:0]
	for _, candidate := range candidates {
		updated, lease, conditions, eligible := prepareRecordTimerClaim(candidate, owner, now, s.leaseTTL())
		if !eligible {
			continue
		}
		commits := []transactionmodel.RecordMutationCommit{
			{Operation: "update", Object: object, Record: updated, Conditions: conditions},
			buildRecordTimerEventCommit(eventObject, updated, "claimed", owner, "", "", "", now),
		}
		if commitErr := s.repository.CommitRecordMutationBatch(ctx, workspaceID, commits); commitErr == nil {
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
