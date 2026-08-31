package deployment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-orm/query"
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
	claim, claimArgs, err := query.NewUpdateBuilder(r.store.SQLRenderer, "_idempotency_cleanup_leases").Set("lease_owner", request.LeaseOwner).Set("lease_expires_at", leaseExpiresAt).SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).Set("last_started_at", nowText).Set("last_error", "").Set("updated_at", nowText).Where(query.And(query.Equal("id", idempotencyCleanupLeaseID), query.Or(query.Equal("lease_owner", ""), query.LessThanOrEqual("lease_expires_at", nowText)))).Build()
	if err != nil {
		return deploymentmodel.IdempotencyCleanupResult{}, fmt.Errorf("build idempotency cleanup lease claim: %w", err)
	}
	claimResult, err := r.db.ExecContext(ctx, claim, claimArgs...)
	if err != nil {
		return deploymentmodel.IdempotencyCleanupResult{}, fmt.Errorf("claim idempotency cleanup lease: %w", err)
	}
	acquired, err := claimResult.RowsAffected()
	if err != nil || acquired != 1 {
		return deploymentmodel.IdempotencyCleanupResult{Acquired: false}, err
	}
	result := deploymentmodel.IdempotencyCleanupResult{Acquired: true, LeaseOwner: request.LeaseOwner, StartedAt: nowText}
	lookup, lookupArgs, err := query.NewSelectBuilder(r.store.SQLRenderer, "_idempotency_cleanup_leases").Columns("fencing_token").Where(query.Equal("id", idempotencyCleanupLeaseID)).Build()
	if err != nil {
		return result, err
	}
	if err := r.db.QueryRowContext(ctx, lookup, lookupArgs...).Scan(&result.FencingToken); err != nil {
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
	complete, completeArgs, err := query.NewUpdateBuilder(r.store.SQLRenderer, "_idempotency_cleanup_leases").Set("lease_owner", "").Set("lease_expires_at", "").Set("last_completed_at", result.CompletedAt).Set("last_deleted", result.Deleted).Set("updated_at", result.CompletedAt).Where(cleanupLeasePredicate(request.LeaseOwner, result.FencingToken)).Build()
	if err != nil {
		return result, err
	}
	completed, err := r.db.ExecContext(ctx, complete, completeArgs...)
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
	queryValue, args, buildErr := query.NewInsertBuilder(r.store.SQLRenderer, "_idempotency_cleanup_leases").Columns("id", "lease_owner", "lease_expires_at", "fencing_token", "last_started_at", "last_completed_at", "last_deleted", "last_error", "updated_at").Values(idempotencyCleanupLeaseID, "", "", 0, "", "", 0, "", now).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := r.db.ExecContext(ctx, queryValue, args...); err != nil {
		var count int
		check, checkArgs, checkErr := query.NewSelectBuilder(r.store.SQLRenderer, "_idempotency_cleanup_leases").Projections(query.Project(query.CountAll())).Where(query.Equal("id", idempotencyCleanupLeaseID)).Build()
		if checkErr != nil {
			return checkErr
		}
		if checkErr = r.db.QueryRowContext(ctx, check, checkArgs...).Scan(&count); checkErr != nil || count != 1 {
			return fmt.Errorf("initialize idempotency cleanup lease: %w", err)
		}
	}
	return nil
}

func (r RuntimeStatusStore) deleteExpiredReceiptBatch(ctx context.Context, table, owner string, fencingToken int64, now string, limit int) (int, error) {
	lease := cleanupLeaseGuard(r.store.SQLRenderer, owner, fencingToken, now)
	selectQuery, selectArgs, err := query.NewSelectBuilder(r.store.SQLRenderer, table).Columns("id").Where(query.And(query.ExistsSubquery(lease), query.NotEqual("expires_at", ""), query.LessThanOrEqual("expires_at", now), query.NotEqual("status", string(idempotency.StatusProcessing)))).OrderBy(query.Ascending("expires_at"), query.Ascending("id")).Limit(limit).Build()
	if err != nil {
		return 0, err
	}
	rows, err := r.db.QueryContext(ctx, selectQuery, selectArgs...)
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
	idValues := make([]any, 0, len(ids))
	for _, id := range ids {
		idValues = append(idValues, id)
	}
	deleteQuery, deleteArgs, err := query.NewDeleteBuilder(r.store.SQLRenderer, table).Where(query.And(query.ExistsSubquery(cleanupLeaseGuard(r.store.SQLRenderer, owner, fencingToken, now)), query.In("id", idValues...))).Build()
	if err != nil {
		return 0, err
	}
	deleted, err := r.db.ExecContext(ctx, deleteQuery, deleteArgs...)
	if err != nil {
		return 0, err
	}
	count, err := deleted.RowsAffected()
	return int(count), err
}

func (r RuntimeStatusStore) failIdempotencyCleanup(ctx context.Context, owner string, fencingToken int64, now string, cause error) {
	queryValue, args, err := query.NewUpdateBuilder(r.store.SQLRenderer, "_idempotency_cleanup_leases").Set("lease_owner", "").Set("lease_expires_at", "").Set("last_error", cause.Error()).Set("updated_at", now).Where(cleanupLeasePredicate(owner, fencingToken)).Build()
	if err == nil {
		_, _ = r.db.ExecContext(ctx, queryValue, args...)
	}
}

func cleanupLeasePredicate(owner string, fencingToken int64) query.Predicate {
	return query.And(query.Equal("id", idempotencyCleanupLeaseID), query.Equal("lease_owner", owner), query.Equal("fencing_token", fencingToken))
}

func cleanupLeaseGuard(renderer query.Renderer, owner string, fencingToken int64, now string) *query.SelectBuilder {
	return query.NewSelectBuilder(renderer, "_idempotency_cleanup_leases").Columns("id").Where(query.And(cleanupLeasePredicate(owner, fencingToken), query.GreaterThan("lease_expires_at", now)))
}
