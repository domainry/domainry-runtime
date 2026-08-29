package lifecycle

import (
	"context"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) archiveIntegrationEventMappingIntents(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, eventID string, purge bool) (int64, int64, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(e.store.SQLRenderer, "integration_event_mapping_intents", job.WorkspaceID).Columns("id").Where(ormbuilder.Equal("event_id", eventID)).Build()
	if buildErr != nil {
		return 0, 0, buildErr
	}
	rows, err := e.database().QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, err
	}
	_ = rows.Close()
	spec := cleanupSpec{table: "integration_event_mapping_intents", idColumn: "id", tenantColumn: "workspace_id"}
	var archived int64
	for _, id := range ids {
		created, err := e.archiveCandidate(ctx, job, policy, spec, id)
		if err != nil {
			return archived, 0, err
		}
		if created {
			archived++
		}
	}
	if !purge || len(ids) == 0 {
		return archived, 0, nil
	}
	query, args, buildErr = ormbuilder.NewWorkspaceDeleteBuilder(e.store.SQLRenderer, "integration_event_mapping_intents", job.WorkspaceID).Where(ormbuilder.Equal("event_id", eventID)).Build()
	if buildErr != nil {
		return archived, 0, buildErr
	}
	result, err := e.database().ExecContext(ctx, query, args...)
	if err != nil {
		return archived, 0, err
	}
	purged, err := result.RowsAffected()
	if err != nil {
		return archived, 0, err
	}
	return archived, purged, nil
}
