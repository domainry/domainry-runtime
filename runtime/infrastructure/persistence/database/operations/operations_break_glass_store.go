package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.OperationsBreakGlassRepository = OperationsStore{}

func (s OperationsStore) CreateOperationsBreakGlass(ctx context.Context, grant operationsmodel.OperationsBreakGlassGrant) (bool, error) {
	if s.database() == nil {
		return false, fmt.Errorf("operations store unavailable")
	}
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var active int64
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_operation_break_glass_grants", grant.WorkspaceID).Projections(query.Project(query.CountAll())).Where(query.And(query.Equal("state", string(operationsmodel.OperationsBreakGlassActive)), query.GreaterThan("expires_at", grant.CreatedAt.UTC().Format(time.RFC3339Nano)))).Build()
	if buildErr != nil {
		return false, buildErr
	}
	if err := tx.QueryRowContext(ctx, queryValue, args...).Scan(&active); err != nil || active > 0 {
		return false, err
	}
	approvers, _ := json.Marshal(grant.ApproverIDs)
	queryValue, args, buildErr = query.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "_operation_break_glass_grants", grant.WorkspaceID).Columns("id", "state", "actor_id", "approver_ids_json", "reason", "incident_ref", "alert_target", "audit_event_id", "expires_at", "revision", "created_at", "updated_at", "revoked_at", "revoked_by", "revocation_note").Values(grant.ID, string(grant.State), grant.ActorID, string(approvers), grant.Reason, grant.IncidentRef, grant.AlertTarget, grant.AuditEventID, grant.ExpiresAt.UTC().Format(time.RFC3339Nano), grant.Revision, grant.CreatedAt.UTC().Format(time.RFC3339Nano), grant.UpdatedAt.UTC().Format(time.RFC3339Nano), "", "", "").Build()
	if buildErr != nil {
		return false, buildErr
	}
	_, err = tx.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s OperationsStore) GetOperationsBreakGlass(ctx context.Context, id string) (operationsmodel.OperationsBreakGlassGrant, bool, error) {
	if s.database() == nil {
		return operationsmodel.OperationsBreakGlassGrant{}, false, fmt.Errorf("operations store unavailable")
	}
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, "_operation_break_glass_grants").Columns(operationsBreakGlassColumns()...).Where(query.Equal("id", id)).Build()
	if buildErr != nil {
		return operationsmodel.OperationsBreakGlassGrant{}, false, buildErr
	}
	grant, err := operationsScanBreakGlass(s.database().QueryRowContext(ctx, queryValue, args...))
	if err == sql.ErrNoRows {
		return operationsmodel.OperationsBreakGlassGrant{}, false, nil
	}
	return grant, err == nil, err
}

func (s OperationsStore) ListOperationsBreakGlass(ctx context.Context, workspaceID string, limit int) ([]operationsmodel.OperationsBreakGlassGrant, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	queryValue, args, buildErr := query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "_operation_break_glass_grants", workspaceID).Columns(operationsBreakGlassColumns()...).OrderBy(query.Descending("created_at"), query.Descending("id")).Limit(limit).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := s.database().QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	grants := []operationsmodel.OperationsBreakGlassGrant{}
	for rows.Next() {
		grant, scanErr := operationsScanBreakGlass(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		grants = append(grants, grant)
	}
	return grants, rows.Err()
}

func (s OperationsStore) RevokeOperationsBreakGlass(ctx context.Context, grant operationsmodel.OperationsBreakGlassGrant, expectedRevision int64) (bool, error) {
	revokedAt := ""
	if grant.RevokedAt != nil {
		revokedAt = grant.RevokedAt.UTC().Format(time.RFC3339Nano)
	}
	queryValue, args, buildErr := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_operation_break_glass_grants", grant.WorkspaceID).Set("state", string(grant.State)).Set("revision", grant.Revision).Set("updated_at", grant.UpdatedAt.UTC().Format(time.RFC3339Nano)).Set("revoked_at", revokedAt).Set("revoked_by", grant.RevokedBy).Set("revocation_note", grant.RevocationNote).Where(query.And(query.Equal("id", grant.ID), query.Equal("state", string(operationsmodel.OperationsBreakGlassActive)), query.Equal("revision", expectedRevision))).Build()
	if buildErr != nil {
		return false, buildErr
	}
	result, err := s.database().ExecContext(ctx, queryValue, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func operationsBreakGlassColumns() []string {
	return []string{"id", "workspace_id", "state", "actor_id", "approver_ids_json", "reason", "incident_ref", "alert_target", "audit_event_id", "expires_at", "revision", "created_at", "updated_at", "revoked_at", "revoked_by", "revocation_note"}
}
func operationsScanBreakGlass(scanner operationsScanner) (operationsmodel.OperationsBreakGlassGrant, error) {
	var grant operationsmodel.OperationsBreakGlassGrant
	var state, approvers, expiresAt, createdAt, updatedAt, revokedAt string
	err := scanner.Scan(&grant.ID, &grant.WorkspaceID, &state, &grant.ActorID, &approvers, &grant.Reason, &grant.IncidentRef, &grant.AlertTarget, &grant.AuditEventID, &expiresAt, &grant.Revision, &createdAt, &updatedAt, &revokedAt, &grant.RevokedBy, &grant.RevocationNote)
	if err != nil {
		return grant, err
	}
	grant.State = operationsmodel.OperationsBreakGlassState(state)
	if err = json.Unmarshal([]byte(approvers), &grant.ApproverIDs); err != nil {
		return grant, err
	}
	grant.ExpiresAt, err = time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return grant, err
	}
	grant.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return grant, err
	}
	grant.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return grant, err
	}
	if revokedAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, revokedAt)
		if parseErr != nil {
			return grant, parseErr
		}
		grant.RevokedAt = &parsed
	}
	return grant, nil
}
