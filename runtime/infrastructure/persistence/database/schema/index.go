package schema

import (
	"context"
	"fmt"
	"strings"
)

func CreateIndexIfMissing(ctx context.Context, s Store, table, index string, unique bool, columns ...string) error {
	existing, err := tableIndexes(ctx, s, table)
	if err != nil {
		return err
	}
	if existing[index] {
		return nil
	}
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, s.Identifier(column))
	}
	prefix := "CREATE INDEX IF NOT EXISTS "
	if unique {
		prefix = "CREATE UNIQUE INDEX IF NOT EXISTS "
	}
	if s.Driver() == "mysql" {
		prefix = "CREATE INDEX "
		if unique {
			prefix = "CREATE UNIQUE INDEX "
		}
	}
	query := prefix + s.Identifier(index) + " ON " + s.TableIdentifier(table) + " (" + strings.Join(quoted, ", ") + ")"
	if _, err := s.SchemaDB().ExecContext(ctx, query); err != nil {
		return fmt.Errorf("create index %s: %w", index, err)
	}
	return nil
}

func tableIndexes(ctx context.Context, s Store, table string) (map[string]bool, error) {
	out := map[string]bool{}
	query := "SELECT name FROM sqlite_master WHERE type='index' AND tbl_name=?"
	if s.Driver() == "mysql" {
		query = "SELECT DISTINCT INDEX_NAME FROM information_schema.STATISTICS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ?"
	} else if s.Driver() == "postgres" {
		query = "SELECT indexname FROM pg_indexes WHERE schemaname = " + s.Placeholder(1) + " AND tablename = " + s.Placeholder(2)
	}
	args := []any{table}
	if s.Driver() == "postgres" {
		args = []any{s.DatabaseSchema(), table}
	}
	rows, err := s.SchemaDB().QueryContext(ctx, query, args...)
	if err != nil {
		return out, fmt.Errorf("list indexes for %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan index for %s: %w", table, err)
		}
		out[name] = true
	}
	return out, rows.Err()
}
