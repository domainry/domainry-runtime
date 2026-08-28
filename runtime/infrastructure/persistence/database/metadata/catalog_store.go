package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type metadataSQLDialect interface {
	Identifier(string) string
	TableIdentifier(string) string
	Placeholder(int) string
}

func (s MetadataStore) manifestMetadataSeeded(ctx context.Context) (bool, error) {
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("metadata_catalog") + " WHERE " + s.store.Identifier("key") + " = " + s.store.Placeholder(1)
	var count int
	if err := s.database().QueryRowContext(ctx, query, "template_id").Scan(&count); err != nil {
		return false, fmt.Errorf("read metadata catalog: %w", err)
	}
	return count > 0, nil
}

func (s MetadataStore) ManifestIdentitySeedSyncedVersion(ctx context.Context) (string, error) {
	query := "SELECT " + s.store.Identifier("value") + " FROM " + s.store.TableIdentifier("metadata_catalog") + " WHERE " + s.store.Identifier("key") + " = " + s.store.Placeholder(1)
	var value string
	if err := s.database().QueryRowContext(ctx, query, "identity_seed_synced_version").Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("read identity seed synced version: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func (s MetadataStore) SetManifestIdentitySeedSyncedVersion(ctx context.Context, version string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	query := metadataCatalogReplaceSQL(s.store, s.store.Driver())
	if _, err := s.database().ExecContext(ctx, query, "identity_seed_synced_version", strings.TrimSpace(version), now); err != nil {
		return fmt.Errorf("set identity seed synced version: %w", err)
	}
	return nil
}

func (s MetadataStore) ManifestOrganizationScopeSeedState(ctx context.Context) (string, error) {
	query := "SELECT " + s.store.Identifier("value") + " FROM " + s.store.TableIdentifier("metadata_catalog") + " WHERE " + s.store.Identifier("key") + " = " + s.store.Placeholder(1)
	var value string
	if err := s.database().QueryRowContext(ctx, query, "organization_scope_seed_state").Scan(&value); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("read organization scope seed state: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func (s MetadataStore) SetManifestOrganizationScopeSeedState(ctx context.Context, state string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	query := metadataCatalogReplaceSQL(s.store, s.store.Driver())
	if _, err := s.database().ExecContext(ctx, query, "organization_scope_seed_state", strings.TrimSpace(state), now); err != nil {
		return fmt.Errorf("set organization scope seed state: %w", err)
	}
	return nil
}

func metadataCatalogReplaceSQL(store metadataSQLDialect, driver string) string {
	base := "INSERT INTO " + store.TableIdentifier("metadata_catalog") +
		" (" + store.Identifier("key") + ", " + store.Identifier("value") + ", " + store.Identifier("updated_at") + ")" +
		" VALUES (" + store.Placeholder(1) + ", " + store.Placeholder(2) + ", " + store.Placeholder(3) + ")"
	switch driver {
	case "mysql":
		return base + " ON DUPLICATE KEY UPDATE " + store.Identifier("value") + " = VALUES(" + store.Identifier("value") + ")" +
			", " + store.Identifier("updated_at") + " = VALUES(" + store.Identifier("updated_at") + ")"
	case "postgres":
		return base + " ON CONFLICT (" + store.Identifier("key") + ") DO UPDATE SET " +
			store.Identifier("value") + " = EXCLUDED." + store.Identifier("value") +
			", " + store.Identifier("updated_at") + " = EXCLUDED." + store.Identifier("updated_at")
	default:
		return strings.Replace(base, "INSERT INTO", "INSERT OR REPLACE INTO", 1)
	}
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
