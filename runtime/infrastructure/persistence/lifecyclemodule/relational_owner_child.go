package lifecyclemodule

import (
	"context"
	"fmt"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-orm/query"
)

func (e OwnerExecutor) archiveChildCollections(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, parentID string, children []relationalChildCollection, purge bool) (int64, int64, error) {
	var archived, purged int64
	for _, child := range children {
		queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(e.renderer, child.table, job.WorkspaceID).Columns(child.idColumn).Where(query.Equal(child.parentColumn, parentID)).OrderBy(query.Ascending(child.idColumn)).Build()
		if buildErr != nil {
			return archived, purged, buildErr
		}
		rows, err := e.database(ctx).QueryContext(ctx, queryValue, args...)
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
			created, err := e.archiveCandidate(ctx, job, policy, cleanupSpec{table: child.table, idColumn: child.idColumn, tenantColumn: child.tenantColumn}, id)
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
		queryValue, args, buildErr = query.NewWorkspaceDeleteBuilder(e.renderer, child.table, job.WorkspaceID).Where(query.Equal(child.parentColumn, parentID)).Build()
		if buildErr != nil {
			return archived, purged, buildErr
		}
		result, err := e.database(ctx).ExecContext(ctx, queryValue, args...)
		if err != nil {
			return archived, purged, fmt.Errorf("purge child collection %s: %w", child.table, err)
		}
		count, err := result.RowsAffected()
		if err != nil {
			return archived, purged, err
		}
		purged += count
	}
	return archived, purged, nil
}
