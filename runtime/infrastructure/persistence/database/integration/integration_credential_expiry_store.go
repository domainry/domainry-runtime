package integration

import (
	"context"
	"database/sql"
	"fmt"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type IntegrationCredentialExpiryStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewIntegrationCredentialExpiryStore(store *database.RuntimeStore) IntegrationCredentialExpiryStore {
	return IntegrationCredentialExpiryStore{store: store, db: store.DB()}
}

func (s IntegrationCredentialExpiryStore) ListIntegrationCredentialExpiryCandidates(ctx context.Context, scope principalmodel.SystemScope, expiresBefore string, limit int) ([]integrationmodel.IntegrationSecret, error) {
	if !scope.Valid() {
		return nil, fmt.Errorf("system scope is required to list integration credential expiry candidates")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	columns := stringsJoinIdentifiers(s.store, "secret_key", "workspace_id", "kind", "status", "description", "value_ref", "fingerprint", "created_by", "created_at", "updated_at", "disabled_at", "expires_at", "rotated_at", "revoked_at", "last_tested_at", "last_test_status", "last_test_error")
	query := "SELECT " + columns + " FROM " + s.store.TableIdentifier("integration_secrets") +
		" WHERE " + s.store.Identifier("expires_at") + " <> '' AND " + s.store.Identifier("expires_at") + " <= " + s.store.Placeholder(1) +
		" AND " + s.store.Identifier("status") + " IN ('active','expired') ORDER BY " + s.store.Identifier("expires_at") + " ASC LIMIT " + fmt.Sprint(limit)
	rows, err := s.db.QueryContext(ctx, query, expiresBefore)
	if err != nil {
		return nil, fmt.Errorf("list integration credential expiry candidates: %w", err)
	}
	defer rows.Close()
	values := []integrationmodel.IntegrationSecret{}
	for rows.Next() {
		value, scanErr := scanIntegrationSecret(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration credential expiry candidates: %w", err)
	}
	return values, nil
}
