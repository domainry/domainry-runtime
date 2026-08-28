package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("runtime_break_glass_grants") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("state") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("expires_at") + " > " + s.store.Placeholder(3)
	if err := tx.QueryRowContext(ctx, query, grant.WorkspaceID, string(operationsmodel.OperationsBreakGlassActive), grant.CreatedAt.UTC().Format(time.RFC3339Nano)).Scan(&active); err != nil || active > 0 {
		return false, err
	}
	approvers, _ := json.Marshal(grant.ApproverIDs)
	columns := operationsBreakGlassColumns()
	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = s.store.Placeholder(i + 1)
	}
	query = "INSERT INTO " + s.store.TableIdentifier("runtime_break_glass_grants") + " (" + operationsQuotedColumns(s.store, columns) + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	_, err = tx.ExecContext(ctx, query, grant.ID, grant.WorkspaceID, string(grant.State), grant.ActorID, string(approvers), grant.Reason, grant.IncidentRef, grant.AlertTarget, grant.AuditEventID, grant.ExpiresAt.UTC().Format(time.RFC3339Nano), grant.Revision, grant.CreatedAt.UTC().Format(time.RFC3339Nano), grant.UpdatedAt.UTC().Format(time.RFC3339Nano), "", "", "")
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
	query := "SELECT " + operationsQuotedColumns(s.store, operationsBreakGlassColumns()) + " FROM " + s.store.TableIdentifier("runtime_break_glass_grants") + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(1)
	grant, err := operationsScanBreakGlass(s.database().QueryRowContext(ctx, query, strings.TrimSpace(id)))
	if err == sql.ErrNoRows {
		return operationsmodel.OperationsBreakGlassGrant{}, false, nil
	}
	return grant, err == nil, err
}

func (s OperationsStore) ListOperationsBreakGlass(ctx context.Context, workspaceID string, limit int) ([]operationsmodel.OperationsBreakGlassGrant, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := "SELECT " + operationsQuotedColumns(s.store, operationsBreakGlassColumns()) + " FROM " + s.store.TableIdentifier("runtime_break_glass_grants") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " ORDER BY " + s.store.Identifier("created_at") + " DESC LIMIT " + s.store.Placeholder(2)
	rows, err := s.database().QueryContext(ctx, query, workspaceID, limit)
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
	query := "UPDATE " + s.store.TableIdentifier("runtime_break_glass_grants") + " SET " + s.store.Identifier("state") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("revision") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("revoked_at") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("revoked_by") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("revocation_note") + " = " + s.store.Placeholder(6) + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("state") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("revision") + " = " + s.store.Placeholder(9)
	revokedAt := ""
	if grant.RevokedAt != nil {
		revokedAt = grant.RevokedAt.UTC().Format(time.RFC3339Nano)
	}
	result, err := s.database().ExecContext(ctx, query, string(grant.State), grant.Revision, grant.UpdatedAt.UTC().Format(time.RFC3339Nano), revokedAt, grant.RevokedBy, grant.RevocationNote, grant.ID, string(operationsmodel.OperationsBreakGlassActive), expectedRevision)
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
