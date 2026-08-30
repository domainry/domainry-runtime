package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func EnsureApplicationSchema(ctx context.Context, s Store) error {
	documentText := "TEXT"
	if s.Driver() == "mysql" {
		// Change Plans and canonical resource definitions are complete system
		// documents, not short labels. MySQL TEXT is capped at 64 KiB while
		// SQLite/PostgreSQL TEXT is effectively unbounded for this use case.
		documentText = "LONGTEXT"
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_runtime_metadata_projection")+" ("+
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
		return fmt.Errorf("create runtime metadata projection: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("_runtime_seed_checkpoints")+" ("+
		s.Identifier("key")+" "+s.ApplicationSchemaIDColumnType()+" PRIMARY KEY, "+
		s.Identifier("value")+" "+documentText+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create runtime seed checkpoints: %w", err)
	}
	if err := migrateLegacyMetadataCatalog(ctx, s); err != nil {
		return err
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("application_schema_exact_decimal_migrations")+" ("+
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
		return fmt.Errorf("create application_schema_exact_decimal_migrations: %w", err)
	}
	if err := migrateLegacyApplicationSchemaTables(ctx, s); err != nil {
		return err
	}
	for _, table := range metadataDefinitionTables() {
		if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier(table)+" ("+
			s.Identifier("id")+" "+s.ApplicationSchemaIDColumnType()+" PRIMARY KEY, "+
			s.Identifier("resource_key")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL UNIQUE, "+
			s.Identifier("object_key")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
			s.Identifier("name")+" TEXT NOT NULL, "+
			s.Identifier("payload_json")+" "+documentText+" NOT NULL, "+
			s.Identifier("schema_version")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
			s.Identifier("schema_hash")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
			s.Identifier("source_kind")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
			s.Identifier("source_id")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
			s.Identifier("disabled_at")+" "+s.ApplicationSchemaIDColumnType()+", "+
			s.Identifier("created_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
			s.Identifier("updated_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL)"); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	if err := migrateLegacyIntegrationRequirements(ctx, s); err != nil {
		return err
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("business_localized_text")+" ("+
		s.Identifier("id")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("workspace_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("entity_type")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("entity_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("property")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("locale")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("text")+" TEXT NOT NULL, "+
		s.Identifier("source_kind")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("source_id")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("created_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create business_localized_text: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_localized_text", "uniq_business_localized_text_key", true,
		"workspace_id", "entity_type", "entity_key", "property", "locale"); err != nil {
		return fmt.Errorf("create business_localized_text unique index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_localized_text", "uniq_business_localized_text_workspace_identity", true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create business_localized_text workspace identity: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_localized_text", "idx_business_localized_text_entity", false,
		"entity_type", "entity_key"); err != nil {
		return fmt.Errorf("create business_localized_text entity index: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier("business_record_localized_value")+" ("+
		s.Identifier("workspace_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("object_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("record_id")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("field_key")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("locale")+" "+s.LocalizedTextKeyColumnType()+" NOT NULL, "+
		s.Identifier("text_value")+" TEXT NOT NULL, "+
		s.Identifier("created_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL, "+
		s.Identifier("updated_at")+" "+s.ApplicationSchemaIDColumnType()+" NOT NULL)"); err != nil {
		return fmt.Errorf("create business_record_localized_value: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_record_localized_value", "uniq_business_record_localized_value", true,
		"workspace_id", "object_key", "record_id", "field_key", "locale"); err != nil {
		return fmt.Errorf("create business record localized value unique index: %w", err)
	}
	if err := s.CreateIndexIfMissing(ctx, "business_record_localized_value", "idx_business_record_localized_search", false,
		"workspace_id", "object_key", "locale", "field_key", "record_id"); err != nil {
		return fmt.Errorf("create business record localized search index: %w", err)
	}
	return nil
}

func migrateLegacyMetadataCatalog(ctx context.Context, s Store) error {
	values := map[string]string{}
	updatedAt := ""
	legacyTables := []string{}
	for _, table := range []string{"metadata_catalog", "application_schema_catalog"} {
		exists, err := runtimeSchemaTableExists(ctx, s, table)
		if errors.Is(err, sql.ErrNoRows) {
			exists, err = false, nil
		}
		if err != nil {
			return fmt.Errorf("inspect legacy metadata catalog %s: %w", table, err)
		}
		if !exists {
			continue
		}
		legacyTables = append(legacyTables, table)
		query, args, err := ormbuilder.NewSelectBuilder(s.RuntimeRenderer(), table).Columns("key", "value", "updated_at").Build()
		if err != nil {
			return fmt.Errorf("build legacy metadata catalog read: %w", err)
		}
		rows, err := s.SchemaDB().QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("read legacy metadata catalog %s: %w", table, err)
		}
		for rows.Next() {
			var key, value, rowUpdatedAt string
			if err := rows.Scan(&key, &value, &rowUpdatedAt); err != nil {
				_ = rows.Close()
				return fmt.Errorf("scan legacy metadata catalog %s: %w", table, err)
			}
			values[key] = value
			if rowUpdatedAt > updatedAt {
				updatedAt = rowUpdatedAt
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	if len(legacyTables) == 0 {
		return nil
	}
	if len(values) == 0 {
		for _, table := range legacyTables {
			// domainry-orm has no DROP TABLE builder; identifiers are host-owned.
			if _, err := s.SchemaDB().ExecContext(ctx, "DROP TABLE "+s.TableIdentifier(table)); err != nil {
				return fmt.Errorf("drop empty legacy metadata catalog %s: %w", table, err)
			}
		}
		return nil
	}
	tx, err := s.SchemaDB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy metadata projection migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	deleteProjection, deleteArgs, err := ormbuilder.NewDeleteBuilder(s.RuntimeRenderer(), "_runtime_metadata_projection").Where(ormbuilder.Equal("id", "current")).Build()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, deleteProjection, deleteArgs...); err != nil {
		return fmt.Errorf("replace legacy metadata projection: %w", err)
	}
	insertProjection, projectionArgs, err := ormbuilder.NewInsertBuilder(s.RuntimeRenderer(), "_runtime_metadata_projection").Columns("id", "contract_version", "source_hash", "schema_hash", "artifact_version", "materializer_version", "status", "template_id", "default_locale", "name", "materialized_at").Values("current", values["schema_version"], "", values["schema_hash"], values["template_version"], "runtime-materializer-v1", "materialized", values["template_id"], values["default_locale"], values["name"], updatedAt).Build()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, insertProjection, projectionArgs...); err != nil {
		return fmt.Errorf("migrate legacy metadata projection: %w", err)
	}
	for _, key := range []string{"identity_seed_synced_version", "organization_scope_seed_state"} {
		value, found := values[key]
		if !found {
			continue
		}
		deleteCheckpoint, deleteArgs, err := ormbuilder.NewDeleteBuilder(s.RuntimeRenderer(), "_runtime_seed_checkpoints").Where(ormbuilder.Equal("key", key)).Build()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, deleteCheckpoint, deleteArgs...); err != nil {
			return err
		}
		insertCheckpoint, insertArgs, err := ormbuilder.NewInsertBuilder(s.RuntimeRenderer(), "_runtime_seed_checkpoints").Columns("key", "value", "updated_at").Values(key, value, updatedAt).Build()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, insertCheckpoint, insertArgs...); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy metadata projection migration: %w", err)
	}
	for _, table := range legacyTables {
		// domainry-orm has no DROP TABLE builder; identifiers are host-owned.
		if _, err := s.SchemaDB().ExecContext(ctx, "DROP TABLE "+s.TableIdentifier(table)); err != nil {
			return fmt.Errorf("drop legacy metadata catalog %s: %w", table, err)
		}
	}
	return nil
}

func migrateLegacyApplicationSchemaTables(ctx context.Context, s Store) error {
	// domainry-orm has no INSERT ... SELECT or DROP TABLE builder. These bounded,
	// dialect-neutral statements preserve legacy host data while changing table
	// ownership; identifiers are supplied exclusively by the host renderer.
	for _, migration := range []struct {
		legacy, target string
		columns        []string
	}{
		{legacy: "metadata_exact_decimal_migrations", target: "application_schema_exact_decimal_migrations", columns: []string{"id", "contract_version", "object_key", "column_keys", "from_types", "to_types", "row_count", "before_hash", "after_hash", "applied_at"}},
	} {
		exists, err := runtimeSchemaTableExists(ctx, s, migration.legacy)
		if errors.Is(err, sql.ErrNoRows) {
			exists, err = false, nil
		}
		if err != nil {
			return fmt.Errorf("inspect legacy %s: %w", migration.legacy, err)
		}
		if !exists {
			continue
		}
		columns := quotedMetadataMigrationColumns(s, migration.columns)
		key := s.Identifier(migration.columns[0])
		if _, err := s.SchemaDB().ExecContext(ctx, "DELETE FROM "+s.TableIdentifier(migration.target)+" WHERE "+key+" IN (SELECT "+key+" FROM "+s.TableIdentifier(migration.legacy)+")"); err != nil {
			return fmt.Errorf("prepare legacy %s copy: %w", migration.legacy, err)
		}
		if _, err := s.SchemaDB().ExecContext(ctx, "INSERT INTO "+s.TableIdentifier(migration.target)+" ("+columns+") SELECT "+columns+" FROM "+s.TableIdentifier(migration.legacy)); err != nil {
			return fmt.Errorf("copy legacy %s: %w", migration.legacy, err)
		}
		if _, err := s.SchemaDB().ExecContext(ctx, "DROP TABLE "+s.TableIdentifier(migration.legacy)); err != nil {
			return fmt.Errorf("retire legacy %s: %w", migration.legacy, err)
		}
	}
	return nil
}

func quotedMetadataMigrationColumns(s Store, columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = s.Identifier(column)
	}
	return strings.Join(quoted, ", ")
}

func metadataDefinitionTables() []string {
	return []string{
		"workflow_definitions",
		"automation_rule_definitions",
		"application_connector_requirements",
		"application_integration_event_mapping_requirements",
	}
}
