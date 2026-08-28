package lifecycle

import (
	"context"
	"fmt"

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
		query := "SELECT " + e.store.Identifier("id") + " FROM " + e.store.TableIdentifier(child.table) + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("process_id") + " = " + e.store.Placeholder(2) + " ORDER BY " + e.store.Identifier("id")
		rows, err := e.database().QueryContext(ctx, query, job.WorkspaceID, processID)
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
		result, err := e.database().ExecContext(ctx, "DELETE FROM "+e.store.TableIdentifier(child.table)+" WHERE "+e.store.Identifier("workspace_id")+" = "+e.store.Placeholder(1)+" AND "+e.store.Identifier("process_id")+" = "+e.store.Placeholder(2), job.WorkspaceID, processID)
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
