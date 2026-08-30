package appschema

import auditmodel "github.com/domainry/domainry-audit-sdk/contract"
import auditmoduleimpl "github.com/domainry/domainry-audit/module"

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
)

func (s ApplicationSchemaStore) insertMetadataChangeAudit(ctx context.Context, tx *sql.Tx, event auditmodel.AuditEvent) error {
	return auditmoduleimpl.AppendPreparedWithin(ctx, s.store.RuntimeRenderer(), runtimeauditmodule.NewTransaction(tx), event)
}

func (s ApplicationSchemaStore) nextApplicationSchemaVersionTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey string) (string, error) {
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

func (s ApplicationSchemaStore) insertApplicationDefinitionVersionTx(ctx context.Context, tx *sql.Tx, resourceType, resourceKey, version, hash string, payload []byte, now string) error {
	query, args, buildErr := ormbuilder.NewInsertBuilder(s.store.SQLRenderer, "metadata_definition_versions").Columns("id", "resource_type", "resource_key", "schema_version", "schema_hash", "payload_json", "created_at").Values(metadataResourceID(resourceType+":version", resourceKey+":"+version+":"+metadataHashPrefix(hash)), resourceType, resourceKey, version, hash, string(payload), now).Build()
	if buildErr != nil {
		return fmt.Errorf("build %s %s version insert: %w", resourceType, resourceKey, buildErr)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert %s %s version: %w", resourceType, resourceKey, err)
	}
	return nil
}

func (s ApplicationSchemaStore) currentMetadataHashTx(ctx context.Context, tx *sql.Tx, table, resourceKey string) string {
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
