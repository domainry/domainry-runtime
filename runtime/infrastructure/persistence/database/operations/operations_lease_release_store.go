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
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
)

type operationsLeaseReleaseSpec struct {
	table           string
	idColumn        string
	workspaceColumn string
	publicationType string
	scopeColumn     string
	scopeValue      string
}

var operationsLeaseReleaseSpecs = map[string]operationsLeaseReleaseSpec{
	"dispatch_callback":          {table: sharedoperation.TableName, idColumn: "id", workspaceColumn: "workspace_id", scopeColumn: "owner", scopeValue: "dispatch"},
	"workflow":                   {table: sharedoperation.TableName, idColumn: "id", workspaceColumn: "workspace_id", scopeColumn: "owner", scopeValue: "workflow"},
	"workflow_execution":         {table: "_workflow_executions", idColumn: "id", workspaceColumn: "workspace_id"},
	"workflow_deadline":          {table: "_workflow_tasks", idColumn: "id", workspaceColumn: "workspace_id"},
	"business_action":            {table: sharedoperation.TableName, idColumn: "id", workspaceColumn: "workspace_id", scopeColumn: "owner", scopeValue: "action"},
	"record_mutation":            {table: sharedoperation.TableName, idColumn: "id", workspaceColumn: "workspace_id", scopeColumn: "owner", scopeValue: "record"},
	"idempotency_cleanup":        {table: sharedworkerscope.TableName, idColumn: "id", scopeColumn: "owner", scopeValue: sharedworkerscope.OwnerIdempotencyCleanup},
	"automation":                 {table: "_automation_runs", idColumn: "id", workspaceColumn: "workspace_id", scopeColumn: "run_kind", scopeValue: "instruction"},
	"runtime_publication_outbox": {table: "_publication_outbox", idColumn: "id", workspaceColumn: "workspace_id", publicationType: "integration.connector"},
}

func (s OperationsStore) ForceReleaseOperationsLease(ctx context.Context, request operationsmodel.OperationsLeaseReleaseRequest) (operationsmodel.OperationsLeaseReleaseResult, bool, error) {
	if err := operationspolicy.OperationsValidateLeaseReleaseRequest(request); err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	spec, found := operationsLeaseReleaseSpecs[strings.TrimSpace(request.Owner)]
	if !found {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, fmt.Errorf("backend.operations.lease_owner_not_registered")
	}
	if !s.store.RuntimeSchemaCapabilities().IncludesTable(spec.table) {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, fmt.Errorf("backend.operations.lease_owner_not_selected")
	}
	if spec.workspaceColumn != "" && strings.TrimSpace(request.WorkspaceID) == "" {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, fmt.Errorf("backend.workspace_scope_required")
	}
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	if spec.table == sharedoperation.TableName {
		return s.forceReleaseSharedOperationLease(ctx, tx, request, spec)
	}
	if spec.table == sharedworkerscope.TableName {
		return s.forceReleaseWorkerScopeLease(ctx, tx, request)
	}
	predicate := operationsLeaseReleasePredicate(spec, request)
	selectBuilder := query.NewSelectBuilder(s.store.SQLRenderer, spec.table).Columns("lease_owner", "lease_expires_at", "fencing_token")
	if spec.workspaceColumn != "" {
		selectBuilder = query.NewWorkspaceSelectBuilder(s.store.SQLRenderer, spec.table, request.WorkspaceID).Columns("lease_owner", "lease_expires_at", "fencing_token")
	}
	queryValue, args, buildErr := selectBuilder.Where(predicate).Build()
	if buildErr != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, buildErr
	}
	var currentOwner, expiresAt string
	var currentToken int64
	if err := tx.QueryRowContext(ctx, queryValue, args...).Scan(&currentOwner, &expiresAt, &currentToken); err != nil {
		if err == sql.ErrNoRows {
			return operationsmodel.OperationsLeaseReleaseResult{}, false, nil
		}
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, fmt.Errorf("backend.operations.lease_expiry_invalid: %w", err)
	}
	if currentOwner != request.ExpectedLeaseOwner || currentToken != request.ExpectedFencingToken {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, nil
	}
	eligibility := "expired"
	if expires.After(request.Now) {
		if !request.VerifiedStuck {
			return operationsmodel.OperationsLeaseReleaseResult{}, false, nil
		}
		eligibility = "verified_stuck"
	}
	updatePredicate := query.And(predicate, query.Equal("lease_owner", request.ExpectedLeaseOwner), query.Equal("fencing_token", request.ExpectedFencingToken))
	updateBuilder := query.NewUpdateBuilder(s.store.SQLRenderer, spec.table)
	if spec.workspaceColumn != "" {
		updateBuilder = query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, spec.table, request.WorkspaceID)
	}
	update, updateArgs, buildErr := updateBuilder.Set("lease_owner", "").Set("lease_expires_at", "").SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).Set("updated_at", request.Now.UTC().Format(time.RFC3339Nano)).Where(updatePredicate).Build()
	if buildErr != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, buildErr
	}
	updated, err := tx.ExecContext(ctx, update, updateArgs...)
	if err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	rows, err := updated.RowsAffected()
	if err != nil || rows != 1 {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	return operationsmodel.OperationsLeaseReleaseResult{Owner: request.Owner, WorkspaceID: request.WorkspaceID, ResourceID: request.ResourceID, PreviousLeaseOwner: currentOwner, PreviousFencingToken: currentToken, NextFencingToken: currentToken + 1, PreviousExpiresAt: expires, ReleasedAt: request.Now.UTC(), Eligibility: eligibility}, true, nil
}

func (s OperationsStore) forceReleaseWorkerScopeLease(ctx context.Context, tx *sql.Tx, request operationsmodel.OperationsLeaseReleaseRequest) (operationsmodel.OperationsLeaseReleaseResult, bool, error) {
	store := sharedworkerscope.NewStore(s.database(), s.store.SQLRenderer)
	released, found, err := store.ForceReleaseLease(ctx, tx, sharedworkerscope.LeaseRelease{
		Identity:           sharedworkerscope.Identity{ID: request.ResourceID, Owner: sharedworkerscope.OwnerIdempotencyCleanup},
		ExpectedLeaseOwner: request.ExpectedLeaseOwner, ExpectedFencingToken: request.ExpectedFencingToken,
		Now: request.Now, AllowUnexpired: request.VerifiedStuck,
	})
	if err != nil || !found {
		return operationsmodel.OperationsLeaseReleaseResult{}, found, err
	}
	if err := tx.Commit(); err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	return operationsmodel.OperationsLeaseReleaseResult{
		Owner: request.Owner, WorkspaceID: request.WorkspaceID, ResourceID: request.ResourceID,
		PreviousLeaseOwner: released.PreviousLeaseOwner, PreviousFencingToken: released.PreviousFencingToken,
		NextFencingToken: released.NextFencingToken, PreviousExpiresAt: released.PreviousLeaseExpiresAt,
		ReleasedAt: request.Now.UTC(), Eligibility: released.Eligibility,
	}, true, nil
}

func (s OperationsStore) forceReleaseSharedOperationLease(ctx context.Context, tx *sql.Tx, request operationsmodel.OperationsLeaseReleaseRequest, spec operationsLeaseReleaseSpec) (operationsmodel.OperationsLeaseReleaseResult, bool, error) {
	ledger, err := s.ledger()
	if err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	txContext := sharedoperation.WithExecutor(ctx, tx)
	token := request.ExpectedFencingToken
	filter := sharedoperation.RecordFilter{
		WorkspaceID: request.WorkspaceID, ID: request.ResourceID, Owner: spec.scopeValue,
		LeaseOwner: request.ExpectedLeaseOwner, FencingToken: &token,
	}
	record, found, err := ledger.GetRecord(txContext, filter)
	if err != nil || !found {
		return operationsmodel.OperationsLeaseReleaseResult{}, found, err
	}
	expires, err := time.Parse(time.RFC3339Nano, record.LeaseExpiresAt)
	if err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, fmt.Errorf("backend.operations.lease_expiry_invalid: %w", err)
	}
	eligibility := "expired"
	if expires.After(request.Now) {
		if !request.VerifiedStuck {
			return operationsmodel.OperationsLeaseReleaseResult{}, false, nil
		}
		eligibility = "verified_stuck"
	}
	empty, updatedAt := "", request.Now.UTC().Format(time.RFC3339Nano)
	changed, err := ledger.PatchRecord(txContext, filter, sharedoperation.RecordChanges{
		LeaseOwner: &empty, LeaseExpiresAt: &empty, IncrementFencingToken: true, UpdatedAt: &updatedAt,
	})
	if err != nil || !changed {
		return operationsmodel.OperationsLeaseReleaseResult{}, changed, err
	}
	if err := tx.Commit(); err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	return operationsmodel.OperationsLeaseReleaseResult{
		Owner: request.Owner, WorkspaceID: request.WorkspaceID, ResourceID: request.ResourceID,
		PreviousLeaseOwner: record.LeaseOwner, PreviousFencingToken: record.FencingToken, NextFencingToken: record.FencingToken + 1,
		PreviousExpiresAt: expires, ReleasedAt: request.Now.UTC(), Eligibility: eligibility,
	}, true, nil
}

func operationsLeaseReleasePredicate(spec operationsLeaseReleaseSpec, request operationsmodel.OperationsLeaseReleaseRequest) query.Predicate {
	predicate := query.Predicate(query.Equal(spec.idColumn, request.ResourceID))
	if spec.scopeColumn != "" {
		predicate = query.And(query.Equal(spec.scopeColumn, spec.scopeValue), predicate)
	}
	if spec.publicationType != "" {
		predicate = query.And(query.Equal("publication_type", spec.publicationType), predicate)
	}
	return predicate
}

func (s OperationsStore) operationsLeaseReleaseIdentity(spec operationsLeaseReleaseSpec, request operationsmodel.OperationsLeaseReleaseRequest) (string, []any) {
	where := s.store.Identifier(spec.idColumn) + " = " + s.store.Placeholder(1)
	args := []any{request.ResourceID}
	if spec.workspaceColumn != "" {
		args = append(args, request.WorkspaceID)
		where += " AND " + s.store.Identifier(spec.workspaceColumn) + " = " + s.store.Placeholder(2)
	}
	return where, args
}

func operationsShiftPlaceholders(s OperationsStore, predicate string, shift int) string {
	for index := 10; index >= 1; index-- {
		predicate = strings.ReplaceAll(predicate, s.store.Placeholder(index), "__placeholder_"+fmt.Sprint(index+shift)+"__")
	}
	for index := 1; index <= 10+shift; index++ {
		predicate = strings.ReplaceAll(predicate, "__placeholder_"+fmt.Sprint(index)+"__", s.store.Placeholder(index))
	}
	return predicate
}
