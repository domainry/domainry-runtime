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

	"github.com/domainry/domainry-foundation/idempotency"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type RuntimeStatusStore struct {
	store                       *database.RuntimeStore
	db                          *sql.DB
	beforeDeleteExpiredReceipts func()
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
		filter := idempotencyOperationFilter(workspaceID, spec, "")
		summary, err := r.operationStore().SummarizeRecords(ctx, filter)
		if err != nil {
			return status, fmt.Errorf("summarize %s idempotency backlog: %w", spec.owner, err)
		}
		for receiptStatus, count := range summary.Statuses {
			status.Backlog[receiptStatus] += count
			status.BacklogTotal += count
		}
		filter.Status = string(idempotency.StatusProcessing)
		expired, err := r.operationStore().CountExpiredLeases(ctx, filter, nowText)
		if err != nil {
			return status, fmt.Errorf("count %s expired idempotency leases: %w", spec.owner, err)
		}
		status.ExpiredLeases += int(expired)
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

	cleanupQuery, cleanupArgs, buildErr := query.NewSelectBuilder(r.store.SQLRenderer, "_worker_scopes").Columns("lease_owner", "lease_expires_at", "fencing_token", "last_started_at", "last_completed_at", "checkpoint", "last_error").Where(cleanupScopePredicate()).Build()
	if buildErr != nil {
		return status, fmt.Errorf("build idempotency cleanup status: %w", buildErr)
	}
	err := r.db.QueryRowContext(ctx, cleanupQuery, cleanupArgs...).Scan(&status.Cleanup.LeaseOwner, &status.Cleanup.LeaseExpiresAt, &status.Cleanup.FencingToken, &status.Cleanup.LastStartedAt, &status.Cleanup.LastCompletedAt, &status.Cleanup.LastDeleted, &status.Cleanup.LastError)
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
	owner, table, scopeColumn, targetColumn, keyColumn, rowOwner string
}

var idempotencyReceiptTables = []idempotencyReceiptTable{
	{owner: "dispatch", scopeColumn: "kind", targetColumn: "resource_id", rowOwner: "dispatch"},
	{owner: "record", scopeColumn: "action_key", targetColumn: "resource_id", keyColumn: "reference", rowOwner: "record"},
	{owner: "action", scopeColumn: "action_key", targetColumn: "resource_id", keyColumn: "reference", rowOwner: "action"},
	{owner: "workflow", scopeColumn: "action_key", targetColumn: "resource_id", keyColumn: "reference", rowOwner: "workflow"},
	{owner: "report", scopeColumn: "action_key", targetColumn: "reference", rowOwner: "report"},
}

func (r RuntimeStatusStore) operationStore() *sharedoperation.SQLStore {
	return sharedoperation.NewSQLStore(r.db, r.store.SQLRenderer)
}

func idempotencyOperationFilter(workspaceID string, spec idempotencyReceiptTable, status string) sharedoperation.RecordFilter {
	filter := sharedoperation.RecordFilter{Owner: spec.rowOwner, Status: strings.TrimSpace(status)}
	if workspaceID = strings.TrimSpace(workspaceID); workspaceID == "" {
		filter.AllScopes = true
	} else {
		filter.WorkspaceID = workspaceID
	}
	return filter
}

func idempotencyRecordValue(record sharedoperation.Record, column string) string {
	switch column {
	case "kind":
		return record.Kind
	case "action_key":
		return record.ActionKey
	case "resource_id":
		return record.ResourceID
	case "reference":
		return record.Reference
	default:
		return ""
	}
}

func idempotencyReceiptOwnerPredicate(spec idempotencyReceiptTable) query.Predicate {
	if spec.rowOwner == "" {
		return query.AlwaysTrue()
	}
	return query.Equal("owner", spec.rowOwner)
}

func (r RuntimeStatusStore) ListIdempotencyReceipts(ctx context.Context, workspaceID, status string, limit int) ([]idempotency.ReceiptSummary, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	workspaceID, status = workspace.String(), strings.TrimSpace(status)
	values := []idempotency.ReceiptSummary{}
	for _, spec := range idempotencyReceiptTables {
		filter := idempotencyOperationFilter(workspaceID, spec, status)
		filter.Limit = limit
		records, err := r.operationStore().ListRecords(ctx, filter)
		if err != nil {
			return nil, fmt.Errorf("list %s idempotency receipts: %w", spec.owner, err)
		}
		for _, record := range records {
			scopeValue, targetValue, key := idempotencyRecordValue(record, spec.scopeColumn), idempotencyRecordValue(record, spec.targetColumn), idempotencyRecordValue(record, spec.keyColumn)
			if spec.keyColumn == "" {
				key = record.IdempotencyKey
			}
			scope := idempotencyReceiptScope(spec.owner, scopeValue, targetValue)
			values = append(values, idempotency.ReceiptSummaryFromValues(spec.owner, record.ID, record.WorkspaceID, scope, key, record.RequestFingerprint, record.Status, record.FencingToken, record.UpdatedAt, record.ExpiresAt))
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].UpdatedAt > values[j].UpdatedAt })
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}

func idempotencyReceiptScope(owner, value, target string) string {
	switch owner {
	case "dispatch":
		return "dispatch.callback.execute"
	case "record":
		return "record." + strings.TrimSpace(value)
	case "action":
		if strings.TrimSpace(target) == "" {
			return "action.execute_object"
		}
		return "action.execute_record"
	case "workflow":
		return "workflow.execute"
	case "report":
		return strings.TrimSpace(value)
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
	var selected idempotencyReceiptTable
	for _, spec := range idempotencyReceiptTables {
		if spec.owner == strings.TrimSpace(owner) {
			selected = spec
			break
		}
	}
	if selected.owner == "" || strings.TrimSpace(id) == "" {
		return false, nil
	}
	empty, updatedAt := "", time.Now().UTC().Format(time.RFC3339Nano)
	return r.operationStore().PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: workspace.String(), ID: strings.TrimSpace(id), Owner: selected.rowOwner, Status: fromStatus,
	}, sharedoperation.RecordChanges{
		Status: &toStatus, LeaseOwner: &empty, LeaseExpiresAt: &empty, IncrementFencingToken: true, UpdatedAt: &updatedAt,
	})
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
