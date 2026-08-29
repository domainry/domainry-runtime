package metadata

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

func (s MetadataStore) manifestMetadataSeeded(ctx context.Context) (bool, error) {
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

func (s MetadataStore) ManifestIdentitySeedSyncedVersion(ctx context.Context) (string, error) {
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

func (s MetadataStore) SetManifestIdentitySeedSyncedVersion(ctx context.Context, version string) error {
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

func (s MetadataStore) ManifestOrganizationScopeSeedState(ctx context.Context) (string, error) {
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

func (s MetadataStore) SetManifestOrganizationScopeSeedState(ctx context.Context, state string) error {
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

func buildMetadataCatalogUpsert(store MetadataStore, key, value, now string) (string, []any, error) {
	insert := ormbuilder.NewInsertBuilder(store.store.SQLRenderer, "metadata_catalog").
		Columns("key", "value", "updated_at").Values(key, value, now)
	return store.store.Engine.ApplyUpsert(insert, []string{"key"}, "value", "updated_at").Build()
}

func (s MetadataStore) insertMetadataCatalog(ctx context.Context, tx *sql.Tx, key string, value string, now string) error {
	columns := []string{"key", "value", "updated_at"}
	query := "INSERT INTO " + s.store.TableIdentifier("metadata_catalog") + " (" + strings.Join(quotedColumns(s.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s.store, len(columns)), ", ") + ")"
	if _, err := tx.ExecContext(ctx, query, strings.TrimSpace(key), strings.TrimSpace(value), now); err != nil {
		return fmt.Errorf("insert metadata catalog %s: %w", key, err)
	}
	return nil
}

func (s MetadataStore) upsertMetadataCatalog(ctx context.Context, tx *sql.Tx, key string, value string, now string) error {
	updateQuery := "UPDATE " + s.store.TableIdentifier("metadata_catalog") +
		" SET " + s.store.Identifier("value") + " = " + s.store.Placeholder(1) +
		", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(2) +
		" WHERE " + s.store.Identifier("key") + " = " + s.store.Placeholder(3)
	result, err := tx.ExecContext(ctx, updateQuery, strings.TrimSpace(value), now, strings.TrimSpace(key))
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
func (s MetadataStore) refreshMetadataCatalogHash(ctx context.Context) error {
	return s.refreshCatalogHash(ctx)
}
