package deployment

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	"github.com/domainry/domainry-orm/query"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const idempotencyCleanupLeaseID = "worker_scope:idempotency_cleanup:receipts"

func (r RuntimeStatusStore) RunIdempotencyCleanup(ctx context.Context, request deploymentmodel.IdempotencyCleanupRequest) (deploymentmodel.IdempotencyCleanupResult, error) {
	request.LeaseOwner = strings.TrimSpace(request.LeaseOwner)
	if request.LeaseOwner == "" {
		return deploymentmodel.IdempotencyCleanupResult{}, fmt.Errorf("idempotency cleanup lease owner is required")
	}
	if request.LeaseTTL <= 0 {
		request.LeaseTTL = 2 * time.Minute
	}
	if request.BatchSize <= 0 || request.BatchSize > 5000 {
		request.BatchSize = 500
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowText := now.Format(time.RFC3339Nano)
	if err := r.ensureIdempotencyCleanupLease(ctx, nowText); err != nil {
		return deploymentmodel.IdempotencyCleanupResult{}, err
	}
	lease, acquired, err := r.workerScopeStore().ClaimLease(ctx, nil, sharedworkerscope.LeaseClaim{
		Identity: idempotencyCleanupIdentity(), LeaseOwner: request.LeaseOwner, Now: now, LeaseExpiresAt: now.Add(request.LeaseTTL),
	})
	if err != nil || !acquired {
		return deploymentmodel.IdempotencyCleanupResult{Acquired: false}, err
	}
	result := deploymentmodel.IdempotencyCleanupResult{Acquired: true, LeaseOwner: request.LeaseOwner, FencingToken: lease.FencingToken, StartedAt: nowText}
	for _, spec := range idempotencyReceiptTables {
		if !r.store.RuntimeSchemaCapabilities().IncludesTable(spec.table) {
			continue
		}
		if result.Deleted >= request.BatchSize {
			break
		}
		deleted, deleteErr := r.deleteExpiredReceiptBatch(ctx, spec, request.LeaseOwner, result.FencingToken, nowText, request.BatchSize-result.Deleted)
		if deleteErr != nil {
			r.failIdempotencyCleanup(ctx, request.LeaseOwner, result.FencingToken, nowText, deleteErr)
			return result, deleteErr
		}
		result.Deleted += deleted
	}
	result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	completedAt, err := time.Parse(time.RFC3339Nano, result.CompletedAt)
	if err != nil {
		return result, err
	}
	completed, err := r.workerScopeStore().CompleteLease(ctx, nil, sharedworkerscope.LeaseCompletion{
		Identity: idempotencyCleanupIdentity(), LeaseOwner: request.LeaseOwner, FencingToken: result.FencingToken,
		CompletedAt: completedAt, Checkpoint: int64(result.Deleted),
	})
	if err != nil {
		return result, err
	}
	if !completed {
		return result, fmt.Errorf("idempotency cleanup lease lost")
	}
	return result, nil
}

func (r RuntimeStatusStore) ensureIdempotencyCleanupLease(ctx context.Context, now string) error {
	value, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return err
	}
	return r.workerScopeStore().Register(ctx, nil, idempotencyCleanupIdentity(), value)
}

func (r RuntimeStatusStore) deleteExpiredReceiptBatch(ctx context.Context, spec idempotencyReceiptTable, owner string, fencingToken int64, now string, limit int) (int, error) {
	if spec.table == "" {
		return r.deleteExpiredOperationBatch(ctx, spec, owner, fencingToken, now, limit)
	}
	lease, err := cleanupLeaseGuard(r.workerScopeStore(), owner, fencingToken, now)
	if err != nil {
		return 0, err
	}
	expiry, err := expiredReceiptEligibility(now)
	if err != nil {
		return 0, err
	}
	eligibility := query.And(idempotencyReceiptOwnerPredicate(spec), expiry)
	selectQuery, selectArgs, err := query.NewSelectBuilder(r.store.SQLRenderer, spec.table).Columns("id", "workspace_id").Where(query.And(query.ExistsSubquery(lease), eligibility)).OrderBy(query.Ascending("expires_at"), query.Ascending("id")).Limit(limit).Build()
	if err != nil {
		return 0, err
	}
	rows, err := r.db.QueryContext(ctx, selectQuery, selectArgs...)
	if err != nil {
		return 0, err
	}
	type receiptLocator struct{ id, workspaceID string }
	locators := []receiptLocator{}
	for rows.Next() {
		var locator receiptLocator
		if err := rows.Scan(&locator.id, &locator.workspaceID); err != nil {
			_ = rows.Close()
			return 0, err
		}
		locators = append(locators, locator)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()
	if len(locators) == 0 {
		return 0, nil
	}
	if r.beforeDeleteExpiredReceipts != nil {
		r.beforeDeleteExpiredReceipts()
	}
	workspaceOrder := make([]string, 0)
	idsByWorkspace := make(map[string][]any)
	for _, locator := range locators {
		if _, exists := idsByWorkspace[locator.workspaceID]; !exists {
			workspaceOrder = append(workspaceOrder, locator.workspaceID)
		}
		idsByWorkspace[locator.workspaceID] = append(idsByWorkspace[locator.workspaceID], locator.id)
	}
	total := 0
	for _, workspaceID := range workspaceOrder {
		deleteQuery, deleteArgs, buildErr := query.NewDeleteBuilder(r.store.SQLRenderer, spec.table).Where(query.And(
			query.ExistsSubquery(lease),
			query.Equal("workspace_id", workspaceID),
			query.In("id", idsByWorkspace[workspaceID]...),
			eligibility,
		)).Build()
		if buildErr != nil {
			return total, buildErr
		}
		deleted, deleteErr := r.db.ExecContext(ctx, deleteQuery, deleteArgs...)
		if deleteErr != nil {
			return total, deleteErr
		}
		count, rowsErr := deleted.RowsAffected()
		if rowsErr != nil {
			return total, rowsErr
		}
		total += int(count)
	}
	return total, nil
}

func (r RuntimeStatusStore) deleteExpiredOperationBatch(ctx context.Context, spec idempotencyReceiptTable, owner string, fencingToken int64, now string, limit int) (int, error) {
	filter := sharedoperation.RecordFilter{AllScopes: true, Owner: spec.rowOwner}
	records, err := r.operationStore().ListExpiredRecordLocators(ctx, filter, now, string(idempotency.StatusProcessing), limit)
	if err != nil || len(records) == 0 {
		return 0, err
	}
	if r.beforeDeleteExpiredReceipts != nil {
		r.beforeDeleteExpiredReceipts()
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	lease, err := cleanupLeaseGuard(r.workerScopeStore(), owner, fencingToken, now)
	if err != nil {
		return 0, err
	}
	leaseQuery, leaseArgs, err := lease.Build()
	if err != nil {
		return 0, err
	}
	var leaseID string
	if err := tx.QueryRowContext(ctx, leaseQuery, leaseArgs...).Scan(&leaseID); err != nil {
		if err == sql.ErrNoRows {
			return 0, nil
		}
		return 0, err
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.ID)
	}
	deleted, err := r.operationStore().DeleteExpiredRecords(sharedoperation.WithExecutor(ctx, tx), filter, ids, now, string(idempotency.StatusProcessing))
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(deleted), nil
}

func expiredReceiptEligibility(now string) (query.Predicate, error) {
	value, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return nil, err
	}
	return query.And(
		query.NotEqual("expires_at", int64(0)),
		query.LessThanOrEqual("expires_at", value.UTC().UnixMilli()),
		query.NotEqual("status", string(idempotency.StatusProcessing)),
	), nil
}

func (r RuntimeStatusStore) failIdempotencyCleanup(ctx context.Context, owner string, fencingToken int64, now string, cause error) {
	failedAt, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return
	}
	_, _ = r.workerScopeStore().FailLease(ctx, nil, sharedworkerscope.LeaseFailure{Identity: idempotencyCleanupIdentity(), LeaseOwner: owner, FencingToken: fencingToken, FailedAt: failedAt, Cause: cause})
}

func (r RuntimeStatusStore) workerScopeStore() *sharedworkerscope.Store {
	return sharedworkerscope.NewStore(r.db, r.store.SQLRenderer)
}

func cleanupLeaseGuard(store *sharedworkerscope.Store, owner string, fencingToken int64, now string) (*query.SelectBuilder, error) {
	value, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		return nil, err
	}
	return store.ActiveLeaseGuard(idempotencyCleanupIdentity(), owner, fencingToken, value)
}

func idempotencyCleanupIdentity() sharedworkerscope.Identity {
	return sharedworkerscope.Identity{ID: idempotencyCleanupLeaseID, Owner: database.WorkerScopeOwnerIdempotencyCleanup, ScopeKey: "receipts"}
}
