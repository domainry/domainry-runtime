package appschema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r ApplicationSchemaStore) refreshCatalogHashWithExecutor(ctx context.Context, executor database.ActionExecutionExecutor) error {
	return r.refreshCatalogHashWithExecutorAt(ctx, executor, time.Now().UTC().Format(time.RFC3339))
}

func (r ApplicationSchemaStore) refreshCatalogHashWithExecutorAt(ctx context.Context, executor database.ActionExecutionExecutor, now string) error {
	definitions, err := r.metadataDefinitions()
	if err != nil {
		return err
	}
	snapshot, err := definitions.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("load Metadata definition snapshot: %w", err)
	}
	hash := sha256.New()
	hash.Write([]byte("metadata:"))
	statement, args, err := query.NewSelectBuilder(r.store.SQLRenderer, "_application_schema_projection").
		Columns("time_zone").Where(query.Equal("id", "current")).Build()
	if err != nil {
		return err
	}
	zone := "UTC"
	if err := executor.QueryRowContext(ctx, statement, args...).Scan(&zone); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load application time zone for catalog hash: %w", err)
	}
	hash.Write([]byte("time_zone:" + zone + "|"))
	for _, definition := range snapshot.Definitions {
		hash.Write([]byte(definition.ResourceType + ":" + definition.ResourceKey + ":" + definition.SchemaHash + "|"))
	}
	value := hex.EncodeToString(hash.Sum(nil))
	insert := query.NewInsertBuilder(r.store.SQLRenderer, "_application_schema_projection").
		Columns("id", "schema_hash", "materialized_at").Values("current", value, now)
	insert, err = r.store.Engine.ApplyUpsert(insert, []string{"id"},
		query.AssignExpression("schema_hash", query.InsertedValue("schema_hash")),
		query.AssignExpression("materialized_at", query.InsertedValue("materialized_at")),
	)
	if err != nil {
		return err
	}
	queryValue, args, err := insert.Build()
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, queryValue, args...)
	return err
}
