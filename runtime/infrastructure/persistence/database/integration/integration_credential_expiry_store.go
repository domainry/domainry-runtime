package integration

import (
	"context"
	"database/sql"
	"fmt"

	ormbuilder "github.com/domainry/domainry-orm/builder"
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
	columns := []string{"secret_key", "workspace_id", "kind", "status", "description", "value_ref", "fingerprint", "created_by", "created_at", "updated_at", "disabled_at", "expires_at", "rotated_at", "revoked_at", "last_tested_at", "last_test_status", "last_test_error"}
	query, args, err := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "integration_secrets").Columns(columns...).Where(ormbuilder.And(ormbuilder.NotEqual("expires_at", ""), ormbuilder.LessThanOrEqual("expires_at", expiresBefore), ormbuilder.In("status", "active", "expired"))).OrderBy(ormbuilder.Ascending("expires_at"), ormbuilder.Ascending("workspace_id"), ormbuilder.Ascending("secret_key")).Limit(limit).Build()
	if err != nil {
		return nil, fmt.Errorf("build integration credential expiry candidates: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
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
