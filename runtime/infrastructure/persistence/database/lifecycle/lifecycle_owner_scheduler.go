package lifecycle

import (
	"context"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) schedulerRunHasActiveDeadLetter(ctx context.Context, workspaceID, table, runID string) (bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, table, workspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.And(ormbuilder.Equal("job_run_id", runID), ormbuilder.NotIn("status", "resolved", "ignored"))).Build()
	if buildErr != nil {
		return false, buildErr
	}
	var count int
	if err := e.database().QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (e OwnerExecutor) archiveSchedulerRunChildren(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, runID string, purge bool) (int64, int64, error) {
	var archived, purged int64
	children := []struct {
		table            string
		resolvedStatuses bool
	}{
		{table: spec.schedulerEventTable},
		{table: spec.schedulerDeadLetterTable, resolvedStatuses: true},
	}
	for _, child := range children {
		if child.table == "" {
			continue
		}
		predicate := ormbuilder.Predicate(ormbuilder.Equal("job_run_id", runID))
		if child.resolvedStatuses {
			predicate = ormbuilder.And(predicate, ormbuilder.In("status", "resolved", "ignored"))
		}
		query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, child.table, job.WorkspaceID).Columns("id").Where(predicate).Build()
		if buildErr != nil {
			return archived, purged, buildErr
		}
		rows, err := e.database().QueryContext(ctx, query, args...)
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
		deleteQuery, deleteArgs, buildErr := ormbuilder.NewWorkspaceDeleteBuilder(e.store.SQLRenderer, child.table, job.WorkspaceID).Where(predicate).Build()
		if buildErr != nil {
			return archived, purged, buildErr
		}
		deleted, err := e.database().ExecContext(ctx, deleteQuery, deleteArgs...)
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
