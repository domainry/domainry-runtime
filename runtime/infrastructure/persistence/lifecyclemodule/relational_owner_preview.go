package lifecyclemodule

import (
	"context"
	"fmt"
	"time"

	"github.com/domainry/domainry-orm/query"
)

func (e OwnerExecutor) previewSpec(ctx context.Context, workspaceID string, spec cleanupSpec, policyKey string, cutoff time.Time) (int64, time.Time, error) {
	if e.archives == nil {
		return 0, time.Time{}, fmt.Errorf("Lifecycle archive store is unavailable")
	}
	const candidateAlias = "candidate"
	builder := query.NewSelectBuilder(e.renderer, spec.table).Alias(candidateAlias).Columns(spec.idColumn, spec.timeColumn)
	if spec.tenantColumn != "" {
		builder = query.NewWorkspaceSelectBuilder(e.renderer, spec.table, workspaceID).Alias(candidateAlias).Columns(spec.idColumn, spec.timeColumn)
	}
	queryValue, args, err := builder.Where(cleanupPredicate(spec, cutoff, candidateAlias)).OrderBy(query.Ascending(spec.timeColumn), query.Ascending(spec.idColumn)).Build()
	if err != nil {
		return 0, time.Time{}, err
	}
	rows, err := e.database(ctx).QueryContext(ctx, queryValue, args...)
	if err != nil {
		return 0, time.Time{}, err
	}
	defer rows.Close()
	var count int64
	oldest := time.Time{}
	for rows.Next() {
		var resourceID string
		var timestamp any
		if err := rows.Scan(&resourceID, &timestamp); err != nil {
			return 0, time.Time{}, err
		}
		archived, err := e.archives.Archived(ctx, workspaceID, spec.table, resourceID, policyKey)
		if err != nil {
			return 0, time.Time{}, err
		}
		if archived {
			continue
		}
		count++
		value := lifecycleCandidateTime(timestamp, spec.unixNanoTime)
		if !value.IsZero() && (oldest.IsZero() || value.Before(oldest)) {
			oldest = value
		}
	}
	return count, oldest, rows.Err()
}

func lifecycleCandidateTime(value any, unixNano bool) time.Time {
	if unixNano {
		if timestamp, ok := value.(int64); ok {
			return time.Unix(0, timestamp).UTC()
		}
		return time.Time{}
	}
	switch timestamp := value.(type) {
	case string:
		parsed, _ := time.Parse(time.RFC3339Nano, timestamp)
		return parsed
	case []byte:
		parsed, _ := time.Parse(time.RFC3339Nano, string(timestamp))
		return parsed
	default:
		return time.Time{}
	}
}
