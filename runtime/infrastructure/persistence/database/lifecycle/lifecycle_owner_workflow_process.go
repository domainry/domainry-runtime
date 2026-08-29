package lifecycle

import (
	"context"
	"fmt"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) archiveWorkflowProcessChildren(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, processID string, purge bool) (int64, int64, error) {
	children := []struct {
		table string
	}{
		{table: "workflow_process_events"},
		{table: "workflow_tasks"},
		{table: "workflow_node_instances"},
	}
	var archived, purged int64
	for _, child := range children {
		query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, child.table, job.WorkspaceID).Columns("id").Where(ormbuilder.Equal("process_id", processID)).OrderBy(ormbuilder.Ascending("id")).Build()
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
		for _, id := range ids {
			created, err := e.archiveCandidate(ctx, job, policy, cleanupSpec{table: child.table, idColumn: "id", tenantColumn: "workspace_id"}, id)
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
		query, args, buildErr = ormbuilder.NewWorkspaceDeleteBuilder(e.store.SQLRenderer, child.table, job.WorkspaceID).Where(ormbuilder.Equal("process_id", processID)).Build()
		if buildErr != nil {
			return archived, purged, buildErr
		}
		result, err := e.database().ExecContext(ctx, query, args...)
		if err != nil {
			return archived, purged, fmt.Errorf("purge workflow process children %s: %w", child.table, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return archived, purged, err
		}
		purged += count
	}
	return archived, purged, nil
}
