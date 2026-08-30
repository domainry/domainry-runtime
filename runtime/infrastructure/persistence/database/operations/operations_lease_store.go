package operations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.OperationsLeaseRepository = OperationsStore{}

type operationsLeaseSpec struct {
	owner string
	table string
}

var operationsLeaseSpecs = []operationsLeaseSpec{
	{owner: "workflow", table: "_workflow_execution_receipts"},
	{owner: "workflow_execution", table: "_workflow_executions"},
	{owner: "workflow_deadline", table: "_workflow_tasks"},
	{owner: "business_action", table: "_action_executions"},
	{owner: "record_mutation", table: "_record_mutation_executions"},
	{owner: "idempotency_cleanup", table: "_idempotency_cleanup_leases"},
	{owner: "automation", table: "_automation_instruction_executions"},
	{owner: "integration_outbox", table: "_publication_outbox"},
	{owner: "transaction_boundary", table: "_transaction_boundary_intents"},
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
	nowText := now.Format(time.RFC3339Nano)
	predicate := ormbuilder.Predicate(ormbuilder.NotEqual("lease_owner", ""))
	if instanceID != "" {
		predicate = ormbuilder.And(predicate, ormbuilder.Or(ormbuilder.Equal("lease_owner", instanceID), ormbuilder.Like("lease_owner", instanceID+":%"), ormbuilder.Like("lease_owner", instanceID+"-%")))
	}
	liveCount := ormbuilder.Coalesce(ormbuilder.Sum(ormbuilder.CaseWhen(ormbuilder.GreaterThan("lease_expires_at", nowText), 1).Else(0)), ormbuilder.Value(0))
	expiredCount := ormbuilder.Coalesce(ormbuilder.Sum(ormbuilder.CaseWhen(ormbuilder.And(ormbuilder.NotEqual("lease_expires_at", ""), ormbuilder.LessThanOrEqual("lease_expires_at", nowText)), 1).Else(0)), ormbuilder.Value(0))
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, table).Projections(ormbuilder.Project(liveCount), ormbuilder.Project(expiredCount)).Where(predicate).Build()
	if buildErr != nil {
		return 0, 0, buildErr
	}
	var live, expired sql.NullInt64
	if err := s.database().QueryRowContext(ctx, query, args...).Scan(&live, &expired); err != nil {
		return 0, 0, err
	}
	return live.Int64, expired.Int64, nil
}
