package operations

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.OperationsControlRepository = OperationsStore{}

func (s OperationsStore) GetOperationsControl(ctx context.Context, purpose string, kind operationsmodel.OperationsControlKind, owner string) (operationsmodel.OperationsControl, bool, error) {
	if s.database() == nil {
		return operationsmodel.OperationsControl{}, false, fmt.Errorf("operations store unavailable")
	}
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, "_operation_controls").Columns(operationsControlColumns()...).Where(query.And(query.Equal("system_purpose", strings.TrimSpace(purpose)), query.Equal("control_kind", string(kind)), query.Equal("owner", strings.TrimSpace(owner)))).Build()
	if buildErr != nil {
		return operationsmodel.OperationsControl{}, false, buildErr
	}
	control, err := operationsScanControl(s.database().QueryRowContext(ctx, queryValue, args...))
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
	predicate := query.Predicate(query.Equal("system_purpose", strings.TrimSpace(purpose)))
	if kind != "" {
		predicate = query.And(predicate, query.Equal("control_kind", string(kind)))
	}
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, "_operation_controls").Columns(operationsControlColumns()...).Where(predicate).OrderBy(query.Ascending("control_kind"), query.Ascending("owner")).Limit(limit).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := s.database().QueryContext(ctx, queryValue, args...)
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
		queryValue, args, buildErr := query.NewInsertBuilder(s.store.SQLRenderer, "_operation_controls").Columns(operationsControlColumns()...).Values(control.SystemPurpose, string(control.Kind), control.Owner, string(control.State), control.Reason, control.Reference, control.UpdatedBy, control.Revision, control.UpdatedAt.UTC().Format(time.RFC3339Nano)).Build()
		if buildErr != nil {
			return false, buildErr
		}
		_, err := s.database().ExecContext(ctx, queryValue, args...)
		if err != nil {
			_, found, readErr := s.GetOperationsControl(ctx, control.SystemPurpose, control.Kind, control.Owner)
			if readErr == nil && found {
				return false, nil
			}
			return false, err
		}
		return true, nil
	}
	queryValue, args, buildErr := query.NewUpdateBuilder(s.store.SQLRenderer, "_operation_controls").Set("state", string(control.State)).Set("reason", control.Reason).Set("reference", control.Reference).Set("updated_by", control.UpdatedBy).Set("revision", control.Revision).Set("updated_at", control.UpdatedAt.UTC().Format(time.RFC3339Nano)).Where(query.And(query.Equal("system_purpose", control.SystemPurpose), query.Equal("control_kind", string(control.Kind)), query.Equal("owner", control.Owner), query.Equal("revision", expectedRevision))).Build()
	if buildErr != nil {
		return false, buildErr
	}
	result, err := s.database().ExecContext(ctx, queryValue, args...)
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
