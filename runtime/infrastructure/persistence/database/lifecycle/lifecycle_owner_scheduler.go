package lifecycle

import (
	"context"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) schedulerRunHasActiveDeadLetter(ctx context.Context, workspaceID, table, runID string) (bool, error) {
	query := "SELECT COUNT(*) FROM " + e.store.TableIdentifier(table) + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("job_run_id") + " = " + e.store.Placeholder(2) + " AND " + e.store.Identifier("status") + " NOT IN ('resolved', 'ignored')"
	var count int
	if err := e.database().QueryRowContext(ctx, query, workspaceID, runID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (e OwnerExecutor) archiveSchedulerRunChildren(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, runID string, purge bool) (int64, int64, error) {
	var archived, purged int64
	children := []struct {
		table       string
		statusWhere string
	}{
		{table: spec.schedulerEventTable},
		{table: spec.schedulerDeadLetterTable, statusWhere: " AND " + e.store.Identifier("status") + " IN ('resolved', 'ignored')"},
	}
	for _, child := range children {
		if child.table == "" {
			continue
		}
		query := "SELECT " + e.store.Identifier("id") + " FROM " + e.store.TableIdentifier(child.table) + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("job_run_id") + " = " + e.store.Placeholder(2) + child.statusWhere
		rows, err := e.database().QueryContext(ctx, query, job.WorkspaceID, runID)
		if err != nil {
			return archived, purged, err
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return archived, purged, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return archived, purged, err
		}
		_ = rows.Close()
		childSpec := cleanupSpec{table: child.table, idColumn: "id", tenantColumn: "workspace_id"}
		for _, id := range ids {
			created, err := e.archiveCandidate(ctx, job, policy, childSpec, id)
			if err != nil {
				return archived, purged, err
			}
			if created {
				archived++
			}
		}
		if !purge || len(ids) == 0 {
			continue
		}
		deleteQuery := "DELETE FROM " + e.store.TableIdentifier(child.table) + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("job_run_id") + " = " + e.store.Placeholder(2) + child.statusWhere
		deleted, err := e.database().ExecContext(ctx, deleteQuery, job.WorkspaceID, runID)
		if err != nil {
			return archived, purged, err
		}
		count, err := deleted.RowsAffected()
		if err != nil {
			return archived, purged, err
		}
		purged += count
	}
	return archived, purged, nil
}
