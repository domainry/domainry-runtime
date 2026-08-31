package integration

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/domainry/domainry-orm/query"
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
	queryValue, args, err := query.NewSelectBuilder(s.store.SQLRenderer, "_integration_secrets").Columns(columns...).Where(query.And(query.NotEqual("expires_at", ""), query.LessThanOrEqual("expires_at", expiresBefore), query.In("status", "active", "expired"))).OrderBy(query.Ascending("expires_at"), query.Ascending("workspace_id"), query.Ascending("secret_key")).Limit(limit).Build()
	if err != nil {
		return nil, fmt.Errorf("build integration credential expiry candidates: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, queryValue, args...)
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
