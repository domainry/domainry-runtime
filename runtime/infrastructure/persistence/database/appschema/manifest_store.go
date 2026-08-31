package appschema

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-orm/query"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var removeDeletedGeneratedAutomationRulesForManifest = func(ctx context.Context, store ApplicationSchemaStore, tx *sql.Tx, manifest manifestmodel.ManifestSchema, now string) error {
	return store.removeDeletedGeneratedAutomationRules(ctx, tx, manifest)
}

var closeGeneratedActionRows = func(rows *sql.Rows) error { return rows.Close() }

type metadataResourceSeed struct {
	ResourceType  string
	Table         string
	Key           string
	ObjectKey     string
	Name          string
	SchemaVersion string
	SourceKind    string
	SourceID      string
	Payload       any
}

func (s ApplicationSchemaStore) EnsureManifestMetadata(ctx context.Context, seed manifestmodel.ManifestSchema) error {
	if len(seed.Objects) == 0 {
		return fmt.Errorf("manifest metadata seed has no objects")
	}
	ctx = manifestMetadataContext(ctx)
	if err := s.syncMetadataModuleDefinitions(ctx, seed); err != nil {
		return err
	}
	seeded, err := s.manifestMetadataSeeded(ctx)
	if err != nil {
		return err
	}
	if seeded {
		return s.SyncManifestMetadata(ctx, seed)
	}
	seeds, err := manifestMetadataSeeds(seed)
	if err != nil {
		return err
	}
	tx, err := s.database().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata seed: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.upsertMetadataProjection(ctx, tx, seed, now); err != nil {
		return err
	}
	for _, seed := range seeds {
		if err := s.insertMetadataResource(ctx, tx, seed, now); err != nil {
			return err
		}
	}
	if err := s.syncManifestLocalizedTexts(ctx, tx, seed, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metadata seed: %w", err)
	}
	return s.refreshMetadataCatalogHash(ctx)
}

func (s ApplicationSchemaStore) SyncManifestMetadata(ctx context.Context, seed manifestmodel.ManifestSchema) error {
	if len(seed.Objects) == 0 {
		return fmt.Errorf("manifest metadata sync has no objects")
	}
	ctx = manifestMetadataContext(ctx)
	if err := s.syncMetadataModuleDefinitions(ctx, seed); err != nil {
		return err
	}
	seeds, err := manifestMetadataSeeds(seed)
	if err != nil {
		return err
	}
	tx, err := s.database().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata sync: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.upsertMetadataProjection(ctx, tx, seed, now); err != nil {
		return err
	}
	for _, seed := range seeds {
		if err := s.syncMetadataResource(ctx, tx, seed, now); err != nil {
			return err
		}
	}
	if err := removeDeletedGeneratedAutomationRulesForManifest(ctx, s, tx, seed, now); err != nil {
		return err
	}
	if err := s.syncManifestLocalizedTexts(ctx, tx, seed, now); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit metadata sync: %w", err)
	}
	_ = s.refreshMetadataCatalogHash(ctx)
	return nil
}

func (s ApplicationSchemaStore) upsertMetadataProjection(ctx context.Context, tx *sql.Tx, seed manifestmodel.ManifestSchema, now string) error {
	_, sourceHash, err := metadataPayload(seed)
	if err != nil {
		return fmt.Errorf("hash metadata source: %w", err)
	}
	contractVersion := strings.TrimSpace(seed.SchemaVersion)
	if contractVersion == "" {
		contractVersion = "manifest-v1"
	}
	columns := []string{"id", "contract_version", "source_hash", "schema_hash", "artifact_version", "materializer_version", "status", "template_id", "default_locale", "name", "materialized_at"}
	values := []any{"current", contractVersion, sourceHash, "", strings.TrimSpace(seed.Version), "runtime-materializer-v1", "materialized", strings.TrimSpace(seed.TemplateID), manifestDefaultLocale(seed), strings.TrimSpace(seed.Name), now}
	insert := query.NewInsertBuilder(s.store.SQLRenderer, "_application_schema_projection").Columns(columns...).Values(values...)
	assignments := make([]query.Assignment, 0, len(columns)-1)
	for _, column := range columns[1:] {
		assignments = append(assignments, query.AssignExpression(column, query.InsertedValue(column)))
	}
	insert, err = s.store.Engine.ApplyUpsert(insert, []string{"id"}, assignments...)
	if err != nil {
		return fmt.Errorf("build metadata projection upsert: %w", err)
	}
	queryValue, args, err := insert.Build()
	if err != nil {
		return fmt.Errorf("build metadata projection: %w", err)
	}
	if _, err := tx.ExecContext(ctx, queryValue, args...); err != nil {
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

func (s ApplicationSchemaStore) removeDeletedGeneratedAutomationRules(ctx context.Context, tx *sql.Tx, manifest manifestmodel.ManifestSchema) error {
	activeKeys := make(map[string]bool, len(manifest.AutomationRules))
	for _, rule := range manifest.AutomationRules {
		if key := strings.TrimSpace(rule.Key); key != "" {
			activeKeys[key] = true
		}
	}
	return s.removeDeletedGeneratedDefinitions(ctx, tx, "_application_schema_automation_rule_definitions", manifestGeneratedSourceID(manifest), activeKeys)
}

func manifestGeneratedSourceID(manifest manifestmodel.ManifestSchema) string {
	if sourceID := strings.TrimSpace(manifest.TemplateID); sourceID != "" {
		return sourceID
	}
	return "generated-template"
}

func (s ApplicationSchemaStore) removeDeletedGeneratedDefinitions(ctx context.Context, tx *sql.Tx, table, sourceID string, activeKeys map[string]bool) error {
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, table).Columns("resource_key").Where(query.And(query.Equal("source_kind", "generated"), query.Equal("source_id", sourceID))).Build()
	if buildErr != nil {
		return fmt.Errorf("build generated %s manifest sync list: %w", table, buildErr)
	}
	rows, err := tx.QueryContext(ctx, queryValue, args...)
	if err != nil {
		return fmt.Errorf("list generated %s for manifest sync: %w", table, err)
	}
	removedKeys := []string{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			rows.Close()
			return fmt.Errorf("scan generated %s for manifest sync: %w", table, err)
		}
		if !activeKeys[key] {
			removedKeys = append(removedKeys, key)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read generated %s for manifest sync: %w", table, err)
	}
	if err := closeGeneratedActionRows(rows); err != nil {
		return fmt.Errorf("close generated %s for manifest sync: %w", table, err)
	}
	for _, key := range removedKeys {
		remove, removeArgs, buildErr := query.NewDeleteBuilder(s.store.SQLRenderer, table).Where(query.Equal("resource_key", key)).Build()
		if buildErr != nil {
			return fmt.Errorf("build deleted generated %s entry %s removal: %w", table, key, buildErr)
		}
		if _, err := tx.ExecContext(ctx, remove, removeArgs...); err != nil {
			return fmt.Errorf("remove deleted generated %s entry %s: %w", table, key, err)
		}
	}
	return nil
}
