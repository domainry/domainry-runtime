package lifecycle

import (
	"context"
	"database/sql"
	"time"
)

func (e OwnerExecutor) previewSpec(ctx context.Context, workspaceID string, spec cleanupSpec, policyKey string, cutoff time.Time) (int64, time.Time, error) {
	where, args := cleanupWhere(e.store, spec, workspaceID, cutoff, 1)
	archiveWorkspace := ""
	if spec.tenantColumn != "" {
		archiveWorkspace = e.store.Identifier(spec.table) + "." + e.store.Identifier(spec.tenantColumn)
	} else {
		archiveWorkspace = e.store.Placeholder(len(args) + 1)
		args = append(args, workspaceID)
	}
	where += " AND NOT EXISTS (SELECT 1 FROM " + e.store.TableIdentifier("lifecycle_archive_entries") + " a WHERE a." + e.store.Identifier("workspace_id") + " = " + archiveWorkspace + " AND a." + e.store.Identifier("source_table") + " = " + e.store.Placeholder(len(args)+1) + " AND a." + e.store.Identifier("resource_id") + " = " + e.store.Identifier(spec.table) + "." + e.store.Identifier(spec.idColumn) + " AND a." + e.store.Identifier("policy_key") + " = " + e.store.Placeholder(len(args)+2) + ")"
	args = append(args, spec.table, policyKey)
	query := "SELECT COUNT(*), MIN(" + e.store.Identifier(spec.timeColumn) + ") FROM " + e.store.TableIdentifier(spec.table) + " WHERE " + where
	var count int64
	parsed := time.Time{}
	if spec.unixNanoTime {
		var oldest sql.NullInt64
		if err := e.database().QueryRowContext(ctx, query, args...).Scan(&count, &oldest); err != nil {
			return 0, time.Time{}, err
		}
		if oldest.Valid {
			parsed = time.Unix(0, oldest.Int64).UTC()
		}
	} else {
		var oldest sql.NullString
		if err := e.database().QueryRowContext(ctx, query, args...).Scan(&count, &oldest); err != nil {
			return 0, time.Time{}, err
		}
		if oldest.Valid {
			parsed, _ = time.Parse(time.RFC3339Nano, oldest.String)
		}
	}
	return count, parsed, nil
}
