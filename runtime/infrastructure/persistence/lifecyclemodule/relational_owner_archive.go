package lifecyclemodule

import (
	"context"
	"encoding/json"
	"fmt"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	"github.com/domainry/domainry-orm/query"
)

func (e OwnerExecutor) archiveCandidate(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, spec cleanupSpec, resourceID string) (bool, error) {
	if e.archives == nil {
		return false, fmt.Errorf("Lifecycle archive store is unavailable")
	}
	exists, err := e.archives.Archived(ctx, job.WorkspaceID, spec.table, resourceID, policy.Policy.Key)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	predicate := query.Predicate(query.Equal(spec.idColumn, resourceID))
	builder := query.NewSelectBuilder(e.renderer, spec.table).Projections(query.Project(query.Star()))
	if spec.tenantColumn != "" {
		builder = query.NewWorkspaceSelectBuilder(e.renderer, spec.table, job.WorkspaceID).Projections(query.Project(query.Star()))
	}
	queryValue, args, buildErr := builder.Where(predicate).Build()
	if buildErr != nil {
		return false, buildErr
	}
	rows, err := e.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return false, err
	}
	columns, _ := rows.Columns()
	if !rows.Next() {
		err := rows.Err()
		_ = rows.Close()
		return false, err
	}
	values, pointers := make([]any, len(columns)), make([]any, len(columns))
	for index := range values {
		pointers[index] = &values[index]
	}
	_ = rows.Scan(pointers...)
	if err := rows.Close(); err != nil {
		return false, err
	}
	payload := map[string]any{}
	for index, column := range columns {
		if bytes, ok := values[index].([]byte); ok {
			payload[column] = string(bytes)
		} else {
			payload[column] = values[index]
		}
	}
	raw, _ := json.Marshal(payload)
	return e.archivePayload(ctx, job, policy, spec.table, resourceID, raw)
}

func (e OwnerExecutor) archivePayload(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, sourceTable, resourceID string, raw []byte) (bool, error) {
	if e.archives == nil {
		return false, fmt.Errorf("Lifecycle archive store is unavailable")
	}
	return e.archives.ArchivePayload(ctx, e.owner, job, policy, sourceTable, resourceID, raw)
}
