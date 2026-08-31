package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.DatabaseRetirementRepository = OperationsStore{}

func (s OperationsStore) RegisterDatabaseRetirement(ctx context.Context, retirement operationsmodel.DatabaseRetirement) (bool, error) {
	payload, _ := json.Marshal(retirement)
	observation := retirement.Evidence.Observation
	queryValue, args, buildErr := query.NewInsertBuilder(s.store.SQLRenderer, "_operation_database_retirements").Columns("id", "engine", "database_name", "schema_name", "object_kind", "object_name", "parent_name", "owner", "state", "blocked_reason", "read_count", "write_count", "last_read_at", "last_write_at", "source_counts_json", "retirement_json", "updated_at").Values(
		retirement.ID, retirement.Object.Engine, retirement.Object.Database, retirement.Object.Schema, retirement.Object.Kind, retirement.Object.Name, retirement.Object.ParentName,
		retirement.Evidence.Owner, string(retirement.State), retirement.BlockedReason, observation.ReadCount, observation.WriteCount, retirementTime(observation.LastReadAt), retirementTime(observation.LastWriteAt), retirementSources(observation.SourceCounts), string(payload), retirement.UpdatedAt.UTC().Format(time.RFC3339Nano),
	).Build()
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

func (s OperationsStore) GetDatabaseRetirement(ctx context.Context, id string) (operationsmodel.DatabaseRetirement, bool, error) {
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, "_operation_database_retirements").Columns("retirement_json").Where(query.Equal("id", strings.TrimSpace(id))).Build()
	if buildErr != nil {
		return operationsmodel.DatabaseRetirement{}, false, buildErr
	}
	var payload string
	if err := s.database().QueryRowContext(ctx, queryValue, args...).Scan(&payload); err != nil {
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
	builder := query.NewSelectBuilder(s.store.SQLRenderer, "_operation_database_retirements").Columns("retirement_json")
	if state != "" {
		builder.Where(query.Equal("state", string(state)))
	}
	queryValue, args, buildErr := builder.OrderBy(query.Descending("updated_at"), query.Descending("id")).Limit(limit).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := s.database().QueryContext(ctx, queryValue, args...)
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
	queryValue, args, buildErr := query.NewUpdateBuilder(s.store.SQLRenderer, "_operation_database_retirements").Set("state", string(retirement.State)).Set("blocked_reason", retirement.BlockedReason).Set("read_count", observation.ReadCount).Set("write_count", observation.WriteCount).Set("last_read_at", retirementTime(observation.LastReadAt)).Set("last_write_at", retirementTime(observation.LastWriteAt)).Set("source_counts_json", retirementSources(observation.SourceCounts)).Set("retirement_json", string(payload)).Set("updated_at", retirement.UpdatedAt.UTC().Format(time.RFC3339Nano)).Where(query.And(query.Equal("id", retirement.ID), query.Equal("state", string(expected)))).Build()
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
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, "_operation_database_retirements").Columns("retirement_json").Where(query.Equal("id", strings.TrimSpace(id))).Build()
	if buildErr != nil {
		return buildErr
	}
	if err := tx.QueryRowContext(ctx, queryValue, args...).Scan(&payload); err != nil {
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
	update, updateArgs, buildErr := query.NewUpdateBuilder(s.store.SQLRenderer, "_operation_database_retirements").Set("read_count", observation.ReadCount).Set("write_count", observation.WriteCount).Set("last_read_at", retirementTime(observation.LastReadAt)).Set("last_write_at", retirementTime(observation.LastWriteAt)).Set("source_counts_json", retirementSources(observation.SourceCounts)).Set("retirement_json", string(updatedPayload)).Set("updated_at", accessedAt.Format(time.RFC3339Nano)).Where(query.Equal("id", retirement.ID)).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := tx.ExecContext(ctx, update, updateArgs...); err != nil {
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
