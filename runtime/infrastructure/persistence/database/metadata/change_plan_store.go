package metadata

import auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s MetadataStore) insertMetadataChangeAudit(ctx context.Context, tx *sql.Tx, event auditmodel.AuditEvent) error {
	if strings.TrimSpace(event.WorkspaceID) == "" {
		event.WorkspaceID = principalmodel.InstallationWorkspaceID
	}
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
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "_audit_events", event.WorkspaceID).
		Columns("id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at").
		Values(event.ID, event.Event, event.ObjectKey, event.RecordID, event.ActorID, event.RoleKey, event.Summary, string(metadata), string(before), string(after), event.CreatedAt).Build()
	if buildErr != nil {
		return fmt.Errorf("build metadata change audit: %w", buildErr)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert metadata change audit: %w", err)
	}
	return nil
}

func (s MetadataStore) nextMetadataSchemaVersionTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string) (string, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "metadata_definition_versions").Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.And(ormbuilder.Equal("resource_type", resourceType), ormbuilder.Equal("resource_key", resourceKey))).Build()
	if buildErr != nil {
		return "", fmt.Errorf("build metadata version count: %w", buildErr)
	}
	var count int
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return "", fmt.Errorf("read metadata version count: %w", err)
	}
	return fmt.Sprintf("%d", count+1), nil
}

func (s MetadataStore) insertMetadataDefinitionVersionTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey, version, hash string, payload []byte, now string) error {
	query, args, buildErr := ormbuilder.NewInsertBuilder(s.store.SQLRenderer, "metadata_definition_versions").Columns("id", "resource_type", "resource_key", "schema_version", "schema_hash", "payload_json", "created_at").Values(metadataResourceID(resourceType+":version", resourceKey+":"+version+":"+metadataHashPrefix(hash)), resourceType, resourceKey, version, hash, string(payload), now).Build()
	if buildErr != nil {
		return fmt.Errorf("build %s %s version insert: %w", resourceType, resourceKey, buildErr)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert %s %s version: %w", resourceType, resourceKey, err)
	}
	return nil
}

func (s MetadataStore) currentMetadataHashTx(ctx context.Context, tx *sql.Tx, table, resourceKey string) string {
	var current string
	query, args, err := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, table).Columns("schema_hash").Where(ormbuilder.Equal("resource_key", resourceKey)).Build()
	if err == nil {
		_ = tx.QueryRowContext(ctx, query, args...).Scan(&current)
	}
	return current
}

func metadataMutationValueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
