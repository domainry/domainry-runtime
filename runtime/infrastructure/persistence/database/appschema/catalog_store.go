package appschema

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

type metadataSQLDialect interface {
	Identifier(string) string
	TableIdentifier(string) string
	Placeholder(int) string
}

func (s ApplicationSchemaStore) manifestMetadataSeeded(ctx context.Context) (bool, error) {
	query, args, err := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "metadata_catalog").
		Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.Equal("key", "template_id")).Build()
	if err != nil {
		return false, fmt.Errorf("build metadata catalog seed query: %w", err)
	}
	var count int
	if err := s.database().QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return false, fmt.Errorf("read metadata catalog: %w", err)
	}
	return count > 0, nil
}

func (s ApplicationSchemaStore) ManifestIdentitySeedSyncedVersion(ctx context.Context) (string, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "metadata_catalog").
		Columns("value").Where(ormbuilder.Equal("key", "identity_seed_synced_version")).Build()
	if buildErr != nil {
		return "", fmt.Errorf("build identity seed version query: %w", buildErr)
	}
	var value string
	if err := s.database().QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("read identity seed synced version: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func (s ApplicationSchemaStore) SetManifestIdentitySeedSyncedVersion(ctx context.Context, version string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	query, args, err := buildMetadataCatalogUpsert(s, "identity_seed_synced_version", strings.TrimSpace(version), now)
	if err != nil {
		return fmt.Errorf("build identity seed version upsert: %w", err)
	}
	if _, err := s.database().ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("set identity seed synced version: %w", err)
	}
	return nil
}

func (s ApplicationSchemaStore) ManifestOrganizationScopeSeedState(ctx context.Context) (string, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "metadata_catalog").
		Columns("value").Where(ormbuilder.Equal("key", "organization_scope_seed_state")).Build()
	if buildErr != nil {
		return "", fmt.Errorf("build organization scope seed query: %w", buildErr)
	}
	var value string
	if err := s.database().QueryRowContext(ctx, query, args...).Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("read organization scope seed state: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func (s ApplicationSchemaStore) SetManifestOrganizationScopeSeedState(ctx context.Context, state string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	query, args, err := buildMetadataCatalogUpsert(s, "organization_scope_seed_state", strings.TrimSpace(state), now)
	if err != nil {
		return fmt.Errorf("build organization scope seed upsert: %w", err)
	}
	if _, err := s.database().ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("set organization scope seed state: %w", err)
	}
	return nil
}

func buildMetadataCatalogUpsert(store ApplicationSchemaStore, key, value, now string) (string, []any, error) {
	insert := ormbuilder.NewInsertBuilder(store.store.SQLRenderer, "metadata_catalog").
		Columns("key", "value", "updated_at").Values(key, value, now)
	insert, err := store.store.Engine.ApplyUpsert(insert, []string{"key"},
		ormbuilder.AssignExpression("value", ormbuilder.InsertedValue("value")),
		ormbuilder.AssignExpression("updated_at", ormbuilder.InsertedValue("updated_at")),
	)
	if err != nil {
		return "", nil, err
	}
	return insert.Build()
}

func (s ApplicationSchemaStore) insertMetadataCatalog(ctx context.Context, tx *sql.Tx, key string, value string, now string) error {
	query, args, buildErr := ormbuilder.NewInsertBuilder(s.store.SQLRenderer, "metadata_catalog").Columns("key", "value", "updated_at").Values(strings.TrimSpace(key), strings.TrimSpace(value), now).Build()
	if buildErr != nil {
		return fmt.Errorf("build metadata catalog insert: %w", buildErr)
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("insert metadata catalog %s: %w", key, err)
	}
	return nil
}

func (s ApplicationSchemaStore) upsertMetadataCatalog(ctx context.Context, tx *sql.Tx, key string, value string, now string) error {
	updateQuery, args, buildErr := ormbuilder.NewUpdateBuilder(s.store.SQLRenderer, "metadata_catalog").Set("value", strings.TrimSpace(value)).Set("updated_at", now).Where(ormbuilder.Equal("key", strings.TrimSpace(key))).Build()
	if buildErr != nil {
		return fmt.Errorf("build metadata catalog update: %w", buildErr)
	}
	result, err := tx.ExecContext(ctx, updateQuery, args...)
	if err != nil {
		return fmt.Errorf("update metadata catalog %s: %w", key, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read updated metadata catalog rows %s: %w", key, err)
	}
	if rows > 0 {
		return nil
	}
	return s.insertMetadataCatalog(ctx, tx, key, value, now)
}

// refreshMetadataCatalogHash uses the same content-addressed catalog algorithm
// as transactional definition publication. Timestamps and publication order
// are deliberately excluded so package replays converge on the same hash.
func (s ApplicationSchemaStore) refreshMetadataCatalogHash(ctx context.Context) error {
	return s.refreshCatalogHash(ctx)
}
