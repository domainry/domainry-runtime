// Runtime status persistence.
package deployment

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type RuntimeStatusStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewRuntimeStatusStore(store *database.RuntimeStore) RuntimeStatusStore {
	return RuntimeStatusStore{store: store, db: store.DB()}
}

func (r RuntimeStatusStore) IdempotencyOperationalStatus(ctx context.Context, workspaceID string, now time.Time) (deploymentmodel.IdempotencyOperationalStatus, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return deploymentmodel.IdempotencyOperationalStatus{}, err
	}
	return r.idempotencyOperationalStatus(ctx, workspace.String(), now)
}

func (r RuntimeStatusStore) IdempotencyOperationalStatusForSystem(ctx context.Context, scope principalmodel.SystemScope, now time.Time) (deploymentmodel.IdempotencyOperationalStatus, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return deploymentmodel.IdempotencyOperationalStatus{}, err
	}
	return r.idempotencyOperationalStatus(ctx, "", now)
}

func (r RuntimeStatusStore) idempotencyOperationalStatus(ctx context.Context, workspaceID string, now time.Time) (deploymentmodel.IdempotencyOperationalStatus, error) {
	status := deploymentmodel.IdempotencyOperationalStatus{Backlog: map[string]int{}, Cleanup: deploymentmodel.IdempotencyCleanupStatus{State: "not_started"}}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	nowText := now.UTC().Format(time.RFC3339Nano)
	for _, spec := range idempotencyReceiptTables {
		query := "SELECT " + r.store.Identifier("status") + ", COUNT(*) FROM " + r.store.TableIdentifier(spec.table)
		args := []any{}
		if workspaceID != "" {
			query += " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1)
			args = append(args, workspaceID)
		}
		query += " GROUP BY " + r.store.Identifier("status")
		rows, err := r.db.QueryContext(ctx, query, args...)
		if err != nil {
			return status, fmt.Errorf("summarize %s idempotency backlog: %w", spec.owner, err)
		}
		for rows.Next() {
			var receiptStatus string
			var count int
			if err := rows.Scan(&receiptStatus, &count); err != nil {
				_ = rows.Close()
				return status, err
			}
			status.Backlog[receiptStatus] += count
			status.BacklogTotal += count
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return status, err
		}
		_ = rows.Close()

		expiredQuery := "SELECT COUNT(*) FROM " + r.store.TableIdentifier(spec.table) + " WHERE " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("lease_expires_at") + " <> '' AND " + r.store.Identifier("lease_expires_at") + " <= " + r.store.Placeholder(2)
		expiredArgs := []any{string(idempotency.StatusProcessing), nowText}
		if workspaceID != "" {
			expiredQuery += " AND " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(3)
			expiredArgs = append(expiredArgs, workspaceID)
		}
		var expired int
		if err := r.db.QueryRowContext(ctx, expiredQuery, expiredArgs...).Scan(&expired); err != nil {
			return status, fmt.Errorf("count %s expired idempotency leases: %w", spec.owner, err)
		}
		status.ExpiredLeases += expired
	}

	metrics := r.IdempotencyMetrics(ctx)
	metricTotal := func(outcome idempotency.Outcome) int64 {
		if workspaceID == "" {
			return metrics.Totals[outcome]
		}
		var total int64
		for _, counter := range metrics.Counters {
			if counter.WorkspaceID == workspaceID && counter.Outcome == outcome {
				total += counter.Count
			}
		}
		return total
	}
	status.Conflicts = metricTotal(idempotency.OutcomeConflict)
	status.Acquired = metricTotal(idempotency.OutcomeAcquired)
	status.Replayed = metricTotal(idempotency.OutcomeReplayed)
	status.LeaseLost = metricTotal(idempotency.OutcomeLeaseLost)
	status.DuplicateSideEffects = metricTotal(idempotency.OutcomeDuplicateSideEffect)

	cleanupQuery := "SELECT " + strings.Join([]string{r.store.Identifier("lease_owner"), r.store.Identifier("lease_expires_at"), r.store.Identifier("fencing_token"), r.store.Identifier("last_started_at"), r.store.Identifier("last_completed_at"), r.store.Identifier("last_deleted"), r.store.Identifier("last_error")}, ", ") + " FROM " + r.store.TableIdentifier("idempotency_cleanup_leases") + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(1)
	err := r.db.QueryRowContext(ctx, cleanupQuery, idempotencyCleanupLeaseID).Scan(&status.Cleanup.LeaseOwner, &status.Cleanup.LeaseExpiresAt, &status.Cleanup.FencingToken, &status.Cleanup.LastStartedAt, &status.Cleanup.LastCompletedAt, &status.Cleanup.LastDeleted, &status.Cleanup.LastError)
	if err != nil {
		if err == sql.ErrNoRows {
			status.EvaluateAlerts()
			return status, nil
		}
		return status, fmt.Errorf("read idempotency cleanup status: %w", err)
	}
	switch {
	case status.Cleanup.LeaseOwner != "" && status.Cleanup.LeaseExpiresAt > nowText:
		status.Cleanup.State = "running"
	case status.Cleanup.LastError != "":
		status.Cleanup.State = "failed"
	case status.Cleanup.LastCompletedAt != "":
		status.Cleanup.State = "completed"
	default:
		status.Cleanup.State = "idle"
	}
	status.EvaluateAlerts()
	return status, nil
}

type idempotencyReceiptTable struct {
	owner, table, scopeColumn, targetColumn string
}

var idempotencyReceiptTables = []idempotencyReceiptTable{
	{owner: "record", table: "record_mutation_executions", scopeColumn: "operation"},
	{owner: "action", table: "business_action_executions", targetColumn: "record_id"},
	{owner: "workflow", table: "workflow_execution_receipts"},
	{owner: "changeplan", table: "business_change_plan_operations", scopeColumn: "operation"},
}

func (r RuntimeStatusStore) ListIdempotencyReceipts(ctx context.Context, workspaceID, status string, limit int) ([]idempotency.ReceiptSummary, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	workspaceID, status = workspace.String(), strings.TrimSpace(status)
	values := []idempotency.ReceiptSummary{}
	for _, spec := range idempotencyReceiptTables {
		scopeColumn := "''"
		if spec.scopeColumn != "" {
			scopeColumn = r.store.Identifier(spec.scopeColumn)
		}
		targetColumn := "''"
		if spec.targetColumn != "" {
			targetColumn = r.store.Identifier(spec.targetColumn)
		}
		query := "SELECT " + strings.Join([]string{r.store.Identifier("id"), r.store.Identifier("workspace_id"), scopeColumn, targetColumn, r.store.Identifier("idempotency_key"), r.store.Identifier("request_fingerprint"), r.store.Identifier("status"), r.store.Identifier("fencing_token"), r.store.Identifier("updated_at"), r.store.Identifier("expires_at")}, ", ") + " FROM " + r.store.TableIdentifier(spec.table) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1)
		args := []any{workspaceID}
		if status != "" {
			query += " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(2)
			args = append(args, status)
		}
		query += " ORDER BY " + r.store.Identifier("updated_at") + " DESC LIMIT " + r.store.Placeholder(len(args)+1)
		args = append(args, limit)
		rows, err := r.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("list %s idempotency receipts: %w", spec.owner, err)
		}
		for rows.Next() {
			var id, workspace, scopeValue, targetValue, key, fingerprint, receiptStatus, updatedAt, expiresAt string
			var fencingToken int64
			if err := rows.Scan(&id, &workspace, &scopeValue, &targetValue, &key, &fingerprint, &receiptStatus, &fencingToken, &updatedAt, &expiresAt); err != nil {
				_ = rows.Close()
				return nil, err
			}
			scope := idempotencyReceiptScope(spec.owner, scopeValue, targetValue)
			values = append(values, idempotency.ReceiptSummaryFromValues(spec.owner, id, workspace, scope, key, fingerprint, receiptStatus, fencingToken, updatedAt, expiresAt))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	sort.Slice(values, func(i, j int) bool { return values[i].UpdatedAt > values[j].UpdatedAt })
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func idempotencyReceiptScope(owner, value, target string) string {
	switch owner {
	case "record":
		return "record." + strings.TrimSpace(value)
	case "action":
		if strings.TrimSpace(target) == "" {
			return "action.execute_object"
		}
		return "action.execute_record"
	case "workflow":
		return "workflow.execute"
	case "changeplan":
		return "change_plan." + strings.TrimSpace(value)
	case "auth":
		return strings.TrimSpace(value)
	default:
		return owner
	}
}

func (r RuntimeStatusStore) RetryIdempotencyReceipt(ctx context.Context, workspaceID, owner, id string) (bool, error) {
	return r.transitionIdempotencyReceipt(ctx, workspaceID, owner, id, string(idempotency.StatusFailedRetryable), string(idempotency.StatusFailedRetryable))
}

func (r RuntimeStatusStore) ResetIdempotencyReceipt(ctx context.Context, workspaceID, owner, id string) (bool, error) {
	return r.transitionIdempotencyReceipt(ctx, workspaceID, owner, id, string(idempotency.StatusFailedTerminal), string(idempotency.StatusFailedRetryable))
}

func (r RuntimeStatusStore) transitionIdempotencyReceipt(ctx context.Context, workspaceID, owner, id, fromStatus, toStatus string) (bool, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	var table string
	for _, spec := range idempotencyReceiptTables {
		if spec.owner == strings.TrimSpace(owner) {
			table = spec.table
			break
		}
	}
	if table == "" || strings.TrimSpace(id) == "" {
		return false, nil
	}
	query := "UPDATE " + r.store.TableIdentifier(table) + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("lease_owner") + " = '', " + r.store.Identifier("lease_expires_at") + " = '', " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(5)
	result, err := r.db.ExecContext(ctx, query, toStatus, time.Now().UTC().Format(time.RFC3339Nano), workspace.String(), strings.TrimSpace(id), fromStatus)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

func (r RuntimeStatusStore) Ping(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

func (r RuntimeStatusStore) MigrationStatus(ctx context.Context) (deploymentmodel.MigrationStatus, error) {
	return r.store.MigrationStatus(ctx)
}

func (r RuntimeStatusStore) IdempotencyMetrics(ctx context.Context) idempotency.MetricsSnapshot {
	return r.store.IdempotencyMetrics(ctx).Snapshot()
}
