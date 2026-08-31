package lifecyclemodule

import (
	"context"
	"fmt"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-orm/query"
)

func (e OwnerExecutor) processSpec(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, holds []lifecyclemodel.LegalHold, cutoff time.Time, limit int) (lifecyclemodel.CleanupBatchResult, error) {
	const candidateAlias = "candidate"
	predicate := cleanupPredicate(spec, cutoff, candidateAlias)
	builder := query.NewSelectBuilder(e.renderer, spec.table).Alias(candidateAlias).Columns(spec.idColumn, spec.timeColumn)
	if spec.tenantColumn != "" {
		builder = query.NewWorkspaceSelectBuilder(e.renderer, spec.table, job.WorkspaceID).Alias(candidateAlias).Columns(spec.idColumn, spec.timeColumn)
	}
	queryValue, args, buildErr := builder.Where(predicate).OrderBy(query.Ascending(spec.timeColumn), query.Ascending(spec.idColumn)).Limit(limit).Build()
	if buildErr != nil {
		return lifecyclemodel.CleanupBatchResult{}, buildErr
	}
	rows, err := e.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return lifecyclemodel.CleanupBatchResult{}, err
	}
	type candidate struct {
		id        string
		timestamp any
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.id, &item.timestamp); err != nil {
			return lifecyclemodel.CleanupBatchResult{}, err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return lifecyclemodel.CleanupBatchResult{}, err
	}
	_ = rows.Close()
	result := lifecyclemodel.CleanupBatchResult{Done: len(candidates) < limit}
	for _, candidate := range candidates {
		if e.archives == nil {
			return result, fmt.Errorf("Lifecycle archive store is unavailable")
		}
		alreadyArchived, archiveLookupErr := e.archives.Archived(ctx, job.WorkspaceID, spec.table, candidate.id, policy.Policy.Key)
		if archiveLookupErr != nil {
			return result, archiveLookupErr
		}
		if job.Operation == lifecyclemodel.OperationArchive && alreadyArchived {
			continue
		}
		result.Checkpoint = spec.table + ":" + candidate.id
		result.Scanned++
		eligibleAt := time.Time{}
		if spec.unixNanoTime {
			switch value := candidate.timestamp.(type) {
			case int64:
				eligibleAt = time.Unix(0, value).UTC()
			}
		} else if value, ok := candidate.timestamp.(string); ok {
			eligibleAt, _ = time.Parse(time.RFC3339Nano, value)
		} else if value, ok := candidate.timestamp.([]byte); ok {
			eligibleAt, _ = time.Parse(time.RFC3339Nano, string(value))
		}
		if result.OldestEligible.IsZero() || eligibleAt.Before(result.OldestEligible) {
			result.OldestEligible = eligibleAt
		}
		if lifecycleHeld(holds, e.owner, spec.table, candidate.id, job.UpdatedAt) {
			result.Skipped++
			continue
		}
		if job.Operation == lifecyclemodel.OperationPurge {
			referenced, referenceErr := e.cleanupCandidateReferenced(ctx, job.WorkspaceID, candidate.id, spec.referenceChecks)
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
			childArchived, childPurged, childErr := e.archiveChildCollections(ctx, job, policy, candidate.id, spec.childCollections, job.Operation == lifecyclemodel.OperationPurge)
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
