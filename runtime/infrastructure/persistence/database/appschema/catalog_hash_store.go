package appschema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	metadatarepository "github.com/domainry/domainry-metadata-sdk/repository"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r ApplicationSchemaStore) refreshCatalogHashTx(ctx context.Context, tx *sql.Tx, now string) error {
	return r.refreshCatalogHashWithExecutorAt(ctx, tx, now)
}

func (r ApplicationSchemaStore) refreshCatalogHashWithExecutor(
	ctx context.Context,
	executor database.ActionExecutionExecutor,
) error {
	return r.refreshCatalogHashWithExecutorAt(
		ctx,
		executor,
		time.Now().UTC().Format(time.RFC3339),
	)
}

func (r ApplicationSchemaStore) refreshCatalogHashWithExecutorAt(
	ctx context.Context,
	executor database.ActionExecutionExecutor,
	now string,
) error {
	tables := metadataCatalogDefinitionTables()
	hash := sha256.New()
	metadataDefinitions := r.metadataDefinitions
	if metadataDefinitions == nil && r.store != nil {
		metadataDefinitions = r.store.MetadataDefinitions()
	}
	executorRepository, ok := metadataDefinitions.(metadatarepository.ExecutorSnapshotRepository)
	if !ok {
		return fmt.Errorf("Metadata executor snapshot repository is unavailable")
	}
	metadataSnapshot, err := executorRepository.DefinitionSnapshotWithExecutor(ctx, executor)
	if err != nil {
		return err
	}
	hash.Write([]byte("metadata:"))
	for _, definition := range metadataSnapshot.Definitions {
		hash.Write([]byte(definition.ResourceType + ":" + definition.Key + ":" + definition.SchemaHash + "|"))
	}
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
	insert := ormbuilder.NewInsertBuilder(r.store.SQLRenderer, "_runtime_metadata_projection").
		Columns("id", "schema_hash", "materialized_at").Values("current", value, now)
	insert, err = r.store.Engine.ApplyUpsert(insert, []string{"id"},
		ormbuilder.AssignExpression("schema_hash", ormbuilder.InsertedValue("schema_hash")),
		ormbuilder.AssignExpression("materialized_at", ormbuilder.InsertedValue("materialized_at")),
	)
	if err != nil {
		return err
	}
	query, args, err := insert.Build()
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, query, args...)
	return err
}

func (r ApplicationSchemaStore) refreshCatalogHash(ctx context.Context) error {
	return r.refreshCatalogHashWithExecutor(ctx, r.database())
}

func metadataCatalogDefinitionTables() []string {
	return []string{"workflow_definitions", "automation_rule_definitions", "application_connector_requirements", "application_integration_event_mapping_requirements"}
}
