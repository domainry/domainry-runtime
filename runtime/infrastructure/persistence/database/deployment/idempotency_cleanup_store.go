package deployment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
)

const idempotencyCleanupLeaseID = "receipts"

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
	nowText, leaseExpiresAt := now.Format(time.RFC3339Nano), now.Add(request.LeaseTTL).Format(time.RFC3339Nano)
	if err := r.ensureIdempotencyCleanupLease(ctx, nowText); err != nil {
		return deploymentmodel.IdempotencyCleanupResult{}, err
	}
	claim := "UPDATE " + r.store.TableIdentifier("idempotency_cleanup_leases") + " SET " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("last_started_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("last_error") + " = '', " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(4) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(5) + " AND (" + r.store.Identifier("lease_owner") + " = '' OR " + r.store.Identifier("lease_expires_at") + " <= " + r.store.Placeholder(6) + ")"
	claimResult, err := r.db.ExecContext(ctx, claim, request.LeaseOwner, leaseExpiresAt, nowText, nowText, idempotencyCleanupLeaseID, nowText)
	if err != nil {
		return deploymentmodel.IdempotencyCleanupResult{}, fmt.Errorf("claim idempotency cleanup lease: %w", err)
	}
	acquired, err := claimResult.RowsAffected()
	if err != nil || acquired != 1 {
		return deploymentmodel.IdempotencyCleanupResult{Acquired: false}, err
	}
	result := deploymentmodel.IdempotencyCleanupResult{Acquired: true, LeaseOwner: request.LeaseOwner, StartedAt: nowText}
	if err := r.db.QueryRowContext(ctx, "SELECT "+r.store.Identifier("fencing_token")+" FROM "+r.store.TableIdentifier("idempotency_cleanup_leases")+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(1), idempotencyCleanupLeaseID).Scan(&result.FencingToken); err != nil {
		return result, err
	}
	for _, spec := range idempotencyReceiptTables {
		if result.Deleted >= request.BatchSize {
			break
		}
		deleted, deleteErr := r.deleteExpiredReceiptBatch(ctx, spec.table, request.LeaseOwner, result.FencingToken, nowText, request.BatchSize-result.Deleted)
		if deleteErr != nil {
			r.failIdempotencyCleanup(ctx, request.LeaseOwner, result.FencingToken, nowText, deleteErr)
			return result, deleteErr
		}
		result.Deleted += deleted
	}
	result.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	complete := "UPDATE " + r.store.TableIdentifier("idempotency_cleanup_leases") + " SET " + r.store.Identifier("lease_owner") + " = '', " + r.store.Identifier("lease_expires_at") + " = '', " + r.store.Identifier("last_completed_at") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("last_deleted") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(6)
	completed, err := r.db.ExecContext(ctx, complete, result.CompletedAt, result.Deleted, result.CompletedAt, idempotencyCleanupLeaseID, request.LeaseOwner, result.FencingToken)
	if err != nil {
		return result, err
	}
	rows, err := completed.RowsAffected()
	if err != nil || rows != 1 {
		return result, fmt.Errorf("idempotency cleanup lease lost")
	}
	return result, nil
}

func (r RuntimeStatusStore) ensureIdempotencyCleanupLease(ctx context.Context, now string) error {
	query := r.store.InsertStatement("idempotency_cleanup_leases", []string{"id", "lease_owner", "lease_expires_at", "fencing_token", "last_started_at", "last_completed_at", "last_deleted", "last_error", "updated_at"})
	if _, err := r.db.ExecContext(ctx, query, idempotencyCleanupLeaseID, "", "", 0, "", "", 0, "", now); err != nil {
		var count int
		if checkErr := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+r.store.TableIdentifier("idempotency_cleanup_leases")+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(1), idempotencyCleanupLeaseID).Scan(&count); checkErr != nil || count != 1 {
			return fmt.Errorf("initialize idempotency cleanup lease: %w", err)
		}
	}
	return nil
}

func (r RuntimeStatusStore) deleteExpiredReceiptBatch(ctx context.Context, table, owner string, fencingToken int64, now string, limit int) (int, error) {
	leaseGuard := "EXISTS (SELECT 1 FROM " + r.store.TableIdentifier("idempotency_cleanup_leases") + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("lease_expires_at") + " > " + r.store.Placeholder(4) + ")"
	selectQuery := "SELECT " + r.store.Identifier("id") + " FROM " + r.store.TableIdentifier(table) + " WHERE " + leaseGuard + " AND " + r.store.Identifier("expires_at") + " <> '' AND " + r.store.Identifier("expires_at") + " <= " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " <> " + r.store.Placeholder(6) + " ORDER BY " + r.store.Identifier("expires_at") + " ASC LIMIT " + r.store.Placeholder(7)
	rows, err := r.db.QueryContext(ctx, selectQuery, idempotencyCleanupLeaseID, owner, fencingToken, now, now, string(idempotency.StatusProcessing), limit)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, err
	}
	_ = rows.Close()
	if len(ids) == 0 {
		return 0, nil
	}
	args := []any{idempotencyCleanupLeaseID, owner, fencingToken, now}
	idPlaceholders := make([]string, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
		idPlaceholders = append(idPlaceholders, r.store.Placeholder(len(args)))
	}
	deleteQuery := "DELETE FROM " + r.store.TableIdentifier(table) + " WHERE " + leaseGuard + " AND " + r.store.Identifier("id") + " IN (" + strings.Join(idPlaceholders, ", ") + ")"
	deleted, err := r.db.ExecContext(ctx, deleteQuery, args...)
	if err != nil {
		return 0, err
	}
	count, err := deleted.RowsAffected()
	return int(count), err
}

func (r RuntimeStatusStore) failIdempotencyCleanup(ctx context.Context, owner string, fencingToken int64, now string, cause error) {
	query := "UPDATE " + r.store.TableIdentifier("idempotency_cleanup_leases") + " SET " + r.store.Identifier("lease_owner") + " = '', " + r.store.Identifier("lease_expires_at") + " = '', " + r.store.Identifier("last_error") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(5)
	_, _ = r.db.ExecContext(ctx, query, cause.Error(), now, idempotencyCleanupLeaseID, owner, fencingToken)
}
