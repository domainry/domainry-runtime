package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s MetadataStore) insertMetadataResource(ctx context.Context, tx *sql.Tx, seed metadataResourceSeed, now string) error {
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
	query := "INSERT INTO " + s.store.TableIdentifier(seed.Table) + " (" + strings.Join(quotedColumns(s.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert %s %s: %w", seed.ResourceType, seed.Key, err)
	}
	versionColumns := []string{"id", "resource_type", "resource_key", "schema_version", "schema_hash", "payload_json", "created_at"}
	versionValues := []any{
		metadataResourceID(seed.ResourceType+":version", seed.Key),
		seed.ResourceType,
		seed.Key,
		seed.SchemaVersion,
		hash,
		string(raw),
		now,
	}
	versionQuery := "INSERT INTO " + s.store.TableIdentifier("metadata_definition_versions") + " (" + strings.Join(quotedColumns(s.store, versionColumns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(versionColumns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, versionQuery, versionValues...); err != nil {
		return fmt.Errorf("insert %s %s version: %w", seed.ResourceType, seed.Key, err)
	}
	return nil
}

func (s MetadataStore) syncMetadataResource(ctx context.Context, tx *sql.Tx, seed metadataResourceSeed, now string) error {
	raw, hash, err := metadataPayload(seed.Payload)
	if err != nil {
		return fmt.Errorf("encode %s %s: %w", seed.ResourceType, seed.Key, err)
	}
	query := "SELECT " + strings.Join(quotedColumns(s.store, []string{"schema_hash", "source_kind", "disabled_at"}), ", ") +
		" FROM " + s.store.TableIdentifier(seed.Table) +
		" WHERE " + s.store.Identifier("resource_key") + " = " + s.store.Placeholder(1)
	var currentHash string
	var sourceKind string
	var disabledAt sql.NullString
	err = tx.QueryRowContext(ctx, query, seed.Key).Scan(&currentHash, &sourceKind, &disabledAt)
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
	updateQuery := "UPDATE " + s.store.TableIdentifier(seed.Table) +
		" SET " + s.store.Identifier("object_key") + " = " + s.store.Placeholder(1) +
		", " + s.store.Identifier("name") + " = " + s.store.Placeholder(2) +
		", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(3) +
		", " + s.store.Identifier("schema_version") + " = " + s.store.Placeholder(4) +
		", " + s.store.Identifier("schema_hash") + " = " + s.store.Placeholder(5) +
		", " + s.store.Identifier("source_id") + " = " + s.store.Placeholder(6) +
		", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(7) +
		" WHERE " + s.store.Identifier("resource_key") + " = " + s.store.Placeholder(8)
	if _, err := tx.ExecContext(ctx, updateQuery, seed.ObjectKey, seed.Name, string(raw), seed.SchemaVersion, hash, seed.SourceID, now, seed.Key); err != nil {
		return fmt.Errorf("sync %s %s: %w", seed.ResourceType, seed.Key, err)
	}
	versionColumns := []string{"id", "resource_type", "resource_key", "schema_version", "schema_hash", "payload_json", "created_at"}
	versionID := metadataResourceID(seed.ResourceType+":version", seed.Key+":"+seed.SchemaVersion+":"+metadataHashPrefix(hash))
	var existingResourceType string
	var existingResourceKey string
	var existingSchemaVersion string
	var existingHash string
	versionLookupQuery := "SELECT " + strings.Join(quotedColumns(s.store, []string{"resource_type", "resource_key", "schema_version", "schema_hash"}), ", ") +
		" FROM " + s.store.TableIdentifier("metadata_definition_versions") +
		" WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(1)
	err = tx.QueryRowContext(ctx, versionLookupQuery, versionID).Scan(&existingResourceType, &existingResourceKey, &existingSchemaVersion, &existingHash)
	if err == nil {
		if existingResourceType == seed.ResourceType && existingResourceKey == seed.Key && existingSchemaVersion == seed.SchemaVersion && existingHash == hash {
			return nil
		}
		return fmt.Errorf("metadata definition version id collision for %s %s", seed.ResourceType, seed.Key)
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("read synced %s %s version: %w", seed.ResourceType, seed.Key, err)
	}
	versionValues := []any{
		versionID,
		seed.ResourceType,
		seed.Key,
		seed.SchemaVersion,
		hash,
		string(raw),
		now,
	}
	versionQuery := "INSERT INTO " + s.store.TableIdentifier("metadata_definition_versions") + " (" + strings.Join(quotedColumns(s.store, versionColumns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(versionColumns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, versionQuery, versionValues...); err != nil {
		return fmt.Errorf("insert synced %s %s version: %w", seed.ResourceType, seed.Key, err)
	}
	return nil
}
