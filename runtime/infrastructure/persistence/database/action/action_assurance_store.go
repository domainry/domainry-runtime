package action

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type ActionAssuranceStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewActionAssuranceStore(store *database.RuntimeStore) ActionAssuranceStore {
	return ActionAssuranceStore{store: store, db: store.DB()}
}

func (s ActionAssuranceStore) SaveActionAssuranceGrant(ctx context.Context, grant actionmodel.ActionAssuranceGrant) error {
	methods, _ := json.Marshal(grant.Methods)
	columns := []string{"id", "token_hash", "workspace_id", "user_id", "action_key", "object_key", "record_id", "payload_digest", "methods_json", "approval_version", "approval_hash", "issued_at", "expires_at", "consumed_at"}
	values := []any{grant.ID, grant.TokenHash, grant.WorkspaceID, grant.UserID, grant.ActionKey, grant.ObjectKey, grant.RecordID, grant.PayloadDigest, string(methods), grant.ApprovalVersion, grant.ApprovalHash, grant.IssuedAt, grant.ExpiresAt, grant.ConsumedAt}
	_, err := s.db.ExecContext(ctx, s.store.InsertStatement("action_assurance_grants", columns), values...)
	if err != nil {
		return fmt.Errorf("save action assurance grant: %w", err)
	}
	return nil
}

func (s ActionAssuranceStore) GetActionAssuranceGrant(ctx context.Context, id string) (actionmodel.ActionAssuranceGrant, bool, error) {
	columns := []string{"id", "token_hash", "workspace_id", "user_id", "action_key", "object_key", "record_id", "payload_digest", "methods_json", "approval_version", "approval_hash", "issued_at", "expires_at", "consumed_at"}
	query := "SELECT " + assuranceJoinIdentifiers(s.store, columns) + " FROM " + s.store.TableIdentifier("action_assurance_grants") + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(1)
	var grant actionmodel.ActionAssuranceGrant
	var methods string
	err := s.db.QueryRowContext(ctx, query, id).Scan(&grant.ID, &grant.TokenHash, &grant.WorkspaceID, &grant.UserID, &grant.ActionKey, &grant.ObjectKey, &grant.RecordID, &grant.PayloadDigest, &methods, &grant.ApprovalVersion, &grant.ApprovalHash, &grant.IssuedAt, &grant.ExpiresAt, &grant.ConsumedAt)
	if err == sql.ErrNoRows {
		return actionmodel.ActionAssuranceGrant{}, false, nil
	}
	if err != nil {
		return actionmodel.ActionAssuranceGrant{}, false, fmt.Errorf("get action assurance grant: %w", err)
	}
	if err := json.Unmarshal([]byte(methods), &grant.Methods); err != nil {
		return actionmodel.ActionAssuranceGrant{}, false, fmt.Errorf("decode action assurance methods: %w", err)
	}
	return grant, true, nil
}

func (s ActionAssuranceStore) ConsumeActionAssuranceGrant(ctx context.Context, id string, now time.Time) (bool, error) {
	value := now.UTC().Format(time.RFC3339Nano)
	query := "UPDATE " + s.store.TableIdentifier("action_assurance_grants") + " SET " + s.store.Identifier("consumed_at") + " = " + s.store.Placeholder(1) + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("consumed_at") + " = '' AND " + s.store.Identifier("expires_at") + " > " + s.store.Placeholder(3)
	result, err := s.db.ExecContext(ctx, query, value, id, value)
	if err != nil {
		return false, fmt.Errorf("consume action assurance grant: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect action assurance consume: %w", err)
	}
	return affected == 1, nil
}

func assuranceJoinIdentifiers(store *database.RuntimeStore, values []string) string {
	result := ""
	for index, value := range values {
		if index > 0 {
			result += ", "
		}
		result += store.Identifier(value)
	}
	return result
}
