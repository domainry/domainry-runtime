package schema

import (
	"context"
	"fmt"
)

func EnsureApplicationSchema(ctx context.Context, s Store) error {
	return EnsureApplicationSchemaFor(ctx, s, true)
}

func EnsureApplicationSchemaFor(ctx context.Context, s Store, _ bool) error {
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_project_model_state")+" ("+
		quotedColumnDefinitions(s, []string{
			physicalAutoIncrementID(s.Driver()),
			"state_key " + s.ApplicationSchemaIDColumnType() + " NOT NULL",
			"schema_version " + s.ApplicationSchemaIDColumnType() + " NOT NULL DEFAULT ''",
			"model_hash " + s.ApplicationSchemaIDColumnType() + " NOT NULL DEFAULT ''",
			"catalog_hash " + s.ApplicationSchemaIDColumnType() + " NOT NULL DEFAULT ''",
			"project_key " + s.ApplicationSchemaIDColumnType() + " NOT NULL DEFAULT ''",
			"default_locale " + s.ApplicationSchemaIDColumnType() + " NOT NULL DEFAULT ''",
			"time_zone " + s.ApplicationSchemaIDColumnType() + " NOT NULL DEFAULT 'UTC'",
			"name TEXT NOT NULL DEFAULT ''",
			"initialized_at BIGINT NOT NULL DEFAULT 0",
			"catalog_updated_at BIGINT NOT NULL DEFAULT 0",
		})+")"); err != nil {
		return fmt.Errorf("create project model state: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "_project_model_state", "uniq_project_model_state_key", true, "state_key"); err != nil {
		return fmt.Errorf("create project model state identity: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_record_localized_values")+" ("+
		quotedColumnDefinitions(s, []string{
			physicalAutoIncrementID(s.Driver()),
			"workspace_id " + s.LocalizedTextKeyColumnType() + " NOT NULL",
			"object_key " + s.LocalizedTextKeyColumnType() + " NOT NULL",
			"record_id " + s.LocalizedTextKeyColumnType() + " NOT NULL",
			"field_key " + s.LocalizedTextKeyColumnType() + " NOT NULL",
			"locale " + s.LocalizedTextKeyColumnType() + " NOT NULL",
			"text_value TEXT NOT NULL",
			"created_at BIGINT NOT NULL",
			"updated_at BIGINT NOT NULL",
		})+")"); err != nil {
		return fmt.Errorf("create _record_localized_values: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "_record_localized_values", "uniq_record_localized_value", true,
		"workspace_id", "object_key", "record_id", "field_key", "locale"); err != nil {
		return fmt.Errorf("create localized value identity: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "_record_localized_values", "idx_business_record_localized_search", false,
		"workspace_id", "object_key", "locale", "field_key", "record_id"); err != nil {
		return fmt.Errorf("create business record localized search index: %w", err)
	}
	return nil
}
