package schema

import (
	"context"
	"fmt"
	ormschema "github.com/domainry/domainry-orm/schema"
)

func CreateIndexIfMissing(ctx context.Context, s Store, table, index string, unique bool, columns ...string) error {
	existing, err := tableIndexes(ctx, s, table)
	if err != nil {
		return err
	}
	if existing[index] {
		return nil
	}
	builder := ormschema.NewIndex(s.RuntimeRenderer(), index, table).Columns(columns...)
	if unique {
		builder = builder.Unique()
	}
	profile := s.RuntimeProfile()
	statement, arguments, err := profile.ApplyCreateIndex(builder).Build()
	if err != nil {
		return fmt.Errorf("build index %s: %w", index, err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, statement, arguments...); err != nil {
		if profile.IsCreateIndexAlreadyExists(err) {
			return nil
		}
		return fmt.Errorf("create index %s: %w", index, err)
	}
	return nil
}

func tableIndexes(ctx context.Context, s Store, table string) (map[string]bool, error) {
	out := map[string]bool{}
	query := s.RuntimeProfile().IndexesQuery(s.RuntimeRenderer(), s.DatabaseSchema(), table)
	if query.Statement == "" {
		return out, fmt.Errorf("database engine does not support index inspection")
	}
	rows, err := s.SchemaDB().QueryContext(ctx, query.Statement, query.Arguments...)
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
