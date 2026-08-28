package metadata

import auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

func (s MetadataStore) insertMetadataChangeAudit(ctx context.Context, tx *sql.Tx, event auditmodel.AuditEvent) error {
	before, err := json.Marshal(event.Before)
	if err != nil {
		return fmt.Errorf("encode audit before: %w", err)
	}
	after, err := json.Marshal(event.After)
	if err != nil {
		return fmt.Errorf("encode audit after: %w", err)
	}
	metadata, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	columns := []string{"id", "workspace_id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at"}
	values := []any{event.ID, event.WorkspaceID, event.Event, event.ObjectKey, event.RecordID, event.ActorID, event.RoleKey, event.Summary, string(metadata), string(before), string(after), event.CreatedAt}
	query := "INSERT INTO " + s.store.TableIdentifier("_audit_events") + " (" + strings.Join(quotedColumns(s.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert metadata change audit: %w", err)
	}
	return nil
}

func (s MetadataStore) nextMetadataSchemaVersionTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string) (string, error) {
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("metadata_definition_versions") + " WHERE " + s.store.Identifier("resource_type") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("resource_key") + " = " + s.store.Placeholder(2)
	var count int
	if err := tx.QueryRowContext(ctx, query, resourceType, resourceKey).Scan(&count); err != nil {
		return "", fmt.Errorf("read metadata version count: %w", err)
	}
	return fmt.Sprintf("%d", count+1), nil
}

func (s MetadataStore) insertMetadataDefinitionVersionTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey, version, hash string, payload []byte, now string) error {
	columns := []string{"id", "resource_type", "resource_key", "schema_version", "schema_hash", "payload_json", "created_at"}
	values := []any{metadataResourceID(resourceType+":version", resourceKey+":"+version+":"+metadataHashPrefix(hash)), resourceType, resourceKey, version, hash, string(payload), now}
	query := "INSERT INTO " + s.store.TableIdentifier("metadata_definition_versions") + " (" + strings.Join(quotedColumns(s.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert %s %s version: %w", resourceType, resourceKey, err)
	}
	return nil
}

func (s MetadataStore) currentMetadataHashTx(ctx context.Context, tx *sql.Tx, table, resourceKey string) string {
	var current string
	_ = tx.QueryRowContext(ctx, "SELECT "+s.store.Identifier("schema_hash")+" FROM "+s.store.TableIdentifier(table)+" WHERE "+s.store.Identifier("resource_key")+" = "+s.store.Placeholder(1), resourceKey).Scan(&current)
	return current
}

func metadataMutationValueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
