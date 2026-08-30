package schema

import (
	"context"
	"database/sql"
	"fmt"

	ormbuilder "github.com/domainry/domainry-orm/query"
)

func migrateLegacyIntegrationRequirements(ctx context.Context, s Store) error {
	for _, migration := range []struct{ source, target string }{
		{"connector_definitions", "application_connector_requirements"},
		{"integration_event_mapping_definitions", "application_integration_event_mapping_requirements"},
	} {
		exists, err := runtimeSchemaTableExists(ctx, s, migration.source)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := copyLegacyIntegrationRequirements(ctx, s, migration.source, migration.target); err != nil {
			return err
		}
	}
	return nil
}

func copyLegacyIntegrationRequirements(ctx context.Context, s Store, source, target string) error {
	columns := []string{"id", "resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}
	query, args, err := ormbuilder.NewSelectBuilder(s.RuntimeRenderer(), source).Columns(columns...).Where(ormbuilder.NotEqual("source_kind", "provider")).OrderBy(ormbuilder.Ascending("resource_key")).Build()
	if err != nil {
		return fmt.Errorf("build legacy Integration requirement query: %w", err)
	}
	rows, err := s.SchemaDB().QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("read legacy Integration requirements from %s: %w", source, err)
	}
	defer rows.Close()
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		var disabled sql.NullString
		for index := range values {
			pointers[index] = &values[index]
		}
		pointers[9] = &disabled
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		key := fmt.Sprint(values[1])
		lookup, lookupArgs, buildErr := ormbuilder.NewSelectBuilder(s.RuntimeRenderer(), target).Columns("id").Where(ormbuilder.Equal("resource_key", key)).Build()
		if buildErr != nil {
			return buildErr
		}
		var existing string
		if err := s.SchemaDB().QueryRowContext(ctx, lookup, lookupArgs...).Scan(&existing); err == nil {
			continue
		} else if err != sql.ErrNoRows {
			return err
		}
		if disabled.Valid {
			values[9] = disabled.String
		} else {
			values[9] = nil
		}
		insert, insertArgs, buildErr := ormbuilder.NewInsertBuilder(s.RuntimeRenderer(), target).Columns(columns...).Values(values...).Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := s.SchemaDB().ExecContext(ctx, insert, insertArgs...); err != nil {
			return fmt.Errorf("migrate Integration requirement %s into %s: %w", key, target, err)
		}
	}
	return rows.Err()
}
