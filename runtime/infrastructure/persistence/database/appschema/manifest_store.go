package appschema

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s ApplicationSchemaStore) SyncManifestProjection(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) error {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return err
	}
	return s.syncManifestProjection(ctx, manifest, "sync")
}

func (s ApplicationSchemaStore) syncManifestProjection(ctx context.Context, manifest manifestmodel.ManifestSchema, operation string) error {
	if len(manifest.Objects) == 0 {
		return fmt.Errorf("manifest metadata %s has no objects", operation)
	}
	ctx = manifestMetadataContext(ctx)
	tx, err := s.database().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata %s: %w", operation, err)
	}
	defer tx.Rollback()
	ctx = metadatamodulehost.WithExecutor(ctx, tx)
	if err := s.syncMetadataModuleDefinitions(ctx, manifest); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.upsertMetadataProjection(ctx, tx, manifest, now); err != nil {
		return err
	}
	if err := s.refreshCatalogHashWithExecutorAt(ctx, tx, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metadata %s: %w", operation, err)
	}
	return nil
}

func (s ApplicationSchemaStore) upsertMetadataProjection(ctx context.Context, tx *sql.Tx, manifest manifestmodel.ManifestSchema, now string) error {
	_, sourceHash, err := metadataPayload(manifest)
	if err != nil {
		return fmt.Errorf("hash metadata source: %w", err)
	}
	contractVersion := strings.TrimSpace(manifest.SchemaVersion)
	if contractVersion == "" {
		contractVersion = "manifest-v1"
	}
	columns := []string{"id", "contract_version", "source_hash", "schema_hash", "artifact_version", "materializer_version", "status", "template_id", "default_locale", "name", "materialized_at"}
	values := []any{"current", contractVersion, sourceHash, "", strings.TrimSpace(manifest.Version), "runtime-materializer-v1", "materialized", strings.TrimSpace(manifest.TemplateID), manifestDefaultLocale(manifest), strings.TrimSpace(manifest.Name), now}
	insert := query.NewInsertBuilder(s.store.SQLRenderer, "_application_schema_projection").Columns(columns...).Values(values...)
	assignments := make([]query.Assignment, 0, len(columns)-1)
	for _, column := range columns[1:] {
		assignments = append(assignments, query.AssignExpression(column, query.InsertedValue(column)))
	}
	insert, err = s.store.Engine.ApplyUpsert(insert, []string{"id"}, assignments...)
	if err != nil {
		return fmt.Errorf("build metadata projection upsert: %w", err)
	}
	statement, args, err := insert.Build()
	if err != nil {
		return fmt.Errorf("build metadata projection: %w", err)
	}
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("upsert metadata projection: %w", err)
	}
	return nil
}

func manifestMetadataContext(ctx context.Context) context.Context {
	if requestcontext.WorkspaceID(ctx) != "" {
		return ctx
	}
	return requestcontext.WithWorkspaceID(ctx, principalmodel.InstallationWorkspaceID)
}

func manifestGeneratedSourceID(manifest manifestmodel.ManifestSchema) string {
	if sourceID := strings.TrimSpace(manifest.TemplateID); sourceID != "" {
		return sourceID
	}
	return "generated-template"
}
