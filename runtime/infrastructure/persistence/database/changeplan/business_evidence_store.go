// Business evidence persistence.
package changeplan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	businessseedmodel "github.com/domainry/domainry-runtime/runtime/domain/businessseed/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type BusinessEvidenceStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewBusinessEvidenceStore(store *database.RuntimeStore) BusinessEvidenceStore {
	return BusinessEvidenceStore{store: store, db: store.DB()}
}
func (r BusinessEvidenceStore) UpsertSeedProvenance(ctx context.Context, value businessseedmodel.BusinessSeedProvenance) error {
	columns := []string{"seed_key", "object_key", "record_id", "source_kind", "source_id", "template_id", "template_version", "content_hash", "materialized_at"}
	values := []any{value.SeedKey, value.ObjectKey, value.RecordID, value.SourceKind, value.SourceID, value.TemplateID, value.TemplateVersion, value.ContentHash, value.MaterializedAt}
	tx, err := r.db.BeginTx(ctx, transactionOptions())
	if err != nil {
		return fmt.Errorf("begin seed provenance upsert: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+r.store.TableIdentifier("_business_seed_provenance")+" WHERE "+r.store.Identifier("seed_key")+" = "+r.store.Placeholder(1), value.SeedKey); err != nil {
		return fmt.Errorf("replace seed provenance %s: %w", value.SeedKey, err)
	}
	query := "INSERT INTO " + r.store.TableIdentifier("_business_seed_provenance") + " (" + joinIdentifiers(r.store, columns...) + ") VALUES (" + joinPlaceholders(r.store, len(columns)) + ")"
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert seed provenance %s: %w", value.SeedKey, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit seed provenance %s: %w", value.SeedKey, err)
	}
	return nil
}

func (r BusinessEvidenceStore) GetSeedProvenance(ctx context.Context, seedKey string) (businessseedmodel.BusinessSeedProvenance, bool, error) {
	columns := []string{"seed_key", "object_key", "record_id", "source_kind", "source_id", "template_id", "template_version", "content_hash", "materialized_at"}
	row := r.db.QueryRowContext(ctx, "SELECT "+joinIdentifiers(r.store, columns...)+" FROM "+r.store.TableIdentifier("_business_seed_provenance")+" WHERE "+r.store.Identifier("seed_key")+" = "+r.store.Placeholder(1), strings.TrimSpace(seedKey))
	var value businessseedmodel.BusinessSeedProvenance
	if err := row.Scan(&value.SeedKey, &value.ObjectKey, &value.RecordID, &value.SourceKind, &value.SourceID, &value.TemplateID, &value.TemplateVersion, &value.ContentHash, &value.MaterializedAt); errors.Is(err, sql.ErrNoRows) {
		return businessseedmodel.BusinessSeedProvenance{}, false, nil
	} else if err != nil {
		return businessseedmodel.BusinessSeedProvenance{}, false, fmt.Errorf("get domain seed provenance %s: %w", seedKey, err)
	}
	return value, true, nil
}
func (r BusinessEvidenceStore) ListSeedProvenance(ctx context.Context) ([]businessseedmodel.BusinessSeedProvenance, error) {
	columns := []string{"seed_key", "object_key", "record_id", "source_kind", "source_id", "template_id", "template_version", "content_hash", "materialized_at"}
	rows, err := r.db.QueryContext(ctx, "SELECT "+joinIdentifiers(r.store, columns...)+" FROM "+r.store.TableIdentifier("_business_seed_provenance")+" ORDER BY "+r.store.Identifier("object_key")+", "+r.store.Identifier("seed_key"))
	if err != nil {
		return nil, fmt.Errorf("list domain seed provenance: %w", err)
	}
	defer rows.Close()
	out := []businessseedmodel.BusinessSeedProvenance{}
	for rows.Next() {
		var value businessseedmodel.BusinessSeedProvenance
		if err := rows.Scan(&value.SeedKey, &value.ObjectKey, &value.RecordID, &value.SourceKind, &value.SourceID, &value.TemplateID, &value.TemplateVersion, &value.ContentHash, &value.MaterializedAt); err != nil {
			return nil, fmt.Errorf("scan domain seed provenance: %w", err)
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
