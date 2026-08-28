package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"time"

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
		rows, err := executor.QueryContext(ctx, "SELECT "+stringsJoinIdentifiers(r.store, "resource_key", "schema_hash")+" FROM "+r.store.TableIdentifier(table)+" ORDER BY "+r.store.Identifier("resource_key")+" ASC")
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
	query := metadataCatalogHashWriteSQL(r.store, r.store.Driver())
	_, err := executor.ExecContext(ctx, query, "schema_hash", value, now)
	return err
}

func (r MetadataStore) refreshCatalogHash(ctx context.Context) error {
	return r.refreshCatalogHashWithExecutor(ctx, r.database())
}

func metadataCatalogDefinitionTables() []string {
	return []string{"object_definitions", "field_definitions", "validation_definitions", "view_definitions", "action_definitions", "workflow_definitions", "scheduler_definitions", "automation_rule_definitions", "preference_definitions", "rule_set_definitions", "report_definitions", "operation_state_example_definitions", "sensitive_field_policy_definitions", "report_export_control_definitions", "identity_profile_binding_definitions", "surface_definitions", "component_definitions", "entrypoint_definitions", "dictionary_definitions", "connector_definitions", "integration_event_mapping_definitions", "skill_definitions", "agent_definitions", "agent_task_definitions", "agent_entrypoint_definitions", "agent_service_principal_definitions"}
}

func metadataCatalogHashWriteSQL(store metadataSQLDialect, driver string) string {
	base := "INSERT INTO " + store.TableIdentifier("metadata_catalog") + " (" + store.Identifier("key") + ", " + store.Identifier("value") + ", " + store.Identifier("updated_at") + ") VALUES (" + store.Placeholder(1) + ", " + store.Placeholder(2) + ", " + store.Placeholder(3) + ")"
	switch driver {
	case "mysql":
		return base + " ON DUPLICATE KEY UPDATE " + store.TableIdentifier("value") + " = VALUES(" + store.Identifier("value") + "), " + store.Identifier("updated_at") + " = VALUES(" + store.Identifier("updated_at") + ")"
	case "postgres":
		return base + " ON CONFLICT (" + store.Identifier("key") + ") DO UPDATE SET " + store.Identifier("value") + " = EXCLUDED." + store.Identifier("value") + ", " + store.Identifier("updated_at") + " = EXCLUDED." + store.Identifier("updated_at")
	default:
		return strings.Replace(base, "INSERT INTO", "INSERT OR REPLACE INTO", 1)
	}
}
