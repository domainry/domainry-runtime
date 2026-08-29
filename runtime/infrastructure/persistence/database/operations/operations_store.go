package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationspolicy "github.com/domainry/domainry-runtime/runtime/domain/operations/policy"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

var _ operationsrepository.OperationsRepository = OperationsStore{}

type OperationsStore struct {
	store     *database.RuntimeStore
	db        *sql.DB
	readiness func() database.DatabaseReadiness
	faults    workerplatform.FaultInjector
}

func (s OperationsStore) databaseReadiness() database.DatabaseReadiness {
	if s.readiness != nil {
		return s.readiness()
	}
	return s.store.DatabaseReadiness()
}

func NewOperationsStore(store *database.RuntimeStore) OperationsStore {
	return OperationsStore{store: store, faults: workerplatform.NoopFaultInjector{}}
}

// NewOperationsStoreWithFaults is an explicit test/dev construction seam. The
// production Bootstrap always uses NewOperationsStore and cannot enable it via HTTP or config.
func NewOperationsStoreWithFaults(store *database.RuntimeStore, faults workerplatform.FaultInjector) OperationsStore {
	if faults == nil {
		faults = workerplatform.NoopFaultInjector{}
	}
	return OperationsStore{store: store, faults: faults}
}

func (s OperationsStore) database() *sql.DB {
	if s.db != nil {
		return s.db
	}
	if s.store == nil {
		return nil
	}
	return s.store.DB()
}

func (s OperationsStore) RegisterOperationsCommand(ctx context.Context, receipt operationsmodel.OperationsReceipt) (operationsmodel.OperationsReceipt, operationsmodel.OperationsSubmissionDecision, error) {
	if s.database() == nil {
		return operationsmodel.OperationsReceipt{}, "", fmt.Errorf("operations store unavailable")
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionBeforeBegin); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	defer func() { _ = tx.Rollback() }()
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionAfterBegin); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	values := operationsReceiptValues(receipt)
	columns := operationsReceiptColumns()
	var query string
	var args []any
	if workspaceID, err := principalmodel.NewWorkspaceID(receipt.Command.Scope.WorkspaceID); err == nil {
		insertColumns := append(append([]string{}, columns[:1]...), columns[2:]...)
		insertValues := append(append([]any{}, values[:1]...), values[2:]...)
		query, args, err = ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "runtime_operations", workspaceID.String()).Columns(insertColumns...).Values(insertValues...).Build()
		if err != nil {
			return operationsmodel.OperationsReceipt{}, "", err
		}
	} else {
		query, args, err = ormbuilder.NewInsertBuilder(s.store.SQLRenderer, "runtime_operations").Columns(columns...).Values(values...).Build()
		if err != nil {
			return operationsmodel.OperationsReceipt{}, "", err
		}
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionBeforeWrite); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		_ = tx.Rollback()
		if replay, replayFound, replayErr := s.GetOperationsReceiptByKey(ctx, receipt.Command.Scope, receipt.Command.Kind, receipt.Command.IdempotencyKey); replayErr == nil && replayFound {
			return replay, operationspolicy.OperationsClassifySubmission(&replay, receipt.Command), nil
		}
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionAfterWrite); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionBeforeCommit); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionAfterCommit); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	return receipt, operationsmodel.OperationsSubmissionAccepted, nil
}

func (s OperationsStore) GetOperationsReceipt(ctx context.Context, scope operationsmodel.OperationsScope, id string) (operationsmodel.OperationsReceipt, bool, error) {
	if s.database() == nil {
		return operationsmodel.OperationsReceipt{}, false, fmt.Errorf("operations store unavailable")
	}
	return s.get(ctx, s.database(), scope, strings.TrimSpace(id), "", "")
}

func (s OperationsStore) GetOperationsReceiptByKey(ctx context.Context, scope operationsmodel.OperationsScope, kind, key string) (operationsmodel.OperationsReceipt, bool, error) {
	if s.database() == nil {
		return operationsmodel.OperationsReceipt{}, false, fmt.Errorf("operations store unavailable")
	}
	return s.get(ctx, s.database(), scope, "", strings.TrimSpace(kind), strings.TrimSpace(key))
}

func (s OperationsStore) ListOperationsReceipts(ctx context.Context, scope operationsmodel.OperationsScope, status operationsmodel.OperationsStatus, limit int) ([]operationsmodel.OperationsReceipt, error) {
	if s.database() == nil {
		return nil, fmt.Errorf("operations store unavailable")
	}
	workspaceID, predicate, err := operationsScopePredicate(scope)
	if err != nil {
		return nil, err
	}
	if status != "" {
		predicate = combineOperationsPredicate(predicate, ormbuilder.Equal("status", string(status)))
	}
	builder := operationsSelectBuilder(s.store, workspaceID).Columns(operationsReceiptColumns()...).Where(predicate).OrderBy(ormbuilder.Descending("created_at"), ormbuilder.Descending("id")).Limit(limit)
	query, args, buildErr := builder.Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := s.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []operationsmodel.OperationsReceipt{}
	for rows.Next() {
		receipt, scanErr := operationsScanReceipt(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, receipt)
	}
	return result, rows.Err()
}

func (s OperationsStore) SearchOperationsReceipts(ctx context.Context, scope operationsmodel.OperationsScope, filter operationsmodel.OperationsReceiptFilter) (operationsmodel.OperationsReceiptPage, error) {
	if s.database() == nil {
		return operationsmodel.OperationsReceiptPage{}, fmt.Errorf("operations store unavailable")
	}
	workspaceID, predicate, scopeErr := operationsScopePredicate(scope)
	if scopeErr != nil {
		return operationsmodel.OperationsReceiptPage{}, scopeErr
	}
	addExact := func(column string, value any, present bool) {
		if !present {
			return
		}
		predicate = combineOperationsPredicate(predicate, ormbuilder.Equal(column, value))
	}
	addExact("status", string(filter.Status), filter.Status != "")
	addExact("failure_class", string(filter.FailureClass), filter.FailureClass != "")
	addExact("kind", filter.Kind, filter.Kind != "")
	addExact("resource_type", filter.ResourceType, filter.ResourceType != "")
	addExact("resource_id", filter.ResourceID, filter.ResourceID != "")
	addExact("requested_by", filter.RequestedBy, filter.RequestedBy != "")
	addExact("correlation", filter.Correlation, filter.Correlation != "")
	if filter.CreatedFrom != "" {
		predicate = combineOperationsPredicate(predicate, ormbuilder.GreaterThanOrEqual("created_at", filter.CreatedFrom))
	}
	if filter.CreatedTo != "" {
		predicate = combineOperationsPredicate(predicate, ormbuilder.LessThanOrEqual("created_at", filter.CreatedTo))
	}
	if filter.Search != "" {
		searchColumns := []string{"id", "kind", "resource_type", "resource_id", "requested_by", "reason", "correlation", "error_code", "next_action"}
		terms := make([]ormbuilder.Predicate, 0, len(searchColumns))
		for _, column := range searchColumns {
			terms = append(terms, ormbuilder.LikeValue(ormbuilder.Lower(ormbuilder.Column(column)), "%"+strings.ToLower(filter.Search)+"%"))
		}
		predicate = combineOperationsPredicate(predicate, ormbuilder.Or(terms...))
	}
	var count int
	countQuery, countArgs, buildErr := operationsSelectBuilder(s.store, workspaceID).Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(predicate).Build()
	if buildErr != nil {
		return operationsmodel.OperationsReceiptPage{}, buildErr
	}
	if err := s.database().QueryRowContext(ctx, countQuery, countArgs...).Scan(&count); err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	summary := operationsmodel.OperationsReceiptSummary{}
	summaryQuery, summaryArgs, buildErr := operationsSelectBuilder(s.store, workspaceID).Projections(ormbuilder.Project(ormbuilder.Column("status")), ormbuilder.Project(ormbuilder.Column("failure_class")), ormbuilder.Project(ormbuilder.CountAll())).Where(predicate).GroupBy(ormbuilder.Column("status"), ormbuilder.Column("failure_class")).Build()
	if buildErr != nil {
		return operationsmodel.OperationsReceiptPage{}, buildErr
	}
	summaryRows, err := s.database().QueryContext(ctx, summaryQuery, summaryArgs...)
	if err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	for summaryRows.Next() {
		var status, failureClass string
		var amount int
		if err := summaryRows.Scan(&status, &failureClass, &amount); err != nil {
			_ = summaryRows.Close()
			return operationsmodel.OperationsReceiptPage{}, err
		}
		switch operationsmodel.OperationsStatus(status) {
		case operationsmodel.OperationsStatusCreated:
			summary.Created += amount
		case operationsmodel.OperationsStatusStarted:
			summary.Started += amount
		case operationsmodel.OperationsStatusSucceeded:
			summary.Succeeded += amount
		case operationsmodel.OperationsStatusFailed:
			summary.Failed += amount
		}
		if operationsmodel.OperationsFailureClass(failureClass) == operationsmodel.OperationsFailureManualIntervention {
			summary.ManualIntervention += amount
		}
	}
	if err := summaryRows.Err(); err != nil {
		_ = summaryRows.Close()
		return operationsmodel.OperationsReceiptPage{}, err
	}
	query, args, buildErr := operationsSelectBuilder(s.store, workspaceID).Columns(operationsReceiptColumns()...).Where(predicate).OrderBy(ormbuilder.Descending("created_at"), ormbuilder.Descending("id")).Limit(filter.Limit).Build()
	if buildErr != nil {
		return operationsmodel.OperationsReceiptPage{}, buildErr
	}
	rows, err := s.database().QueryContext(ctx, query, args...)
	if err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	defer rows.Close()
	items := []operationsmodel.OperationsReceipt{}
	for rows.Next() {
		receipt, scanErr := operationsScanReceipt(rows)
		if scanErr != nil {
			return operationsmodel.OperationsReceiptPage{}, scanErr
		}
		items = append(items, receipt)
	}
	if err := rows.Err(); err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	return operationsmodel.OperationsReceiptPage{Items: items, Count: count, Summary: summary}, nil
}

func (s OperationsStore) UpdateOperationsReceipt(ctx context.Context, receipt operationsmodel.OperationsReceipt, expected operationsmodel.OperationsStatus) (bool, error) {
	resultJSON, relatedJSON, evidenceJSON := operationsReceiptJSON(receipt)
	startedAt, finishedAt := "", ""
	if receipt.Command.StartedAt != nil {
		startedAt = receipt.Command.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	if receipt.Command.FinishedAt != nil {
		finishedAt = receipt.Command.FinishedAt.UTC().Format(time.RFC3339Nano)
	}
	workspaceID, scopePredicate, scopeErr := operationsScopePredicate(receipt.Command.Scope)
	if scopeErr != nil {
		return false, scopeErr
	}
	predicate := combineOperationsPredicate(scopePredicate, ormbuilder.And(ormbuilder.Equal("id", receipt.Command.ID), ormbuilder.Equal("status", string(expected))))
	builder := ormbuilder.NewUpdateBuilder(s.store.SQLRenderer, "runtime_operations")
	if workspaceID != "" {
		builder = ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "runtime_operations", workspaceID)
	}
	query, args, buildErr := builder.Set("status", string(receipt.Command.Status)).Set("started_at", startedAt).Set("finished_at", finishedAt).Set("updated_at", receipt.Command.UpdatedAt.UTC().Format(time.RFC3339Nano)).Set("result_json", resultJSON).Set("error_code", strings.TrimSpace(receipt.ErrorCode)).Set("failure_class", string(receipt.FailureClass)).Set("next_action", strings.TrimSpace(receipt.NextAction)).Set("related_ids_json", relatedJSON).Set("correlation", strings.TrimSpace(receipt.Correlation)).Set("evidence_json", evidenceJSON).Where(predicate).Build()
	if buildErr != nil {
		return false, buildErr
	}
	result, err := s.database().ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed == 1, err
}

type operationsQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s OperationsStore) get(ctx context.Context, database operationsQuery, scope operationsmodel.OperationsScope, id, kind, key string) (operationsmodel.OperationsReceipt, bool, error) {
	workspaceID, predicate, scopeErr := operationsScopePredicate(scope)
	if scopeErr != nil {
		return operationsmodel.OperationsReceipt{}, false, scopeErr
	}
	if id != "" {
		predicate = combineOperationsPredicate(predicate, ormbuilder.Equal("id", id))
	} else {
		predicate = combineOperationsPredicate(predicate, ormbuilder.And(ormbuilder.Equal("kind", kind), ormbuilder.Equal("idempotency_key", key)))
	}
	query, args, buildErr := operationsSelectBuilder(s.store, workspaceID).Columns(operationsReceiptColumns()...).Where(predicate).Build()
	if buildErr != nil {
		return operationsmodel.OperationsReceipt{}, false, buildErr
	}
	receipt, err := operationsScanReceipt(database.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		return operationsmodel.OperationsReceipt{}, false, nil
	}
	return receipt, err == nil, err
}

func operationsScopePredicate(scope operationsmodel.OperationsScope) (string, ormbuilder.Predicate, error) {
	if workspaceID, err := principalmodel.NewWorkspaceID(scope.WorkspaceID); err == nil {
		return workspaceID.String(), nil, nil
	}
	if systemPurpose := strings.TrimSpace(scope.SystemPurpose); systemPurpose != "" {
		return "", ormbuilder.Equal("system_purpose", systemPurpose), nil
	}
	return "", nil, fmt.Errorf("operations scope requires workspace_id or system_purpose")
}

// scopePredicate is retained for diagnostic compatibility. Runtime repository
// paths use operationsScopePredicate and typed builders above.
func (s OperationsStore) scopePredicate(scope operationsmodel.OperationsScope, position int) (string, []any) {
	workspaceID, predicate, err := operationsScopePredicate(scope)
	if err != nil {
		return "1 = 0", nil
	}
	if workspaceID != "" {
		predicate = ormbuilder.Equal("workspace_id", workspaceID)
	}
	prepared, args, err := ormbuilder.PreparePredicate(s.store.SQLRenderer, predicate, position-1)
	if err != nil {
		return "1 = 0", nil
	}
	return prepared, args
}

func operationsSelectBuilder(store *database.RuntimeStore, workspaceID string) *ormbuilder.SelectBuilder {
	if workspaceID != "" {
		return ormbuilder.NewWorkspaceSelectBuilder(store.SQLRenderer, "runtime_operations", workspaceID)
	}
	return ormbuilder.NewSelectBuilder(store.SQLRenderer, "runtime_operations")
}

func combineOperationsPredicate(left, right ormbuilder.Predicate) ormbuilder.Predicate {
	if left == nil {
		return right
	}
	if right == nil {
		return left
	}
	return ormbuilder.And(left, right)
}

func operationsReceiptColumns() []string {
	return []string{"id", "workspace_id", "system_purpose", "kind", "permission", "resource_type", "resource_id", "idempotency_key", "request_fingerprint", "requested_by", "reason", "reference", "status", "status_url", "result_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json", "created_at", "started_at", "finished_at", "updated_at"}
}

func operationsQuotedColumns(store *database.RuntimeStore, columns []string) string {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = store.Identifier(column)
	}
	return strings.Join(quoted, ", ")
}

func operationsReceiptValues(receipt operationsmodel.OperationsReceipt) []any {
	resultJSON, relatedJSON, evidenceJSON := operationsReceiptJSON(receipt)
	command := receipt.Command
	return []any{command.ID, command.Scope.WorkspaceID, command.Scope.SystemPurpose, command.Kind, command.Permission, command.Scope.ResourceType, command.Scope.ResourceID, command.IdempotencyKey, command.RequestFingerprint, command.RequestedBy, command.Reason, command.Reference, string(command.Status), receipt.StatusURL, resultJSON, receipt.ErrorCode, string(receipt.FailureClass), receipt.NextAction, relatedJSON, receipt.Correlation, evidenceJSON, command.CreatedAt.UTC().Format(time.RFC3339Nano), "", "", command.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func operationsReceiptJSON(receipt operationsmodel.OperationsReceipt) (string, string, string) {
	resultJSON := string(receipt.Result)
	if resultJSON == "" {
		resultJSON = "{}"
	}
	related, _ := json.Marshal(receipt.RelatedIDs)
	evidence, _ := json.Marshal(receipt.Evidence)
	return resultJSON, string(related), string(evidence)
}

type operationsScanner interface{ Scan(...any) error }

func operationsScanReceipt(scanner operationsScanner) (operationsmodel.OperationsReceipt, error) {
	var receipt operationsmodel.OperationsReceipt
	var status, failureClass, resultJSON, relatedJSON, evidenceJSON, createdAt, startedAt, finishedAt, updatedAt string
	command := &receipt.Command
	err := scanner.Scan(&command.ID, &command.Scope.WorkspaceID, &command.Scope.SystemPurpose, &command.Kind, &command.Permission, &command.Scope.ResourceType, &command.Scope.ResourceID, &command.IdempotencyKey, &command.RequestFingerprint, &command.RequestedBy, &command.Reason, &command.Reference, &status, &receipt.StatusURL, &resultJSON, &receipt.ErrorCode, &failureClass, &receipt.NextAction, &relatedJSON, &receipt.Correlation, &evidenceJSON, &createdAt, &startedAt, &finishedAt, &updatedAt)
	if err != nil {
		return receipt, err
	}
	command.Status, receipt.FailureClass = operationsmodel.OperationsStatus(status), operationsmodel.OperationsFailureClass(failureClass)
	receipt.Result = json.RawMessage(resultJSON)
	if err := json.Unmarshal([]byte(relatedJSON), &receipt.RelatedIDs); err != nil {
		return receipt, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &receipt.Evidence); err != nil {
		return receipt, err
	}
	command.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return receipt, err
	}
	command.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return receipt, err
	}
	if startedAt != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, startedAt)
		if parseErr != nil {
			return receipt, parseErr
		}
		command.StartedAt = &value
	}
	if finishedAt != "" {
		value, parseErr := time.Parse(time.RFC3339Nano, finishedAt)
		if parseErr != nil {
			return receipt, parseErr
		}
		command.FinishedAt = &value
	}
	return receipt, nil
}
