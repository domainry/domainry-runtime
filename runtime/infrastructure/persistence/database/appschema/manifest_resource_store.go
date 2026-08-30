package appschema

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
)

func (s ApplicationSchemaStore) insertMetadataResource(ctx context.Context, tx *sql.Tx, seed metadataResourceSeed, now string) error {
	raw, hash, err := metadataPayload(seed.Payload)
	if err != nil {
		return fmt.Errorf("encode %s %s: %w", seed.ResourceType, seed.Key, err)
	}
	columns := []string{"id", "resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at"}
	values := []any{
		metadataResourceID(seed.ResourceType, seed.Key),
		seed.Key,
		seed.ObjectKey,
		seed.Name,
		string(raw),
		seed.SchemaVersion,
		hash,
		seed.SourceKind,
		seed.SourceID,
		nil,
		now,
		now,
	}
	insert := ormbuilder.NewInsertBuilder(s.store.SQLRenderer, seed.Table).Columns(columns...).Values(values...)
	query, args, buildErr := insert.Build()
	if buildErr != nil {
		return fmt.Errorf("build %s %s insert: %w", seed.ResourceType, seed.Key, buildErr)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert %s %s: %w", seed.ResourceType, seed.Key, err)
	}
	return nil
}

func (s ApplicationSchemaStore) syncMetadataResource(ctx context.Context, tx *sql.Tx, seed metadataResourceSeed, now string) error {
	raw, hash, err := metadataPayload(seed.Payload)
	if err != nil {
		return fmt.Errorf("encode %s %s: %w", seed.ResourceType, seed.Key, err)
	}
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, seed.Table).Columns("schema_hash", "source_kind", "disabled_at").Where(ormbuilder.Equal("resource_key", seed.Key)).Build()
	if buildErr != nil {
		return fmt.Errorf("build %s %s sync lookup: %w", seed.ResourceType, seed.Key, buildErr)
	}
	var currentHash string
	var sourceKind string
	var disabledAt sql.NullString
	err = tx.QueryRowContext(ctx, query, args...).Scan(&currentHash, &sourceKind, &disabledAt)
	if err == sql.ErrNoRows {
		return s.insertMetadataResource(ctx, tx, seed, now)
	}
	if err != nil {
		return fmt.Errorf("read %s %s for sync: %w", seed.ResourceType, seed.Key, err)
	}
	if strings.TrimSpace(sourceKind) != "generated" || disabledAt.Valid {
		return nil
	}
	if currentHash == hash {
		return nil
	}
	updateQuery, updateArgs, buildErr := ormbuilder.NewUpdateBuilder(s.store.SQLRenderer, seed.Table).
		Set("object_key", seed.ObjectKey).Set("name", seed.Name).Set("payload_json", string(raw)).Set("schema_version", seed.SchemaVersion).
		Set("schema_hash", hash).Set("source_id", seed.SourceID).Set("updated_at", now).Where(ormbuilder.Equal("resource_key", seed.Key)).Build()
	if buildErr != nil {
		return fmt.Errorf("build %s %s sync update: %w", seed.ResourceType, seed.Key, buildErr)
	}
	if _, err := tx.ExecContext(ctx, updateQuery, updateArgs...); err != nil {
		return fmt.Errorf("sync %s %s: %w", seed.ResourceType, seed.Key, err)
	}
	return nil
}
