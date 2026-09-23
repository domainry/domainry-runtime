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
		s.Identifier("id")+" "+s.ApplicationSchemaIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("schema_version")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("model_hash")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("catalog_hash")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("project_key")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("default_locale")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("time_zone")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT 'UTC', "+
		s.Identifier("name")+" "+s.RuntimeColumnDefinition("TEXT NOT NULL DEFAULT ''")+", "+
		s.Identifier("initialized_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '', "+
		s.Identifier("catalog_updated_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT '')"); err != nil {
		return fmt.Errorf("create project model state: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_record_localized_values")+" ("+
		s.Identifier("workspace_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("object_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("record_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("field_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("locale")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("text_value")+" TEXT NOT NULL, "+
		s.Identifier("created_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		"PRIMARY KEY ("+s.Identifier("workspace_id")+", "+s.Identifier("object_key")+", "+s.Identifier("record_id")+", "+s.Identifier("field_key")+", "+s.Identifier("locale")+"))"); err != nil {
		return fmt.Errorf("create _record_localized_values: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "_record_localized_values", "idx_business_record_localized_search", false,
		"workspace_id", "object_key", "locale", "field_key", "record_id"); err != nil {
		return fmt.Errorf("create business record localized search index: %w", err)
	}
	return nil
}
