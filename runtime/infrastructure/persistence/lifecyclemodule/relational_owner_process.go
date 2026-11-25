package lifecyclemodule

import (
	"context"
	"fmt"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-orm/query"
)

func (e OwnerExecutor) processSpec(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, holds []lifecyclemodel.LegalHold, cutoff time.Time, limit int, position cleanupPosition) (lifecyclemodel.CleanupBatchResult, error) {
	const candidateAlias = "candidate"
	predicate := cleanupPredicate(spec, cutoff, candidateAlias)
	if position.id != nil {
		predicate = query.And(predicate, query.Or(
			query.GreaterThan(spec.timeColumn, position.timestamp),
			query.And(query.Equal(spec.timeColumn, position.timestamp), query.GreaterThan(spec.idColumn, position.id)),
		))
	}
	builder := query.NewSelectBuilder(e.renderer, spec.table).Alias(candidateAlias).Columns(spec.idColumn, spec.timeColumn)
	if spec.tenantColumn != "" {
		builder = query.NewWorkspaceSelectBuilder(e.renderer, spec.table, job.WorkspaceID).Alias(candidateAlias).Columns(spec.idColumn, spec.timeColumn)
	}
	queryValue, args, buildErr := builder.Where(predicate).OrderBy(query.Ascending(spec.timeColumn), query.Ascending(spec.idColumn)).Limit(limit + 1).Build()
	if buildErr != nil {
		return lifecyclemodel.CleanupBatchResult{}, buildErr
	}
	rows, err := e.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return lifecyclemodel.CleanupBatchResult{}, err
	}
	defer rows.Close()
	result := lifecyclemodel.CleanupBatchResult{Done: true}
	candidates := make([]cleanupPosition, 0, limit)
	for rows.Next() {
		if len(candidates) == limit {
			result.Done = false
			break
		}
		item := cleanupPosition{spec: position.spec}
		if err := rows.Scan(&item.id, &item.timestamp); err != nil {
			return lifecyclemodel.CleanupBatchResult{}, err
		}
		if bytes, ok := item.id.([]byte); ok {
			item.id = string(bytes)
		}
		switch id := item.id.(type) {
		case int64:
		case string:
			if id == "" {
				return lifecyclemodel.CleanupBatchResult{}, fmt.Errorf("empty lifecycle cleanup candidate ID")
			}
		default:
			return lifecyclemodel.CleanupBatchResult{}, fmt.Errorf("unsupported lifecycle cleanup candidate ID type %T", item.id)
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		return lifecyclemodel.CleanupBatchResult{}, err
	}
	if err := rows.Close(); err != nil {
		return lifecyclemodel.CleanupBatchResult{}, err
	}
	for _, candidate := range candidates {
		id := fmt.Sprint(candidate.id)
		result.Checkpoint = candidate.checkpoint()
		result.Scanned++
		eligibleAt := lifecycleCandidateTime(candidate.timestamp, spec.unixNanoTime)
		if result.OldestEligible.IsZero() || eligibleAt.Before(result.OldestEligible) {
			result.OldestEligible = eligibleAt
		}
		if lifecycleHeld(holds, e.owner, spec.table, id, job.UpdatedAt) {
			result.Skipped++
			continue
		}
		if job.Operation == lifecyclemodel.OperationPurge {
			referenced, referenceErr := e.cleanupCandidateReferenced(ctx, job.WorkspaceID, id, spec.referenceChecks)
			if referenceErr != nil {
				return result, referenceErr
			}
			if referenced {
				result.Skipped++
				continue
			}
		}
		if job.DryRun {
			continue
		}
		archived, archiveErr := e.archiveCandidate(ctx, job, policy, spec, candidate.id)
		if archiveErr != nil {
			result.Failed++
			return result, archiveErr
		}
		if archived {
			result.Archived++
		}
		if len(spec.childCollections) > 0 {
			childArchived, childPurged, childErr := e.archiveChildCollections(ctx, job, policy, id, spec.childCollections, job.Operation == lifecyclemodel.OperationPurge)
			result.Archived, result.Purged = result.Archived+childArchived, result.Purged+childPurged
			if childErr != nil {
				result.Failed++
				return result, childErr
			}
		}
		if job.Operation == lifecyclemodel.OperationArchive {
			continue
		}
		deletePredicate := query.Predicate(query.Equal(spec.idColumn, candidate.id))
		var deleteQuery string
		var deleteArgs []any
		var buildErr error
		if spec.tenantColumn != "" {
			deleteQuery, deleteArgs, buildErr = query.NewWorkspaceDeleteBuilder(e.renderer, spec.table, job.WorkspaceID).Where(deletePredicate).Build()
		} else {
			deleteQuery, deleteArgs, buildErr = query.NewDeleteBuilder(e.renderer, spec.table).Where(deletePredicate).Build()
		}
		if buildErr != nil {
			result.Failed++
			return result, buildErr
		}
		deleted, deleteErr := e.database(ctx).ExecContext(ctx, deleteQuery, deleteArgs...)
		if deleteErr != nil {
			result.Failed++
			return result, fmt.Errorf("purge %s %s: %w", spec.table, candidate.id, deleteErr)
		}
		count, rowsErr := deleted.RowsAffected()
		if rowsErr != nil {
			result.Failed++
			return result, fmt.Errorf("read purged %s %s count: %w", spec.table, candidate.id, rowsErr)
		}
		result.Purged += count
	}
	return result, nil
}

func (e OwnerExecutor) cleanupCandidateReferenced(ctx context.Context, workspaceID, resourceID string, checks []cleanupReferenceCheck) (bool, error) {
	for _, check := range checks {
		predicate := query.Predicate(query.Equal(check.referenceColumn, resourceID))
		builder := query.NewSelectBuilder(e.renderer, check.table).Projections(query.Project(query.CountAll()))
		if check.tenantColumn != "" {
			builder = query.NewWorkspaceSelectBuilder(e.renderer, check.table, workspaceID).Projections(query.Project(query.CountAll()))
		}
		if check.fixedColumn != "" {
			predicate = query.And(predicate, query.Equal(check.fixedColumn, check.fixedValue))
		}
		queryValue, args, buildErr := builder.Where(predicate).Build()
		if buildErr != nil {
			return false, buildErr
		}
		var count int
		if err := e.database(ctx).QueryRowContext(ctx, queryValue, args...).Scan(&count); err != nil {
			return false, err
		}
		if count > 0 {
			return true, nil
		}
	}
	return false, nil
}

func lifecycleHeld(holds []lifecyclemodel.LegalHold, owner, resourceType, resourceID string, now time.Time) bool {
	for _, hold := range holds {
		if now.Before(hold.StartsAt) || (hold.EndsAt != nil && !now.Before(*hold.EndsAt)) {
			continue
		}
		if (hold.Owner == "" || hold.Owner == owner) && (hold.ResourceType == "" || hold.ResourceType == resourceType) && (hold.ResourceID == "" || hold.ResourceID == resourceID) {
			return true
		}
	}
	return false
}
