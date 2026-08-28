package lifecycle

import (
	"context"
	"fmt"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) processSpec(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, holds []lifecyclemodel.LegalHold, cutoff time.Time, limit int) (lifecyclemodel.CleanupBatchResult, error) {
	where, args := cleanupWhere(e.store, spec, job.WorkspaceID, cutoff, 1)
	if job.Operation == lifecyclemodel.OperationArchive {
		where += " AND NOT EXISTS (SELECT 1 FROM " + e.store.TableIdentifier("lifecycle_archive_entries") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(len(args)+1) + " AND " + e.store.Identifier("source_table") + " = " + e.store.Placeholder(len(args)+2) + " AND " + e.store.Identifier("resource_id") + " = " + e.store.Identifier(spec.table) + "." + e.store.Identifier(spec.idColumn) + " AND " + e.store.Identifier("policy_key") + " = " + e.store.Placeholder(len(args)+3) + ")"
		args = append(args, job.WorkspaceID, spec.table, policy.Policy.Key)
	}
	args = append(args, limit)
	query := "SELECT " + e.store.Identifier(spec.idColumn) + ", " + e.store.Identifier(spec.timeColumn) + " FROM " + e.store.TableIdentifier(spec.table) + " WHERE " + where + " ORDER BY " + e.store.Identifier(spec.timeColumn) + ", " + e.store.Identifier(spec.idColumn) + " LIMIT " + e.store.Placeholder(len(args))
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
		deleteWhere := e.store.Identifier(spec.idColumn) + " = " + e.store.Placeholder(1)
		deleteArgs := []any{candidate.id}
		if spec.tenantColumn != "" {
			deleteWhere = e.store.Identifier(spec.tenantColumn) + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier(spec.idColumn) + " = " + e.store.Placeholder(2)
			deleteArgs = []any{job.WorkspaceID, candidate.id}
		}
		deleteArgs = append(deleteArgs, job.WorkspaceID, spec.table, candidate.id)
		archiveStart := len(deleteArgs) - 2
		deleteQuery := "DELETE FROM " + e.store.TableIdentifier(spec.table) + " WHERE " + deleteWhere + " AND EXISTS (SELECT 1 FROM " + e.store.TableIdentifier("lifecycle_archive_entries") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(archiveStart) + " AND " + e.store.Identifier("source_table") + " = " + e.store.Placeholder(archiveStart+1) + " AND " + e.store.Identifier("resource_id") + " = " + e.store.Placeholder(archiveStart+2) + ")"
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
		where, args := e.store.Identifier(check.referenceColumn)+" = "+e.store.Placeholder(1), []any{resourceID}
		if check.tenantColumn != "" {
			where, args = e.store.Identifier(check.tenantColumn)+" = "+e.store.Placeholder(1)+" AND "+e.store.Identifier(check.referenceColumn)+" = "+e.store.Placeholder(2), []any{workspaceID, resourceID}
		}
		if check.fixedColumn != "" {
			args = append(args, check.fixedValue)
			where += " AND " + e.store.Identifier(check.fixedColumn) + " = " + e.store.Placeholder(len(args))
		}
		query := "SELECT COUNT(*) FROM " + e.store.TableIdentifier(check.table) + " WHERE " + where
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
