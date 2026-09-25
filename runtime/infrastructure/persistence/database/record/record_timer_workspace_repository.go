package record

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// ListDueRecordTimerWorkspaces returns the workspace partitions with timer
// work due for claiming. Record Timer owns the scheduling semantics; this
// persistence adapter only performs the partition query.
func (r RecordStore) ListDueRecordTimerWorkspaces(ctx context.Context, object definitionmodel.ObjectSchema, now time.Time) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(object.Key) == "" {
		return nil, fmt.Errorf("record workspace object key is required")
	}
	s := r.store
	nowValue := now.UTC().UnixMilli()
	predicate := query.Or(
		query.And(query.Equal("status", "scheduled"), query.LessThanOrEqual("due_at", nowValue)),
		query.And(query.Equal("status", "leased"), query.LessThanOrEqual("due_at", nowValue), query.LessThanOrEqual("lease_expires_at", nowValue)),
	)
	queryValue, args, buildErr := query.NewSelectBuilder(s.SQLRenderer, object.Key).Columns("workspace_id").Distinct().Where(predicate).OrderBy(query.Ascending("workspace_id")).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := r.database().QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, fmt.Errorf("list record workspaces: %w", err)
	}
	defer rows.Close()
	workspaces := []string{}
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		if workspaceID = strings.TrimSpace(workspaceID); workspaceID != "" {
			workspaces = append(workspaces, workspaceID)
		}
	}
	return workspaces, rows.Err()
}
