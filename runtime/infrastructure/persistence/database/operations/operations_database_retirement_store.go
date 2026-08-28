package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.DatabaseRetirementRepository = OperationsStore{}

func (s OperationsStore) RegisterDatabaseRetirement(ctx context.Context, retirement operationsmodel.DatabaseRetirement) (bool, error) {
	payload, _ := json.Marshal(retirement)
	columns := []string{"id", "engine", "database_name", "schema_name", "object_kind", "object_name", "parent_name", "owner", "state", "blocked_reason", "read_count", "write_count", "last_read_at", "last_write_at", "source_counts_json", "retirement_json", "updated_at"}
	placeholders := make([]string, len(columns))
	for index := range placeholders {
		placeholders[index] = s.store.Placeholder(index + 1)
	}
	observation := retirement.Evidence.Observation
	query := "INSERT INTO " + s.store.TableIdentifier("runtime_database_retirements") + " (" + operationsQuotedColumns(s.store, columns) + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	result, err := s.database().ExecContext(ctx, query,
		retirement.ID, retirement.Object.Engine, retirement.Object.Database, retirement.Object.Schema, retirement.Object.Kind, retirement.Object.Name, retirement.Object.ParentName,
		retirement.Evidence.Owner, string(retirement.State), retirement.BlockedReason, observation.ReadCount, observation.WriteCount, retirementTime(observation.LastReadAt), retirementTime(observation.LastWriteAt), retirementSources(observation.SourceCounts), string(payload), retirement.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s OperationsStore) GetDatabaseRetirement(ctx context.Context, id string) (operationsmodel.DatabaseRetirement, bool, error) {
	query := "SELECT " + s.store.Identifier("retirement_json") + " FROM " + s.store.TableIdentifier("runtime_database_retirements") + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(1)
	var payload string
	if err := s.database().QueryRowContext(ctx, query, strings.TrimSpace(id)).Scan(&payload); err != nil {
		if err == sql.ErrNoRows {
			return operationsmodel.DatabaseRetirement{}, false, nil
		}
		return operationsmodel.DatabaseRetirement{}, false, err
	}
	var retirement operationsmodel.DatabaseRetirement
	if err := json.Unmarshal([]byte(payload), &retirement); err != nil {
		return operationsmodel.DatabaseRetirement{}, false, err
	}
	return retirement, true, nil
}

func (s OperationsStore) ListDatabaseRetirements(ctx context.Context, state operationsmodel.DatabaseRetirementState, limit int) ([]operationsmodel.DatabaseRetirement, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args := []any{}
	where := "1 = 1"
	if state != "" {
		args = append(args, string(state))
		where = s.store.Identifier("state") + " = " + s.store.Placeholder(1)
	}
	args = append(args, limit)
	query := "SELECT " + s.store.Identifier("retirement_json") + " FROM " + s.store.TableIdentifier("runtime_database_retirements") + " WHERE " + where + " ORDER BY " + s.store.Identifier("updated_at") + " DESC LIMIT " + s.store.Placeholder(len(args))
	rows, err := s.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []operationsmodel.DatabaseRetirement{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var retirement operationsmodel.DatabaseRetirement
		if err := json.Unmarshal([]byte(payload), &retirement); err != nil {
			return nil, err
		}
		result = append(result, retirement)
	}
	return result, rows.Err()
}

func (s OperationsStore) TransitionDatabaseRetirement(ctx context.Context, retirement operationsmodel.DatabaseRetirement, expected operationsmodel.DatabaseRetirementState) (bool, error) {
	payload, _ := json.Marshal(retirement)
	observation := retirement.Evidence.Observation
	columns := []string{"state", "blocked_reason", "read_count", "write_count", "last_read_at", "last_write_at", "source_counts_json", "retirement_json", "updated_at"}
	values := []any{string(retirement.State), retirement.BlockedReason, observation.ReadCount, observation.WriteCount, retirementTime(observation.LastReadAt), retirementTime(observation.LastWriteAt), retirementSources(observation.SourceCounts), string(payload), retirement.UpdatedAt.UTC().Format(time.RFC3339Nano)}
	assignments := make([]string, len(columns))
	for index, column := range columns {
		assignments[index] = s.store.Identifier(column) + " = " + s.store.Placeholder(index+1)
	}
	values = append(values, retirement.ID, string(expected))
	query := "UPDATE " + s.store.TableIdentifier("runtime_database_retirements") + " SET " + strings.Join(assignments, ", ") + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(len(values)-1) + " AND " + s.store.Identifier("state") + " = " + s.store.Placeholder(len(values))
	result, err := s.database().ExecContext(ctx, query, values...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s OperationsStore) RecordDatabaseRetirementAccess(ctx context.Context, id, operation, source string, at time.Time) error {
	operation, source = strings.TrimSpace(operation), strings.TrimSpace(source)
	if operation != "read" && operation != "write" {
		return fmt.Errorf("database retirement access operation must be read or write")
	}
	if !databaseRetirementAccessSourceAllowed(source) || at.IsZero() {
		return fmt.Errorf("database retirement access source and time are required")
	}
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var payload string
	query := "SELECT " + s.store.Identifier("retirement_json") + " FROM " + s.store.TableIdentifier("runtime_database_retirements") + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(1)
	if err := tx.QueryRowContext(ctx, query, strings.TrimSpace(id)).Scan(&payload); err != nil {
		return err
	}
	var retirement operationsmodel.DatabaseRetirement
	if err := json.Unmarshal([]byte(payload), &retirement); err != nil {
		return err
	}
	observation := &retirement.Evidence.Observation
	if observation.SourceCounts == nil {
		observation.SourceCounts = map[string]uint64{}
	}
	observation.SourceCounts[source]++
	accessedAt := at.UTC()
	if operation == "read" {
		observation.ReadCount++
		observation.LastReadAt = &accessedAt
	} else {
		observation.WriteCount++
		observation.LastWriteAt = &accessedAt
	}
	retirement.UpdatedAt = accessedAt
	updatedPayload, _ := json.Marshal(retirement)
	assignments := []string{
		s.store.Identifier("read_count") + " = " + s.store.Placeholder(1),
		s.store.Identifier("write_count") + " = " + s.store.Placeholder(2),
		s.store.Identifier("last_read_at") + " = " + s.store.Placeholder(3),
		s.store.Identifier("last_write_at") + " = " + s.store.Placeholder(4),
		s.store.Identifier("source_counts_json") + " = " + s.store.Placeholder(5),
		s.store.Identifier("retirement_json") + " = " + s.store.Placeholder(6),
		s.store.Identifier("updated_at") + " = " + s.store.Placeholder(7),
	}
	update := "UPDATE " + s.store.TableIdentifier("runtime_database_retirements") + " SET " + strings.Join(assignments, ", ") + " WHERE " + s.store.Identifier("id") + " = " + s.store.Placeholder(8)
	if _, err := tx.ExecContext(ctx, update, observation.ReadCount, observation.WriteCount, retirementTime(observation.LastReadAt), retirementTime(observation.LastWriteAt), retirementSources(observation.SourceCounts), string(updatedPayload), accessedAt.Format(time.RFC3339Nano), retirement.ID); err != nil {
		return err
	}
	return tx.Commit()
}

func retirementTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func retirementSources(values map[string]uint64) string {
	if values == nil {
		return "{}"
	}
	raw, _ := json.Marshal(values)
	return string(raw)
}

func databaseRetirementAccessSourceAllowed(source string) bool {
	switch source {
	case "runtime", "http", "worker", "migration", "report", "query_builder", "connector", "external":
		return true
	default:
		return false
	}
}
