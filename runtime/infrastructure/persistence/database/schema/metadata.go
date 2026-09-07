package schema

import (
	"context"
	"fmt"
)

func EnsureApplicationSchema(ctx context.Context, s Store) error {
	documentText := "TEXT"
	if s.Driver() == "mysql" {
		// Change Plans and canonical resource definitions are complete system
		// documents, not short labels. MySQL TEXT is capped at 64 KiB while
		// SQLite/PostgreSQL TEXT is effectively unbounded for this use case.
		documentText = "LONGTEXT"
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_application_schema_projection")+" ("+
		s.Identifier("id")+" "+s.ApplicationSchemaIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("contract_version")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("source_hash")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("schema_hash")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("artifact_version")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("materializer_version")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("status")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("template_id")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("default_locale")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("name")+" TEXT NOT NULL DEFAULT '', "+
		s.Identifier("materialized_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '')"); err != nil {
		return fmt.Errorf("create application schema projection: %w", err)
	}
	if err := EnsureApplicationTimeZoneSchema(ctx, s); err != nil {
		return err
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_application_schema_seed_checkpoints")+" ("+
		s.Identifier("key")+" "+s.ApplicationSchemaIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("value")+" "+documentText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create application schema seed checkpoints: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_application_schema_exact_decimal_migration_receipts")+" ("+
		s.Identifier("id")+" "+s.ApplicationSchemaIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("contract_version")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("object_key")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("column_keys")+" "+documentText+" NOT NULL, "+
		s.Identifier("from_types")+" "+documentText+" NOT NULL, "+
		s.Identifier("to_types")+" "+documentText+" NOT NULL, "+
		s.Identifier("row_count")+" BIGINT NOT NULL, "+
		s.Identifier("before_hash")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("after_hash")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("applied_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create _application_schema_exact_decimal_migration_receipts: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_record_localized_values")+" ("+
		s.Identifier("workspace_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("object_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("record_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("field_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("locale")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("text_value")+" TEXT NOT NULL, "+
		s.Identifier("created_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create _record_localized_values: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "_record_localized_values", "uniq_business_record_localized_value", true,
		"workspace_id", "object_key", "record_id", "field_key", "locale"); err != nil {
		return fmt.Errorf("create business record localized value unique index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "_record_localized_values", "idx_business_record_localized_search", false,
		"workspace_id", "object_key", "locale", "field_key", "record_id"); err != nil {
		return fmt.Errorf("create business record localized search index: %w", err)
	}
	return nil
}
