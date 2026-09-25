package operations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.OperationsLeaseRepository = OperationsStore{}

type operationsLeaseSpec struct {
	owner       string
	table       string
	scopeColumn string
	scopeValue  string
}

var operationsLeaseSpecs = []operationsLeaseSpec{
	{owner: "dispatch_callback", table: sharedoperation.TableName, scopeColumn: "owner", scopeValue: "dispatch"},
	{owner: "workflow", table: sharedoperation.TableName, scopeColumn: "owner", scopeValue: "workflow"},
	{owner: "workflow_execution", table: "_workflow_executions"},
	{owner: "workflow_deadline", table: "_workflow_tasks"},
	{owner: "business_action", table: sharedoperation.TableName, scopeColumn: "owner", scopeValue: "action"},
	{owner: "record_mutation", table: sharedoperation.TableName, scopeColumn: "owner", scopeValue: "record"},
	{owner: "idempotency_cleanup", table: sharedworkerscope.TableName, scopeColumn: "owner", scopeValue: sharedworkerscope.OwnerIdempotencyCleanup},
	{owner: "automation", table: "_automation_runs", scopeColumn: "run_kind", scopeValue: "instruction"},
	{owner: "runtime_publication_outbox", table: "_publication_outbox"},
}

func (s OperationsStore) OperationsLeaseSnapshot(ctx context.Context, instanceID string, now time.Time) (operationsmodel.OperationsLeaseSnapshot, error) {
	if s.database() == nil {
		return operationsmodel.OperationsLeaseSnapshot{}, fmt.Errorf("operations store unavailable")
	}
	instanceID = strings.TrimSpace(instanceID)
	snapshot := operationsmodel.OperationsLeaseSnapshot{InstanceID: instanceID, CheckedAt: now.UTC(), Owners: make([]operationsmodel.OperationsLeaseCount, 0, len(operationsLeaseSpecs))}
	for _, spec := range operationsLeaseSpecs {
		if !s.store.RuntimeSchemaCapabilities().IncludesTable(spec.table) {
			continue
		}
		live, expired, err := s.operationsLeaseCounts(ctx, spec.table, spec.scopeColumn, spec.scopeValue, instanceID, now.UTC())
		if err != nil {
			return operationsmodel.OperationsLeaseSnapshot{}, fmt.Errorf("read %s leases: %w", spec.owner, err)
		}
		if live == 0 && expired == 0 {
			continue
		}
		snapshot.Live, snapshot.Expired = snapshot.Live+live, snapshot.Expired+expired
		snapshot.Owners = append(snapshot.Owners, operationsmodel.OperationsLeaseCount{Owner: spec.owner, Live: live, Expired: expired})
	}
	return snapshot, nil
}

func (s OperationsStore) operationsLeaseCounts(ctx context.Context, table, scopeColumn, scopeValue, instanceID string, now time.Time) (int64, int64, error) {
	if table == sharedoperation.TableName {
		ledger, err := s.ledger()
		if err != nil {
			return 0, 0, err
		}
		counts, err := ledger.CountLeases(ctx, sharedoperation.RecordFilter{AllScopes: true, Owner: scopeValue}, instanceID, now.Format(time.RFC3339Nano))
		return counts.Live, counts.Expired, err
	}
	if table == sharedworkerscope.TableName {
		counts, err := sharedworkerscope.NewStore(s.database(), s.store.SQLRenderer).CountLeases(ctx, s.database(), scopeValue, instanceID, now)
		return counts.Live, counts.Expired, err
	}
	nowMillis := now.UTC().UnixMilli()
	predicate := query.Predicate(query.NotEqual("lease_owner", ""))
	if scopeColumn != "" {
		predicate = query.And(predicate, query.Equal(scopeColumn, scopeValue))
	}
	if instanceID != "" {
		predicate = query.And(predicate, query.Or(query.Equal("lease_owner", instanceID), query.Like("lease_owner", instanceID+":%"), query.Like("lease_owner", instanceID+"-%")))
	}
	liveCount := query.Coalesce(query.Sum(query.CaseWhen(query.GreaterThan("lease_expires_at", nowMillis), 1).Else(0)), query.Value(0))
	expiredCount := query.Coalesce(query.Sum(query.CaseWhen(query.And(query.NotEqual("lease_expires_at", int64(0)), query.LessThanOrEqual("lease_expires_at", nowMillis)), 1).Else(0)), query.Value(0))
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, table).Projections(query.Project(liveCount), query.Project(expiredCount)).Where(predicate).Build()
	if buildErr != nil {
		return 0, 0, buildErr
	}
	var live, expired sql.NullInt64
	if err := s.database().QueryRowContext(ctx, queryValue, args...).Scan(&live, &expired); err != nil {
		return 0, 0, err
	}
	return live.Int64, expired.Int64, nil
}
