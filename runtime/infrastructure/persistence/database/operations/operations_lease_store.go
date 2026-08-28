package operations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.OperationsLeaseRepository = OperationsStore{}

type operationsLeaseSpec struct {
	owner string
	table string
}

var operationsLeaseSpecs = []operationsLeaseSpec{
	{owner: "workflow", table: "workflow_execution_receipts"},
	{owner: "workflow_execution", table: "_workflow_executions"},
	{owner: "workflow_deadline", table: "workflow_tasks"},
	{owner: "business_action", table: "business_action_executions"},
	{owner: "record_mutation", table: "record_mutation_executions"},
	{owner: "record_batch", table: "record_batch_jobs"},
	{owner: "idempotency_cleanup", table: "idempotency_cleanup_leases"},
	{owner: "automation", table: "automation_instruction_executions"},
	{owner: "integration_event", table: "integration_events"},
	{owner: "integration_outbox", table: "integration_outbox_messages"},
	{owner: "transaction_boundary", table: "transaction_boundary_intents"},
	{owner: "credential_refresh", table: "integration_credential_refresh_leases"},
	{owner: "changeplan", table: "business_change_plan_operations"},
	{owner: "notification_publication", table: "notification_template_publication_requests"},
	{owner: "lifecycle_cleanup", table: "lifecycle_cleanup_jobs"},
}

func (s OperationsStore) OperationsLeaseSnapshot(ctx context.Context, instanceID string, now time.Time) (operationsmodel.OperationsLeaseSnapshot, error) {
	if s.database() == nil {
		return operationsmodel.OperationsLeaseSnapshot{}, fmt.Errorf("operations store unavailable")
	}
	instanceID = strings.TrimSpace(instanceID)
	snapshot := operationsmodel.OperationsLeaseSnapshot{InstanceID: instanceID, CheckedAt: now.UTC(), Owners: make([]operationsmodel.OperationsLeaseCount, 0, len(operationsLeaseSpecs))}
	for _, spec := range operationsLeaseSpecs {
		live, expired, err := s.operationsLeaseCounts(ctx, spec.table, instanceID, now.UTC())
		if err != nil {
			return operationsmodel.OperationsLeaseSnapshot{}, fmt.Errorf("read %s leases: %w", spec.owner, err)
		}
		if live == 0 && expired == 0 {
			continue
		}
		snapshot.Live, snapshot.Expired = snapshot.Live+live, snapshot.Expired+expired
		snapshot.Owners = append(snapshot.Owners, operationsmodel.OperationsLeaseCount{Owner: spec.owner, Table: spec.table, Live: live, Expired: expired})
	}
	return snapshot, nil
}

func (s OperationsStore) operationsLeaseCounts(ctx context.Context, table, instanceID string, now time.Time) (int64, int64, error) {
	expires := s.store.Identifier("lease_expires_at")
	owner := s.store.Identifier("lease_owner")
	args := []any{now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)}
	where := owner + " <> ''"
	if instanceID != "" {
		args = append(args, instanceID, instanceID+":%", instanceID+"-%")
		where += " AND (" + owner + " = " + s.store.Placeholder(3) + " OR " + owner + " LIKE " + s.store.Placeholder(4) + " OR " + owner + " LIKE " + s.store.Placeholder(5) + ")"
	}
	query := "SELECT SUM(CASE WHEN " + expires + " > " + s.store.Placeholder(1) + " THEN 1 ELSE 0 END), SUM(CASE WHEN " + expires + " <> '' AND " + expires + " <= " + s.store.Placeholder(2) + " THEN 1 ELSE 0 END) FROM " + s.store.TableIdentifier(table) + " WHERE " + where
	var live, expired sql.NullInt64
	if err := s.database().QueryRowContext(ctx, query, args...).Scan(&live, &expired); err != nil {
		return 0, 0, err
	}
	return live.Int64, expired.Int64, nil
}
