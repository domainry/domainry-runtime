package operations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
)

type operationsLeaseReleaseSpec struct {
	table           string
	idColumn        string
	workspaceColumn string
	publicationType string
}

var operationsLeaseReleaseSpecs = map[string]operationsLeaseReleaseSpec{
	"workflow":                   {table: "_workflow_execution_receipts", idColumn: "id", workspaceColumn: "workspace_id"},
	"workflow_execution":         {table: "_workflow_executions", idColumn: "id", workspaceColumn: "workspace_id"},
	"workflow_deadline":          {table: "_workflow_tasks", idColumn: "id", workspaceColumn: "workspace_id"},
	"business_action":            {table: "_action_executions", idColumn: "id", workspaceColumn: "workspace_id"},
	"record_mutation":            {table: "_record_mutation_executions", idColumn: "id", workspaceColumn: "workspace_id"},
	"idempotency_cleanup":        {table: "_idempotency_cleanup_leases", idColumn: "id"},
	"automation":                 {table: "_automation_instruction_executions", idColumn: "id", workspaceColumn: "workspace_id"},
	"runtime_publication_outbox": {table: "_publication_outbox", idColumn: "id", workspaceColumn: "workspace_id", publicationType: "integration.connector"},
	"transaction_boundary":       {table: "_transaction_boundary_intents", idColumn: "id", workspaceColumn: "workspace_id"},
}

func (s OperationsStore) ForceReleaseOperationsLease(ctx context.Context, request operationsmodel.OperationsLeaseReleaseRequest) (operationsmodel.OperationsLeaseReleaseResult, bool, error) {
	if err := operationspolicy.OperationsValidateLeaseReleaseRequest(request); err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	spec, found := operationsLeaseReleaseSpecs[strings.TrimSpace(request.Owner)]
	if !found {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, fmt.Errorf("backend.operations.lease_owner_not_registered")
	}
	if spec.workspaceColumn != "" && strings.TrimSpace(request.WorkspaceID) == "" {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, fmt.Errorf("backend.workspace_scope_required")
	}
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return operationsmodel.OperationsLeaseReleaseResult{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
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

func operationsLeaseReleasePredicate(spec operationsLeaseReleaseSpec, request operationsmodel.OperationsLeaseReleaseRequest) query.Predicate {
	predicate := query.Predicate(query.Equal(spec.idColumn, request.ResourceID))
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
