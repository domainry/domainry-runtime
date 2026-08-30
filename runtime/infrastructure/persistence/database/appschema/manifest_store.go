package appschema

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var disableRemovedGeneratedActionsForManifest = func(ctx context.Context, store ApplicationSchemaStore, tx *sql.Tx, manifest manifestmodel.ManifestSchema, now string) error {
	return store.disableRemovedGeneratedActions(ctx, tx, manifest, now)
}

var disableRemovedGeneratedAutomationRulesForManifest = func(ctx context.Context, store ApplicationSchemaStore, tx *sql.Tx, manifest manifestmodel.ManifestSchema, now string) error {
	return store.disableRemovedGeneratedAutomationRules(ctx, tx, manifest, now)
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
	for key, value := range map[string]string{
		"template_id":      seed.TemplateID,
		"template_version": seed.Version,
		"default_locale":   manifestDefaultLocale(seed),
		"name":             seed.Name,
		"schema_version":   seed.Version,
	} {
		if err := s.insertMetadataCatalog(ctx, tx, key, value, now); err != nil {
			return err
		}
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
	return nil
}

func (s ApplicationSchemaStore) SyncManifestMetadata(ctx context.Context, seed manifestmodel.ManifestSchema) error {
	if len(seed.Objects) == 0 {
		return fmt.Errorf("manifest metadata sync has no objects")
	}
	ctx = manifestMetadataContext(ctx)
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
	for key, value := range map[string]string{
		"template_id":      seed.TemplateID,
		"template_version": seed.Version,
		"default_locale":   manifestDefaultLocale(seed),
		"name":             seed.Name,
		"schema_version":   seed.Version,
	} {
		if err := s.upsertMetadataCatalog(ctx, tx, key, value, now); err != nil {
			return err
		}
	}
	for _, seed := range seeds {
		if err := s.syncMetadataResource(ctx, tx, seed, now); err != nil {
			return err
		}
	}
	if err := disableRemovedGeneratedActionsForManifest(ctx, s, tx, seed, now); err != nil {
		return err
	}
	if err := disableRemovedGeneratedAutomationRulesForManifest(ctx, s, tx, seed, now); err != nil {
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

// manifestMetadataContext binds installation-owned manifest metadata to the
// compatibility workspace when startup does not run inside an HTTP request.
// An explicit caller workspace is preserved so request-scoped sync cannot be
// silently redirected to another tenant.
func manifestMetadataContext(ctx context.Context) context.Context {
	if requestcontext.WorkspaceID(ctx) != "" {
		return ctx
	}
	return requestcontext.WithWorkspaceID(ctx, principalmodel.InstallationWorkspaceID)
}

func (s ApplicationSchemaStore) disableRemovedGeneratedActions(ctx context.Context, tx *sql.Tx, manifest manifestmodel.ManifestSchema, now string) error {
	activeKeys := make(map[string]bool, len(manifest.Actions))
	for _, action := range manifest.Actions {
		if key := strings.TrimSpace(action.Key); key != "" {
			activeKeys[key] = true
		}
	}
	return s.disableRemovedGeneratedDefinitions(ctx, tx, "action_definitions", manifestGeneratedSourceID(manifest), activeKeys, now)
}

// disableRemovedGeneratedAutomationRules deactivates automation rule
// definitions that were removed from the model: without it, rules deleted
// before an evolution apply + repackage kept executing on an existing Runtime
// database until the cohort was rebuilt from scratch.
func (s ApplicationSchemaStore) disableRemovedGeneratedAutomationRules(ctx context.Context, tx *sql.Tx, manifest manifestmodel.ManifestSchema, now string) error {
	activeKeys := make(map[string]bool, len(manifest.AutomationRules))
	for _, rule := range manifest.AutomationRules {
		if key := strings.TrimSpace(rule.Key); key != "" {
			activeKeys[key] = true
		}
	}
	return s.disableRemovedGeneratedDefinitions(ctx, tx, "automation_rule_definitions", manifestGeneratedSourceID(manifest), activeKeys, now)
}

func manifestGeneratedSourceID(manifest manifestmodel.ManifestSchema) string {
	if sourceID := strings.TrimSpace(manifest.TemplateID); sourceID != "" {
		return sourceID
	}
	return "generated-template"
}

func (s ApplicationSchemaStore) disableRemovedGeneratedDefinitions(ctx context.Context, tx *sql.Tx, table, sourceID string, activeKeys map[string]bool, now string) error {
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, table).Columns("resource_key").Where(ormbuilder.And(ormbuilder.Equal("source_kind", "generated"), ormbuilder.Equal("source_id", sourceID), ormbuilder.IsNull("disabled_at"))).Build()
	if buildErr != nil {
		return fmt.Errorf("build generated %s manifest sync list: %w", table, buildErr)
	}
	rows, err := tx.QueryContext(ctx, query, args...)
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
		update, updateArgs, buildErr := ormbuilder.NewUpdateBuilder(s.store.SQLRenderer, table).Set("disabled_at", now).Set("updated_at", now).Where(ormbuilder.Equal("resource_key", key)).Build()
		if buildErr != nil {
			return fmt.Errorf("build removed generated %s entry %s disable: %w", table, key, buildErr)
		}
		if _, err := tx.ExecContext(ctx, update, updateArgs...); err != nil {
			return fmt.Errorf("disable removed generated %s entry %s: %w", table, key, err)
		}
	}
	return nil
}
