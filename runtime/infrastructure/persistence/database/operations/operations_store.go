package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

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
	placeholders := make([]string, len(columns))
	for index := range columns {
		placeholders[index] = s.store.Placeholder(index + 1)
	}
	query := "INSERT INTO " + s.store.TableIdentifier("runtime_operations") + " (" + operationsQuotedColumns(s.store, columns) + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if err := workerplatform.CheckFault(ctx, s.faults, workerplatform.FaultTransactionBeforeWrite); err != nil {
		return operationsmodel.OperationsReceipt{}, "", err
	}
	if _, err := tx.ExecContext(ctx, query, values...); err != nil {
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
	where, args := s.scopePredicate(scope, 1)
	if status != "" {
		args = append(args, string(status))
		where += " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(len(args))
	}
	args = append(args, limit)
	query := "SELECT " + operationsQuotedColumns(s.store, operationsReceiptColumns()) + " FROM " + s.store.TableIdentifier("runtime_operations") + " WHERE " + where + " ORDER BY " + s.store.Identifier("created_at") + " DESC LIMIT " + s.store.Placeholder(len(args))
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
	where, args := s.scopePredicate(scope, 1)
	addExact := func(column string, value any, present bool) {
		if !present {
			return
		}
		args = append(args, value)
		where += " AND " + s.store.Identifier(column) + " = " + s.store.Placeholder(len(args))
	}
	addExact("status", string(filter.Status), filter.Status != "")
	addExact("failure_class", string(filter.FailureClass), filter.FailureClass != "")
	addExact("kind", filter.Kind, filter.Kind != "")
	addExact("resource_type", filter.ResourceType, filter.ResourceType != "")
	addExact("resource_id", filter.ResourceID, filter.ResourceID != "")
	addExact("requested_by", filter.RequestedBy, filter.RequestedBy != "")
	addExact("correlation", filter.Correlation, filter.Correlation != "")
	if filter.CreatedFrom != "" {
		args = append(args, filter.CreatedFrom)
		where += " AND " + s.store.Identifier("created_at") + " >= " + s.store.Placeholder(len(args))
	}
	if filter.CreatedTo != "" {
		args = append(args, filter.CreatedTo)
		where += " AND " + s.store.Identifier("created_at") + " <= " + s.store.Placeholder(len(args))
	}
	if filter.Search != "" {
		searchColumns := []string{"id", "kind", "resource_type", "resource_id", "requested_by", "reason", "correlation", "error_code", "next_action"}
		terms := make([]string, 0, len(searchColumns))
		for _, column := range searchColumns {
			args = append(args, "%"+strings.ToLower(filter.Search)+"%")
			terms = append(terms, "LOWER("+s.store.Identifier(column)+") LIKE "+s.store.Placeholder(len(args)))
		}
		where += " AND (" + strings.Join(terms, " OR ") + ")"
	}
	var count int
	countQuery := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("runtime_operations") + " WHERE " + where
	if err := s.database().QueryRowContext(ctx, countQuery, args...).Scan(&count); err != nil {
		return operationsmodel.OperationsReceiptPage{}, err
	}
	summary := operationsmodel.OperationsReceiptSummary{}
	summaryQuery := "SELECT " + s.store.Identifier("status") + ", " + s.store.Identifier("failure_class") + ", COUNT(*) FROM " + s.store.TableIdentifier("runtime_operations") + " WHERE " + where + " GROUP BY " + s.store.Identifier("status") + ", " + s.store.Identifier("failure_class")
	summaryRows, err := s.database().QueryContext(ctx, summaryQuery, args...)
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
	args = append(args, filter.Limit)
	query := "SELECT " + operationsQuotedColumns(s.store, operationsReceiptColumns()) + " FROM " + s.store.TableIdentifier("runtime_operations") + " WHERE " + where + " ORDER BY " + s.store.Identifier("created_at") + " DESC LIMIT " + s.store.Placeholder(len(args))
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
	assignments := []string{
		"status", "started_at", "finished_at", "updated_at", "result_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json",
	}
	args := []any{string(receipt.Command.Status), startedAt, finishedAt, receipt.Command.UpdatedAt.UTC().Format(time.RFC3339Nano), resultJSON, strings.TrimSpace(receipt.ErrorCode), string(receipt.FailureClass), strings.TrimSpace(receipt.NextAction), relatedJSON, strings.TrimSpace(receipt.Correlation), evidenceJSON}
	set := make([]string, len(assignments))
	for index, column := range assignments {
		set[index] = s.store.Identifier(column) + " = " + s.store.Placeholder(index+1)
	}
	args = append(args, receipt.Command.ID, string(expected))
	query := "UPDATE " + s.store.TableIdentifier("runtime_operations") + " SET " + strings.Join(set, ", ") + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(len(args)-1) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(len(args))
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
	where, args := s.scopePredicate(scope, 1)
	if id != "" {
		args = append(args, id)
		where += " AND " + s.store.Identifier("id") + " = " + s.store.Placeholder(len(args))
	} else {
		args = append(args, kind, key)
		where += " AND " + s.store.Identifier("kind") + " = " + s.store.Placeholder(len(args)-1) + " AND " + s.store.Identifier("idempotency_key") + " = " + s.store.Placeholder(len(args))
	}
	query := "SELECT " + operationsQuotedColumns(s.store, operationsReceiptColumns()) + " FROM " + s.store.TableIdentifier("runtime_operations") + " WHERE " + where
	receipt, err := operationsScanReceipt(database.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		return operationsmodel.OperationsReceipt{}, false, nil
	}
	return receipt, err == nil, err
}

func (s OperationsStore) scopePredicate(scope operationsmodel.OperationsScope, position int) (string, []any) {
	if workspaceID, err := principalmodel.NewWorkspaceID(scope.WorkspaceID); err == nil {
		return s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(position), []any{workspaceID.String()}
	}
	if systemPurpose := strings.TrimSpace(scope.SystemPurpose); systemPurpose != "" {
		return s.store.Identifier("system_purpose") + " = " + s.store.Placeholder(position), []any{systemPurpose}
	}
	return "1 = 0", nil
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
