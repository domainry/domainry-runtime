package action

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/query"
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
	columns := []string{"id", "token_hash", "user_id", "action_key", "object_key", "record_id", "payload_digest", "methods_json", "approval_version", "approval_hash", "issued_at", "expires_at", "consumed_at"}
	values := []any{grant.ID, grant.TokenHash, grant.UserID, grant.ActionKey, grant.ObjectKey, grant.RecordID, grant.PayloadDigest, string(methods), grant.ApprovalVersion, grant.ApprovalHash, grant.IssuedAt, grant.ExpiresAt, grant.ConsumedAt}
	query, args, err := ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "action_assurance_grants", grant.WorkspaceID).Columns(columns...).Values(values...).Build()
	if err == nil {
		_, err = s.db.ExecContext(ctx, query, args...)
	}
	if err != nil {
		return fmt.Errorf("save action assurance grant: %w", err)
	}
	return nil
}

func (s ActionAssuranceStore) GetActionAssuranceGrant(ctx context.Context, id string) (actionmodel.ActionAssuranceGrant, bool, error) {
	columns := []string{"id", "token_hash", "workspace_id", "user_id", "action_key", "object_key", "record_id", "payload_digest", "methods_json", "approval_version", "approval_hash", "issued_at", "expires_at", "consumed_at"}
	query, args, err := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "action_assurance_grants").Columns(columns...).Where(ormbuilder.Equal("id", id)).Build()
	if err != nil {
		return actionmodel.ActionAssuranceGrant{}, false, fmt.Errorf("build action assurance lookup: %w", err)
	}
	var grant actionmodel.ActionAssuranceGrant
	var methods string
	err = s.db.QueryRowContext(ctx, query, args...).Scan(&grant.ID, &grant.TokenHash, &grant.WorkspaceID, &grant.UserID, &grant.ActionKey, &grant.ObjectKey, &grant.RecordID, &grant.PayloadDigest, &methods, &grant.ApprovalVersion, &grant.ApprovalHash, &grant.IssuedAt, &grant.ExpiresAt, &grant.ConsumedAt)
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

func (s ActionAssuranceStore) ConsumeActionAssuranceGrant(ctx context.Context, workspaceID, id string, now time.Time) (bool, error) {
	value := now.UTC().Format(time.RFC3339Nano)
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "action_assurance_grants", workspaceID).Set("consumed_at", value).Where(ormbuilder.And(
		ormbuilder.Equal("id", id), ormbuilder.Equal("consumed_at", ""), ormbuilder.GreaterThan("expires_at", value),
	)).Build()
	if err != nil {
		return false, fmt.Errorf("build action assurance consume: %w", err)
	}
	result, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("consume action assurance grant: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect action assurance consume: %w", err)
	}
	return affected == 1, nil
}
