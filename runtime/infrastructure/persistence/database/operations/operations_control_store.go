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

var _ operationsrepository.OperationsControlRepository = OperationsStore{}

func (s OperationsStore) GetOperationsControl(ctx context.Context, purpose string, kind operationsmodel.OperationsControlKind, owner string) (operationsmodel.OperationsControl, bool, error) {
	if s.database() == nil {
		return operationsmodel.OperationsControl{}, false, fmt.Errorf("operations store unavailable")
	}
	query := "SELECT " + operationsQuotedColumns(s.store, operationsControlColumns()) + " FROM " + s.store.TableIdentifier("runtime_operation_controls") + " WHERE " + s.store.Identifier("system_purpose") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("control_kind") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("owner") + " = " + s.store.Placeholder(3)
	control, err := operationsScanControl(s.database().QueryRowContext(ctx, query, strings.TrimSpace(purpose), string(kind), strings.TrimSpace(owner)))
	if err == sql.ErrNoRows {
		return operationsmodel.OperationsControl{}, false, nil
	}
	return control, err == nil, err
}

func (s OperationsStore) ListOperationsControls(ctx context.Context, purpose string, kind operationsmodel.OperationsControlKind, limit int) ([]operationsmodel.OperationsControl, error) {
	if s.database() == nil {
		return nil, fmt.Errorf("operations store unavailable")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	args := []any{strings.TrimSpace(purpose)}
	where := s.store.Identifier("system_purpose") + " = " + s.store.Placeholder(1)
	if kind != "" {
		args = append(args, string(kind))
		where += " AND " + s.store.Identifier("control_kind") + " = " + s.store.Placeholder(len(args))
	}
	args = append(args, limit)
	query := "SELECT " + operationsQuotedColumns(s.store, operationsControlColumns()) + " FROM " + s.store.TableIdentifier("runtime_operation_controls") + " WHERE " + where + " ORDER BY " + s.store.Identifier("control_kind") + ", " + s.store.Identifier("owner") + " LIMIT " + s.store.Placeholder(len(args))
	rows, err := s.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []operationsmodel.OperationsControl{}
	for rows.Next() {
		control, scanErr := operationsScanControl(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, control)
	}
	return result, rows.Err()
}

func (s OperationsStore) PutOperationsControl(ctx context.Context, control operationsmodel.OperationsControl, expectedRevision int64) (bool, error) {
	if s.database() == nil {
		return false, fmt.Errorf("operations store unavailable")
	}
	if expectedRevision < 0 {
		return false, fmt.Errorf("operations.control_revision_invalid")
	}
	if expectedRevision == 0 {
		columns := operationsControlColumns()
		placeholders := make([]string, len(columns))
		for index := range placeholders {
			placeholders[index] = s.store.Placeholder(index + 1)
		}
		query := "INSERT INTO " + s.store.TableIdentifier("runtime_operation_controls") + " (" + operationsQuotedColumns(s.store, columns) + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
		_, err := s.database().ExecContext(ctx, query, control.SystemPurpose, string(control.Kind), control.Owner, string(control.State), control.Reason, control.Reference, control.UpdatedBy, control.Revision, control.UpdatedAt.UTC().Format(time.RFC3339Nano))
		if err != nil {
			_, found, readErr := s.GetOperationsControl(ctx, control.SystemPurpose, control.Kind, control.Owner)
			if readErr == nil && found {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	query := "UPDATE " + s.store.TableIdentifier("runtime_operation_controls") + " SET " + s.store.Identifier("state") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("reason") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("reference") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("updated_by") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("revision") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(6) + " WHERE " + s.store.Identifier("system_purpose") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("control_kind") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("owner") + " = " + s.store.Placeholder(9) + " AND " + s.store.Identifier("revision") + " = " + s.store.Placeholder(10)
	result, err := s.database().ExecContext(ctx, query, string(control.State), control.Reason, control.Reference, control.UpdatedBy, control.Revision, control.UpdatedAt.UTC().Format(time.RFC3339Nano), control.SystemPurpose, string(control.Kind), control.Owner, expectedRevision)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func operationsControlColumns() []string {
	return []string{"system_purpose", "control_kind", "owner", "state", "reason", "reference", "updated_by", "revision", "updated_at"}
}

func operationsScanControl(scanner operationsScanner) (operationsmodel.OperationsControl, error) {
	var control operationsmodel.OperationsControl
	var kind, state, updatedAt string
	err := scanner.Scan(&control.SystemPurpose, &kind, &control.Owner, &state, &control.Reason, &control.Reference, &control.UpdatedBy, &control.Revision, &updatedAt)
	if err != nil {
		return control, err
	}
	control.Kind, control.State = operationsmodel.OperationsControlKind(kind), operationsmodel.OperationsControlState(state)
	control.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	return control, err
}
