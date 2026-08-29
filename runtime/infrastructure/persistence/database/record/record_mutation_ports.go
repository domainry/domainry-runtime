package record

import (
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"context"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
)

func (r RecordStore) ApplyRecordMutationTx(ctx context.Context, tx TransactionExecutor, workspaceID string, commit transactionmodel.RecordMutationCommit) error {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	return r.applyRecordMutationTx(ctx, tx, workspaceID, commit)
}

// ApplyAuditTx lets another owner adapter include explicit evidence in its
// transaction without exposing SQL to Domain/Application code.
func (r RecordStore) ApplyAuditTx(ctx context.Context, tx TransactionExecutor, event auditmodel.AuditEvent) error {
	return r.insertAuditEventTx(ctx, tx, event)
}
