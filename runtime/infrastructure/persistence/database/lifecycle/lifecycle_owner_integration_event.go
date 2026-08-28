package lifecycle

import (
	"context"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (e OwnerExecutor) archiveIntegrationEventMappingIntents(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, eventID string, purge bool) (int64, int64, error) {
	query := "SELECT " + e.store.Identifier("id") + " FROM " + e.store.TableIdentifier("integration_event_mapping_intents") + " WHERE " + e.store.Identifier("workspace_id") + " = " + e.store.Placeholder(1) + " AND " + e.store.Identifier("event_id") + " = " + e.store.Placeholder(2)
	rows, err := e.database().QueryContext(ctx, query, job.WorkspaceID, eventID)
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
	result, err := e.database().ExecContext(ctx, "DELETE FROM "+e.store.TableIdentifier("integration_event_mapping_intents")+" WHERE "+e.store.Identifier("workspace_id")+" = "+e.store.Placeholder(1)+" AND "+e.store.Identifier("event_id")+" = "+e.store.Placeholder(2), job.WorkspaceID, eventID)
	if err != nil {
		return archived, 0, err
	}
	purged, err := result.RowsAffected()
	if err != nil {
		return archived, 0, err
	}
	return archived, purged, nil
}
