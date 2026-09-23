package deployment

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
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
	nowText, leaseExpiresAt := now.Format(time.RFC3339Nano), now.Add(request.LeaseTTL).Format(time.RFC3339Nano)
	if err := r.ensureIdempotencyCleanupLease(ctx, nowText); err != nil {
		return deploymentmodel.IdempotencyCleanupResult{}, err
	}
	claim, claimArgs, err := query.NewUpdateBuilder(r.store.SQLRenderer, "_worker_scopes").Set("lease_owner", request.LeaseOwner).Set("lease_expires_at", leaseExpiresAt).SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).Set("last_started_at", nowText).Set("last_error", "").Set("updated_at", nowText).Where(query.And(cleanupScopePredicate(), query.Or(query.Equal("lease_owner", ""), query.LessThanOrEqual("lease_expires_at", nowText)))).Build()
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
	lookup, lookupArgs, err := query.NewSelectBuilder(r.store.SQLRenderer, "_worker_scopes").Columns("fencing_token").Where(cleanupScopePredicate()).Build()
	if err != nil {
		return result, err
	}
	if err := r.db.QueryRowContext(ctx, lookup, lookupArgs...).Scan(&result.FencingToken); err != nil {
		return result, err
	}
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
	complete, completeArgs, err := query.NewUpdateBuilder(r.store.SQLRenderer, "_worker_scopes").Set("lease_owner", "").Set("lease_expires_at", "").Set("last_completed_at", result.CompletedAt).Set("checkpoint", result.Deleted).Set("updated_at", result.CompletedAt).Where(cleanupLeasePredicate(request.LeaseOwner, result.FencingToken)).Build()
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
	if _, registered := database.WorkerScopeRegistrationFor(database.WorkerScopeOwnerIdempotencyCleanup); !registered {
		return fmt.Errorf("idempotency cleanup worker scope owner is not registered")
	}
	queryValue, args, buildErr := query.NewInsertBuilder(r.store.SQLRenderer, "_worker_scopes").Columns("id", "owner", "scope_key", "lease_owner", "lease_expires_at", "fencing_token", "last_started_at", "last_completed_at", "checkpoint", "last_error", "updated_at").Values(idempotencyCleanupLeaseID, database.WorkerScopeOwnerIdempotencyCleanup, "receipts", "", "", 0, "", "", 0, "", now).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := r.db.ExecContext(ctx, queryValue, args...); err != nil {
		var count int
		check, checkArgs, checkErr := query.NewSelectBuilder(r.store.SQLRenderer, "_worker_scopes").Projections(query.Project(query.CountAll())).Where(cleanupScopePredicate()).Build()
		if checkErr != nil {
			return checkErr
		}
		if checkErr = r.db.QueryRowContext(ctx, check, checkArgs...).Scan(&count); checkErr != nil || count != 1 {
			return fmt.Errorf("initialize idempotency cleanup lease: %w", err)
		}
	}
	return nil
}

func (r RuntimeStatusStore) deleteExpiredReceiptBatch(ctx context.Context, spec idempotencyReceiptTable, owner string, fencingToken int64, now string, limit int) (int, error) {
	lease := cleanupLeaseGuard(r.store.SQLRenderer, owner, fencingToken, now)
	eligibility := query.And(idempotencyReceiptOwnerPredicate(spec), expiredReceiptEligibility(now))
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
			query.ExistsSubquery(cleanupLeaseGuard(r.store.SQLRenderer, owner, fencingToken, now)),
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

func expiredReceiptEligibility(now string) query.Predicate {
	return query.And(
		query.NotEqual("expires_at", ""),
		query.LessThanOrEqual("expires_at", now),
		query.NotEqual("status", string(idempotency.StatusProcessing)),
	)
}

func (r RuntimeStatusStore) failIdempotencyCleanup(ctx context.Context, owner string, fencingToken int64, now string, cause error) {
	queryValue, args, err := query.NewUpdateBuilder(r.store.SQLRenderer, "_worker_scopes").Set("lease_owner", "").Set("lease_expires_at", "").Set("last_error", cause.Error()).Set("updated_at", now).Where(cleanupLeasePredicate(owner, fencingToken)).Build()
	if err == nil {
		_, _ = r.db.ExecContext(ctx, queryValue, args...)
	}
}

func cleanupLeasePredicate(owner string, fencingToken int64) query.Predicate {
	return query.And(cleanupScopePredicate(), query.Equal("lease_owner", owner), query.Equal("fencing_token", fencingToken))
}

func cleanupLeaseGuard(renderer query.Renderer, owner string, fencingToken int64, now string) *query.SelectBuilder {
	return query.NewSelectBuilder(renderer, "_worker_scopes").Columns("id").Where(query.And(cleanupLeasePredicate(owner, fencingToken), query.GreaterThan("lease_expires_at", now)))
}

func cleanupScopePredicate() query.Predicate {
	return query.And(
		query.Equal("id", idempotencyCleanupLeaseID),
		query.Equal("owner", database.WorkerScopeOwnerIdempotencyCleanup),
		query.Equal("scope_key", "receipts"),
	)
}
