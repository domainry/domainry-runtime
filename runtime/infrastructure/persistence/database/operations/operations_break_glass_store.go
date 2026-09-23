package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	sharedoperation "github.com/domainry/domainry-foundation/operation"
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
	ledger, err := s.ledger()
	if err != nil {
		return false, err
	}
	txContext := sharedoperation.WithExecutor(ctx, tx)
	active, err := ledger.CountActiveBreakGlass(txContext, grant.WorkspaceID, string(operationsmodel.OperationsBreakGlassActive), grant.CreatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil || active > 0 {
		return false, err
	}
	approvers, _ := json.Marshal(grant.ApproverIDs)
	if err := s.store.GuardSubjectEvidenceWrite(ctx, tx, grant.WorkspaceID, sharedoperation.BreakGlassTableName,
		[]string{"id", "actor_id"}, []any{grant.ID, grant.ActorID}); err != nil {
		return false, err
	}
	for _, actor := range grant.ApproverIDs {
		if err := s.store.GuardSubjectRecordsWrite(ctx, tx, grant.WorkspaceID, "", nil, actor); err != nil {
			return false, err
		}
	}
	inserted, err := ledger.InsertBreakGlass(txContext, sharedoperation.BreakGlassGrant{
		ID: grant.ID, WorkspaceID: grant.WorkspaceID, State: string(grant.State), ActorID: grant.ActorID, ApproverIDsJSON: approvers,
		Reason: grant.Reason, IncidentRef: grant.IncidentRef, AlertTarget: grant.AlertTarget, AuditEventID: grant.AuditEventID,
		ExpiresAt: grant.ExpiresAt.UTC().Format(time.RFC3339Nano), Revision: grant.Revision,
		CreatedAt: grant.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: grant.UpdatedAt.UTC().Format(time.RFC3339Nano),
	})
	if err != nil || !inserted {
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
	ledger, err := s.ledger()
	if err != nil {
		return operationsmodel.OperationsBreakGlassGrant{}, false, err
	}
	record, found, err := ledger.GetBreakGlass(ctx, id)
	if err != nil || !found {
		return operationsmodel.OperationsBreakGlassGrant{}, found, err
	}
	grant, err := operationsBreakGlassFromRecord(record)
	return grant, err == nil, err
}

func (s OperationsStore) ListOperationsBreakGlass(ctx context.Context, workspaceID string, limit int) ([]operationsmodel.OperationsBreakGlassGrant, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	ledger, err := s.ledger()
	if err != nil {
		return nil, err
	}
	records, err := ledger.ListBreakGlass(ctx, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	grants := make([]operationsmodel.OperationsBreakGlassGrant, 0, len(records))
	for _, record := range records {
		grant, err := operationsBreakGlassFromRecord(record)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}
	return grants, nil
}

func (s OperationsStore) RevokeOperationsBreakGlass(ctx context.Context, grant operationsmodel.OperationsBreakGlassGrant, expectedRevision int64) (bool, error) {
	revokedAt := ""
	if grant.RevokedAt != nil {
		revokedAt = grant.RevokedAt.UTC().Format(time.RFC3339Nano)
	}
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.store.GuardSubjectEvidenceWrite(ctx, tx, grant.WorkspaceID, sharedoperation.BreakGlassTableName,
		[]string{"id", "revoked_by"}, []any{grant.ID, grant.RevokedBy}); err != nil {
		return false, err
	}
	ledger, err := s.ledger()
	if err != nil {
		return false, err
	}
	record := sharedoperation.BreakGlassGrant{
		ID: grant.ID, WorkspaceID: grant.WorkspaceID, State: string(grant.State), ActorID: grant.ActorID,
		ApproverIDsJSON: json.RawMessage(`[]`), Reason: grant.Reason, IncidentRef: grant.IncidentRef, AlertTarget: grant.AlertTarget,
		AuditEventID: grant.AuditEventID, ExpiresAt: grant.ExpiresAt.UTC().Format(time.RFC3339Nano), Revision: grant.Revision,
		CreatedAt: grant.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: grant.UpdatedAt.UTC().Format(time.RFC3339Nano),
		RevokedAt: revokedAt, RevokedBy: grant.RevokedBy, RevocationNote: grant.RevocationNote,
	}
	approvers, _ := json.Marshal(grant.ApproverIDs)
	record.ApproverIDsJSON = approvers
	changed, err := ledger.RevokeBreakGlass(sharedoperation.WithExecutor(ctx, tx), record, string(operationsmodel.OperationsBreakGlassActive), expectedRevision)
	if err != nil || !changed {
		return changed, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func operationsBreakGlassFromRecord(record sharedoperation.BreakGlassGrant) (operationsmodel.OperationsBreakGlassGrant, error) {
	grant := operationsmodel.OperationsBreakGlassGrant{
		ID: record.ID, WorkspaceID: record.WorkspaceID, State: operationsmodel.OperationsBreakGlassState(record.State), ActorID: record.ActorID,
		Reason: record.Reason, IncidentRef: record.IncidentRef, AlertTarget: record.AlertTarget, AuditEventID: record.AuditEventID,
		Revision: record.Revision, RevokedBy: record.RevokedBy, RevocationNote: record.RevocationNote,
	}
	var err error
	if err = json.Unmarshal(record.ApproverIDsJSON, &grant.ApproverIDs); err != nil {
		return grant, err
	}
	grant.ExpiresAt, err = time.Parse(time.RFC3339Nano, record.ExpiresAt)
	if err != nil {
		return grant, err
	}
	grant.CreatedAt, err = time.Parse(time.RFC3339Nano, record.CreatedAt)
	if err != nil {
		return grant, err
	}
	grant.UpdatedAt, err = time.Parse(time.RFC3339Nano, record.UpdatedAt)
	if err != nil {
		return grant, err
	}
	if record.RevokedAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, record.RevokedAt)
		if parseErr != nil {
			return grant, parseErr
		}
		grant.RevokedAt = &parsed
	}
	return grant, nil
}
