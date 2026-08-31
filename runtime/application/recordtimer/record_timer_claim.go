package recordtimer

import (
	"context"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordtimerpolicy "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (s *RecordTimerApplicationService) ClaimDueRecordTimers(ctx context.Context, workspaceID string, now time.Time, limit int, scope principalmodel.SystemScope) ([]RecordTimerLease, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return nil, recordTimerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	limit = recordTimerLimit(limit)
	object, err := s.objectForPrincipal(ctx, recordTimerWorkerPrincipal(), "record_timer")
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
		return nil, recordTimerInternalError("list due record timers", err)
	}
	candidates := append([]recordmodel.Record(nil), page.Items...)
	if len(candidates) < limit {
		query.PageSize = limit - len(candidates)
		query.Filters = map[string]any{"status": "leased", "due_at__lte": now.UTC().Format(time.RFC3339Nano), "lease_expires_at__lte": now.UTC().Format(time.RFC3339Nano)}
		expired, expiredErr := s.repository.ListRecords(ctx, workspaceID, object, query)
		if expiredErr != nil {
			return nil, recordTimerInternalError("list expired record timer leases", expiredErr)
		}
		candidates = append(candidates, expired.Items...)
	}
	leases := make([]RecordTimerLease, 0, len(candidates))
	owner := s.worker.WorkerID.String()
	claimCommits := make([]transactionmodel.RecordMutationCommit, 0, len(candidates)*2)
	for _, candidate := range candidates {
		updated, lease, conditions, eligible := recordtimerpolicy.PrepareClaim(candidate, owner, now, s.leaseTTL())
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
		updated, lease, conditions, eligible := recordtimerpolicy.PrepareClaim(candidate, owner, now, s.leaseTTL())
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
