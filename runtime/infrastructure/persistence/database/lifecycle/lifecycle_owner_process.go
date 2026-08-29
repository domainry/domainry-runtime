package lifecycle

import (
	"context"
	"fmt"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) processSpec(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, holds []lifecyclemodel.LegalHold, cutoff time.Time, limit int) (lifecyclemodel.CleanupBatchResult, error) {
	const candidateAlias = "candidate"
	predicate := cleanupPredicate(spec, cutoff, candidateAlias)
	if job.Operation == lifecyclemodel.OperationArchive {
		archive := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, "lifecycle_archive_entries", job.WorkspaceID).Alias("archive").Columns("id").Where(ormbuilder.And(
			ormbuilder.Equal("source_table", spec.table),
			ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("archive", "resource_id"), ormbuilder.QualifiedColumn(candidateAlias, spec.idColumn)),
			ormbuilder.Equal("policy_key", policy.Policy.Key),
		))
		predicate = ormbuilder.And(predicate, ormbuilder.NotExistsSubquery(archive))
	}
	builder := ormbuilder.NewSelectBuilder(e.store.SQLRenderer, spec.table).Alias(candidateAlias).Columns(spec.idColumn, spec.timeColumn)
	if spec.tenantColumn != "" {
		builder = ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, spec.table, job.WorkspaceID).Alias(candidateAlias).Columns(spec.idColumn, spec.timeColumn)
	}
	query, args, buildErr := builder.Where(predicate).OrderBy(ormbuilder.Ascending(spec.timeColumn), ormbuilder.Ascending(spec.idColumn)).Limit(limit).Build()
	if buildErr != nil {
		return lifecyclemodel.CleanupBatchResult{}, buildErr
	}
	rows, err := e.database().QueryContext(ctx, query, args...)
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
		if spec.schedulerDeadLetterTable != "" {
			blocked, blockErr := e.schedulerRunHasActiveDeadLetter(ctx, job.WorkspaceID, spec.schedulerDeadLetterTable, candidate.id)
			if blockErr != nil {
				return result, blockErr
			}
			if blocked {
				result.Skipped++
				continue
			}
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
		if spec.table == "record_batch_jobs" {
			childArchived, childPurged, childErr := e.archiveRecordBatchChunks(ctx, job, policy, candidate.id, job.Operation == lifecyclemodel.OperationPurge)
			result.Archived, result.Purged = result.Archived+childArchived, result.Purged+childPurged
			if childErr != nil {
				result.Failed++
				return result, childErr
			}
		}
		if spec.table == "integration_events" {
			childArchived, childPurged, childErr := e.archiveIntegrationEventMappingIntents(ctx, job, policy, candidate.id, job.Operation == lifecyclemodel.OperationPurge)
			result.Archived, result.Purged = result.Archived+childArchived, result.Purged+childPurged
			if childErr != nil {
				result.Failed++
				return result, childErr
			}
		}
		if spec.workflowProcessChildren {
			childArchived, childPurged, childErr := e.archiveWorkflowProcessChildren(ctx, job, policy, candidate.id, job.Operation == lifecyclemodel.OperationPurge)
			result.Archived, result.Purged = result.Archived+childArchived, result.Purged+childPurged
			if childErr != nil {
				result.Failed++
				return result, childErr
			}
		}
		if spec.table == "job_run" {
			childArchived, childPurged, childErr := e.archiveSchedulerRunChildren(ctx, job, policy, spec, candidate.id, job.Operation == lifecyclemodel.OperationPurge)
			result.Archived, result.Purged = result.Archived+childArchived, result.Purged+childPurged
			if childErr != nil {
				result.Failed++
				return result, childErr
			}
		}
		if job.Operation == lifecyclemodel.OperationArchive {
			continue
		}
		archive := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, "lifecycle_archive_entries", job.WorkspaceID).Columns("id").Where(ormbuilder.And(ormbuilder.Equal("source_table", spec.table), ormbuilder.Equal("resource_id", candidate.id)))
		deletePredicate := ormbuilder.And(ormbuilder.Equal(spec.idColumn, candidate.id), ormbuilder.ExistsSubquery(archive))
		var deleteQuery string
		var deleteArgs []any
		var buildErr error
		if spec.tenantColumn != "" {
			deleteQuery, deleteArgs, buildErr = ormbuilder.NewWorkspaceDeleteBuilder(e.store.SQLRenderer, spec.table, job.WorkspaceID).Where(deletePredicate).Build()
		} else {
			deleteQuery, deleteArgs, buildErr = ormbuilder.NewDeleteBuilder(e.store.SQLRenderer, spec.table).Where(deletePredicate).Build()
		}
		if buildErr != nil {
			result.Failed++
			return result, buildErr
		}
		deleted, deleteErr := e.database().ExecContext(ctx, deleteQuery, deleteArgs...)
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
		predicate := ormbuilder.Predicate(ormbuilder.Equal(check.referenceColumn, resourceID))
		builder := ormbuilder.NewSelectBuilder(e.store.SQLRenderer, check.table).Projections(ormbuilder.Project(ormbuilder.CountAll()))
		if check.tenantColumn != "" {
			builder = ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, check.table, workspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll()))
		}
		if check.fixedColumn != "" {
			predicate = ormbuilder.And(predicate, ormbuilder.Equal(check.fixedColumn, check.fixedValue))
		}
		query, args, buildErr := builder.Where(predicate).Build()
		if buildErr != nil {
			return false, buildErr
		}
		var count int
		if err := e.database().QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
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
