// Integration configuration persistence.
package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type IntegrationConfigStore struct {
	store       *database.RuntimeStore
	db          *sql.DB
	driver      string
	createIndex func(context.Context, string, string, bool, ...string) error
}

func NewIntegrationConfigStore(store *database.RuntimeStore) IntegrationConfigStore {
	return IntegrationConfigStore{store: store, db: store.DB(), driver: store.Driver(), createIndex: store.CreateIndexIfMissing}
}

func (r IntegrationConfigStore) ListSecrets(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationSecret, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	executor := database.ActionExecutionExecutor(r.db)
	if actionExecutor := database.ActionExecutionTransaction(ctx); actionExecutor != nil {
		executor = actionExecutor
	}
	columns := []string{"secret_key", "workspace_id", "kind", "status", "description", "value_ref", "fingerprint", "created_by", "created_at", "updated_at", "disabled_at", "expires_at", "rotated_at", "revoked_at", "last_tested_at", "last_test_status", "last_test_error"}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_secrets", workspaceID).Columns(columns...).OrderBy(ormbuilder.Ascending("secret_key")).Build()
	if err != nil {
		return nil, fmt.Errorf("build integration secret list: %w", err)
	}
	rows, err := executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list integration secrets: %w", err)
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationSecret{}
	for rows.Next() {
		value, err := scanIntegrationSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration secrets: %w", err)
	}
	return out, nil
}

func (r IntegrationConfigStore) UpsertSecret(ctx context.Context, workspaceID string, value integrationmodel.IntegrationSecret) (integrationmodel.IntegrationSecret, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	value.WorkspaceID = workspaceID
	createdAt, createdBy, err := r.createdMetadata(ctx, "integration_secrets", "secret_key", value.WorkspaceID, value.Key, value.CreatedBy, now)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, fmt.Errorf("read integration secret: %w", err)
	}
	columns := []string{"id", "secret_key", "workspace_id", "kind", "status", "description", "value_ref", "fingerprint", "created_by", "created_at", "updated_at", "disabled_at", "expires_at", "rotated_at", "revoked_at", "last_tested_at", "last_test_status", "last_test_error"}
	args := []any{"integration_secret:" + value.WorkspaceID + ":" + value.Key, value.Key, value.WorkspaceID, value.Kind, value.Status, value.Description, value.ValueRef, value.Fingerprint, createdBy, createdAt, now, value.DisabledAt, value.ExpiresAt, value.RotatedAt, value.RevokedAt, value.LastTestedAt, value.LastTestStatus, value.LastTestError}
	if err := r.replaceRow(ctx, "integration_secrets", "secret_key", value.WorkspaceID, value.Key, columns, args, "integration secret"); err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	value.CreatedBy, value.CreatedAt, value.UpdatedAt = createdBy, createdAt, now
	return value, nil
}

func (r IntegrationConfigStore) ListConnections(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationConnection, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	executor := database.ActionExecutionExecutor(r.db)
	if actionExecutor := database.ActionExecutionTransaction(ctx); actionExecutor != nil {
		executor = actionExecutor
	}
	columns := []string{"connection_key", "workspace_id", "connector_key", "provider_key", "name", "status", "config_json", "secret_refs_json", "created_by", "created_at", "updated_at"}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_connections", workspaceID).Columns(columns...).OrderBy(ormbuilder.Ascending("connection_key")).Build()
	if err != nil {
		return nil, fmt.Errorf("build integration connection list: %w", err)
	}
	rows, err := executor.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list integration connections: %w", err)
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationConnection{}
	for rows.Next() {
		value, err := scanIntegrationConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration connections: %w", err)
	}
	return out, nil
}

func (r IntegrationConfigStore) UpsertConnection(ctx context.Context, workspaceID string, value integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	value.WorkspaceID = workspaceID
	createdAt, createdBy, err := r.createdMetadata(ctx, "integration_connections", "connection_key", value.WorkspaceID, value.Key, value.CreatedBy, now)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, fmt.Errorf("read integration connection: %w", err)
	}
	configJSON, err := json.Marshal(nonNilMap(value.Config))
	if err != nil {
		return integrationmodel.IntegrationConnection{}, fmt.Errorf("encode integration connection config: %w", err)
	}
	secretRefsJSON, _ := json.Marshal(nonNilStringMap(value.SecretRefs))
	columns := []string{"id", "connection_key", "workspace_id", "connector_key", "provider_key", "name", "status", "config_json", "secret_refs_json", "created_by", "created_at", "updated_at"}
	args := []any{"integration_connection:" + value.WorkspaceID + ":" + value.Key, value.Key, value.WorkspaceID, value.ConnectorKey, value.ProviderKey, value.Name, value.Status, string(configJSON), string(secretRefsJSON), createdBy, createdAt, now}
	if err := r.replaceRow(ctx, "integration_connections", "connection_key", value.WorkspaceID, value.Key, columns, args, "integration connection"); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	value.CreatedBy, value.CreatedAt, value.UpdatedAt = createdBy, createdAt, now
	return value, nil
}

func (r IntegrationConfigStore) ListExternalIdentities(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationExternalIdentity, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	columns := []string{"identity_key", "workspace_id", "provider", "external_subject", "external_subject_type", "external_name", "external_organization", "external_department", "external_group", "external_bot_id", "actor_id", "role_key", "status", "last_resolved_at", "created_by", "created_at", "updated_at", "disabled_at"}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_external_identities", workspaceID).Columns(columns...).OrderBy(ormbuilder.Ascending("provider"), ormbuilder.Ascending("external_subject")).Build()
	if err != nil {
		return nil, fmt.Errorf("build integration external identity list: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list integration external identities: %w", err)
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationExternalIdentity{}
	for rows.Next() {
		value, err := scanIntegrationExternalIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration external identities: %w", err)
	}
	return out, nil
}

func (r IntegrationConfigStore) UpsertExternalIdentity(ctx context.Context, workspaceID string, value integrationmodel.IntegrationExternalIdentity) (integrationmodel.IntegrationExternalIdentity, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	value.WorkspaceID = workspaceID
	createdAt, createdBy, err := r.createdMetadata(ctx, "integration_external_identities", "identity_key", value.WorkspaceID, value.Key, value.CreatedBy, now)
	if err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, fmt.Errorf("read integration external identity: %w", err)
	}
	columns := []string{"id", "identity_key", "workspace_id", "provider", "external_subject", "external_subject_type", "external_name", "external_organization", "external_department", "external_group", "external_bot_id", "actor_id", "role_key", "status", "last_resolved_at", "created_by", "created_at", "updated_at", "disabled_at"}
	args := []any{"integration_external_identity:" + value.WorkspaceID + ":" + value.Key, value.Key, value.WorkspaceID, value.Provider, value.ExternalSubject, value.ExternalSubjectType, value.ExternalName, value.ExternalOrganization, value.ExternalDepartment, value.ExternalGroup, value.ExternalBotID, value.ActorID, value.RoleKey, value.Status, value.LastResolvedAt, createdBy, createdAt, now, value.DisabledAt}
	if err := r.replaceRow(ctx, "integration_external_identities", "identity_key", value.WorkspaceID, value.Key, columns, args, "integration external identity"); err != nil {
		return integrationmodel.IntegrationExternalIdentity{}, err
	}
	value.CreatedBy, value.CreatedAt, value.UpdatedAt = createdBy, createdAt, now
	return value, nil
}

func (r IntegrationConfigStore) createdMetadata(ctx context.Context, table, keyColumn, workspaceID, key, createdBy, now string) (string, string, error) {
	var existingAt, existingBy string
	executor := database.ActionExecutionExecutor(r.db)
	if actionExecutor := database.ActionExecutionTransaction(ctx); actionExecutor != nil {
		executor = actionExecutor
	}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, table, workspaceID).Columns("created_at", "created_by").Where(ormbuilder.Equal(keyColumn, key)).Build()
	if buildErr != nil {
		return "", "", fmt.Errorf("build integration created metadata lookup: %w", buildErr)
	}
	err := executor.QueryRowContext(ctx, query, args...).Scan(&existingAt, &existingBy)
	if errors.Is(err, sql.ErrNoRows) {
		return now, createdBy, nil
	}
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(createdBy) == "" {
		createdBy = existingBy
	}
	return existingAt, createdBy, nil
}

func (r IntegrationConfigStore) replaceRow(ctx context.Context, table, keyColumn, workspaceID, key string, columns []string, args []any, label string) error {
	insertColumns, insertValues, err := workspaceInsertValues(workspaceID, columns, args)
	if err != nil {
		return fmt.Errorf("build %s replacement: %w", label, err)
	}
	deleteStatement, deleteArgs, err := ormbuilder.NewWorkspaceDeleteBuilder(r.store.SQLRenderer, table, workspaceID).
		Where(ormbuilder.Equal(keyColumn, key)).Build()
	if err != nil {
		return fmt.Errorf("build %s replacement delete: %w", label, err)
	}
	insertStatement, insertArgs, err := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, table, workspaceID).
		Columns(insertColumns...).Values(insertValues...).Build()
	if err != nil {
		return fmt.Errorf("build %s replacement insert: %w", label, err)
	}
	if executor := database.ActionExecutionTransaction(ctx); executor != nil {
		if _, err := executor.ExecContext(ctx, deleteStatement, deleteArgs...); err != nil {
			return fmt.Errorf("replace %s: %w", label, err)
		}
		if _, err := executor.ExecContext(ctx, insertStatement, insertArgs...); err != nil {
			return fmt.Errorf("insert %s: %w", label, err)
		}
		return nil
	}
	tx, err := r.db.BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return fmt.Errorf("begin %s upsert: %w", label, err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, deleteStatement, deleteArgs...); err != nil {
		return fmt.Errorf("replace %s: %w", label, err)
	}
	if _, err := tx.ExecContext(ctx, insertStatement, insertArgs...); err != nil {
		return fmt.Errorf("insert %s: %w", label, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s upsert: %w", label, err)
	}
	return nil
}

func workspaceInsertValues(workspaceID string, columns []string, values []any) ([]string, []any, error) {
	if len(columns) != len(values) {
		return nil, nil, fmt.Errorf("SQL replacement columns and values differ")
	}
	insertColumns := make([]string, 0, len(columns))
	insertValues := make([]any, 0, len(values))
	for index, column := range columns {
		if strings.TrimSpace(column) == "workspace_id" {
			if fmt.Sprint(values[index]) != workspaceID {
				return nil, nil, fmt.Errorf("SQL replacement workspace does not match repository workspace")
			}
			continue
		}
		insertColumns = append(insertColumns, column)
		insertValues = append(insertValues, values[index])
	}
	return insertColumns, insertValues, nil
}

func (r IntegrationConfigStore) ListWebhookSubscriptions(ctx context.Context, workspaceID, connectorKey, eventType, status string, limit int) ([]integrationmodel.IntegrationWebhookSubscription, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	predicates := []ormbuilder.Predicate{}
	if connectorKey = strings.TrimSpace(connectorKey); connectorKey != "" {
		predicates = append(predicates, ormbuilder.Equal("connector_key", connectorKey))
	}
	if status = strings.TrimSpace(status); status != "" {
		predicates = append(predicates, ormbuilder.Equal("status", status))
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_webhook_subscriptions", workspaceID).
		Columns(integrationWebhookSubscriptionColumns...).
		OrderBy(ormbuilder.Ascending("subscription_key")).
		Limit(limit)
	if len(predicates) != 0 {
		builder.Where(ormbuilder.And(predicates...))
	}
	query, args, buildErr := builder.Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build integration webhook subscription query: %w", buildErr)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list integration webhook subscriptions: %w", err)
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationWebhookSubscription{}
	for rows.Next() {
		value, err := scanIntegrationWebhookSubscription(rows)
		if err != nil {
			return nil, err
		}
		if integrationWebhookSubscriptionMatchesEvent(value, eventType) {
			out = append(out, value)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration webhook subscriptions: %w", err)
	}
	return out, nil
}

func (r IntegrationConfigStore) UpsertWebhookSubscription(ctx context.Context, workspaceID string, value integrationmodel.IntegrationWebhookSubscription) (integrationmodel.IntegrationWebhookSubscription, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	value.WorkspaceID = workspaceID
	createdAt, createdBy, err := r.createdMetadata(ctx, "integration_webhook_subscriptions", "subscription_key", value.WorkspaceID, value.Key, value.CreatedBy, now)
	if err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, fmt.Errorf("read integration webhook subscription: %w", err)
	}
	eventTypesJSON, _ := json.Marshal(nonNilStringSlice(value.EventTypes))
	columns := []string{"id", "subscription_key", "workspace_id", "name", "connector_key", "connection_key", "event_types_json", "status", "description", "created_by", "created_at", "updated_at", "disabled_at"}
	args := []any{"integration_webhook_subscription:" + value.WorkspaceID + ":" + value.Key, value.Key, value.WorkspaceID, value.Name, value.ConnectorKey, value.ConnectionKey, string(eventTypesJSON), value.Status, value.Description, createdBy, createdAt, now, value.DisabledAt}
	if err := r.replaceRow(ctx, "integration_webhook_subscriptions", "subscription_key", value.WorkspaceID, value.Key, columns, args, "integration webhook subscription"); err != nil {
		return integrationmodel.IntegrationWebhookSubscription{}, err
	}
	value.CreatedBy, value.CreatedAt, value.UpdatedAt = createdBy, createdAt, now
	return value, nil
}

func (r IntegrationConfigStore) ListAPIKeys(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationAPIKey, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_api_keys", workspaceID).Columns(integrationAPIKeyColumns...).OrderBy(ormbuilder.Descending("created_at")).Build()
	if err != nil {
		return nil, fmt.Errorf("build integration api key list: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list integration api keys: %w", err)
	}
	defer rows.Close()
	out := []integrationmodel.IntegrationAPIKey{}
	for rows.Next() {
		value, err := scanIntegrationAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list integration api keys: %w", err)
	}
	return out, nil
}

func (r IntegrationConfigStore) UpsertAPIKey(ctx context.Context, workspaceID string, value integrationmodel.IntegrationAPIKey) (integrationmodel.IntegrationAPIKey, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspaceID, err := requireIntegrationMutationWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, err
	}
	value.WorkspaceID = workspaceID
	createdAt, createdBy, err := r.createdMetadata(ctx, "integration_api_keys", "api_key", value.WorkspaceID, value.Key, value.CreatedBy, now)
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, fmt.Errorf("read integration api key: %w", err)
	}
	scopesJSON, _ := json.Marshal(nonNilStringSlice(value.Scopes))
	columns := []string{"id", "api_key", "workspace_id", "name", "token_prefix", "token_hash", "actor_id", "role_key", "scopes_json", "status", "expires_at", "last_used_at", "created_by", "created_at", "updated_at", "disabled_at"}
	args := []any{"integration_api_key:" + value.WorkspaceID + ":" + value.Key, value.Key, value.WorkspaceID, value.Name, value.TokenPrefix, value.TokenHash, value.ActorID, value.RoleKey, string(scopesJSON), value.Status, value.ExpiresAt, value.LastUsedAt, createdBy, createdAt, now, value.DisabledAt}
	if err := r.replaceRow(ctx, "integration_api_keys", "api_key", value.WorkspaceID, value.Key, columns, args, "integration api key"); err != nil {
		return integrationmodel.IntegrationAPIKey{}, err
	}
	value.CreatedBy, value.CreatedAt, value.UpdatedAt = createdBy, createdAt, now
	return value, nil
}

func (r IntegrationConfigStore) FindAPIKeyByTokenHash(ctx context.Context, workspaceID, tokenHash string) (integrationmodel.IntegrationAPIKey, bool, error) {
	workspaceID, err := requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, false, err
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_api_keys", workspaceID).Columns(integrationAPIKeyColumns...).Where(ormbuilder.Equal("token_hash", strings.TrimSpace(tokenHash))).Build()
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, false, fmt.Errorf("build integration api key token lookup: %w", err)
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	value, err := scanIntegrationAPIKey(row)
	if errors.Is(err, sql.ErrNoRows) {
		return integrationmodel.IntegrationAPIKey{}, false, nil
	}
	return value, err == nil, err
}

func (r IntegrationConfigStore) UpdateAPIKeyLastUsed(ctx context.Context, workspaceID, key, lastUsedAt string) (integrationmodel.IntegrationAPIKey, error) {
	var err error
	workspaceID, err = requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return integrationmodel.IntegrationAPIKey{}, fmt.Errorf("integration api key is required")
	}
	if strings.TrimSpace(lastUsedAt) == "" {
		lastUsedAt = time.Now().UTC().Format(time.RFC3339)
	}
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "integration_api_keys", workspaceID).Set("last_used_at", lastUsedAt).Set("updated_at", lastUsedAt).Where(ormbuilder.Equal("api_key", key)).Build()
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, fmt.Errorf("build integration api key last-used update: %w", err)
	}
	if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
		return integrationmodel.IntegrationAPIKey{}, fmt.Errorf("update integration api key last used: %w", err)
	}
	query, args, err = ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "integration_api_keys", workspaceID).Columns(integrationAPIKeyColumns...).Where(ormbuilder.Equal("api_key", key)).Build()
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, fmt.Errorf("build integration api key lookup: %w", err)
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	value, err := scanIntegrationAPIKey(row)
	return value, err
}
