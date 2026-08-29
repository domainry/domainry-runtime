package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r MetadataStore) refreshCatalogHashTx(ctx context.Context, tx *sql.Tx, now string) error {
	return r.refreshCatalogHashWithExecutorAt(ctx, tx, now)
}

func (r MetadataStore) refreshCatalogHashWithExecutor(
	ctx context.Context,
	executor database.ActionExecutionExecutor,
) error {
	return r.refreshCatalogHashWithExecutorAt(
		ctx,
		executor,
		time.Now().UTC().Format(time.RFC3339),
	)
}

func (r MetadataStore) refreshCatalogHashWithExecutorAt(
	ctx context.Context,
	executor database.ActionExecutionExecutor,
	now string,
) error {
	tables := metadataCatalogDefinitionTables()
	hash := sha256.New()
	for _, table := range tables {
		query, args, err := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, table).
			Columns("resource_key", "schema_hash").OrderBy(ormbuilder.Ascending("resource_key")).Build()
		if err != nil {
			return err
		}
		rows, err := executor.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		hash.Write([]byte(table + ":"))
		for rows.Next() {
			var key, schemaHash string
			if err := rows.Scan(&key, &schemaHash); err != nil {
				rows.Close()
				return err
			}
			hash.Write([]byte(key + ":" + schemaHash + "|"))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	value := hex.EncodeToString(hash.Sum(nil))
	insert := ormbuilder.NewInsertBuilder(r.store.SQLRenderer, "metadata_catalog").
		Columns("key", "value", "updated_at").Values("schema_hash", value, now)
	query, args, err := r.store.Engine.ApplyUpsert(insert, []string{"key"}, "value", "updated_at").Build()
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, query, args...)
	return err
}

func (r MetadataStore) refreshCatalogHash(ctx context.Context) error {
	return r.refreshCatalogHashWithExecutor(ctx, r.database())
}

func metadataCatalogDefinitionTables() []string {
	return []string{"object_definitions", "field_definitions", "validation_definitions", "view_definitions", "action_definitions", "workflow_definitions", "scheduler_definitions", "automation_rule_definitions", "preference_definitions", "rule_set_definitions", "report_definitions", "operation_state_example_definitions", "sensitive_field_policy_definitions", "report_export_control_definitions", "identity_profile_binding_definitions", "surface_definitions", "component_definitions", "entrypoint_definitions", "dictionary_definitions", "connector_definitions", "integration_event_mapping_definitions", "skill_definitions", "agent_definitions", "agent_task_definitions", "agent_entrypoint_definitions", "agent_service_principal_definitions"}
}
