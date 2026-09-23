package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
)

var _ operationsrepository.DatabaseRetirementRepository = OperationsStore{}

const (
	databaseRetirementSystemPurpose = "database_retirement"
	databaseRetirementOwner         = "operations"
	databaseRetirementKind          = "database_retirement"
)

func (s OperationsStore) RegisterDatabaseRetirement(ctx context.Context, retirement operationsmodel.DatabaseRetirement) (bool, error) {
	payload, _ := json.Marshal(retirement)
	metadata, _ := json.Marshal(retirement.Object)
	identity := databaseRetirementIdentity(retirement.Object)
	evidence := []string{}
	if strings.TrimSpace(retirement.Evidence.AuditEventID) != "" {
		evidence = append(evidence, strings.TrimSpace(retirement.Evidence.AuditEventID))
	}
	receipt := operationsmodel.OperationsReceipt{
		Command: operationsmodel.OperationsCommand{
			ID: retirement.ID, Owner: databaseRetirementOwner, Kind: databaseRetirementKind,
			ActionKey:      "runtime.operations.database_retirement",
			Scope:          operationsmodel.OperationsScope{SystemPurpose: databaseRetirementSystemPurpose, ResourceType: "database_object", ResourceID: retirement.ID},
			IdempotencyKey: identity, RequestFingerprint: identity, RequestedBy: retirement.Evidence.Owner,
			Reason: retirement.BlockedReason, Status: operationsmodel.OperationsStatus(retirement.State), CreatedAt: retirement.UpdatedAt, UpdatedAt: retirement.UpdatedAt,
		},
		StatusURL: "/api/operations/database-retirements/" + retirement.ID,
		Result:    payload, Metadata: metadata, Evidence: evidence,
	}
	if retirement.State == operationsmodel.DatabaseRetirementBlocked {
		receipt.ErrorCode = "runtime.database_retirement_blocked"
		receipt.FailureClass = operationsmodel.OperationsFailureManualIntervention
	}
	queryValue, args, buildErr := query.NewInsertBuilder(s.store.SQLRenderer, "_operations").Columns(operationsReceiptColumns()...).Values(operationsReceiptValues(receipt)...).Build()
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
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, "_operations").Columns("result_json").Where(databaseRetirementPredicate(query.Equal("id", strings.TrimSpace(id)))).Build()
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
	builder := query.NewSelectBuilder(s.store.SQLRenderer, "_operations").Columns("result_json").Where(databaseRetirementPredicate())
	if state != "" {
		builder.Where(databaseRetirementPredicate(query.Equal("status", string(state))))
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
	evidence, _ := json.Marshal(databaseRetirementEvidenceReferences(retirement))
	builder := query.NewUpdateBuilder(s.store.SQLRenderer, "_operations").
		Set("status", string(retirement.State)).
		Set("reason", retirement.BlockedReason).
		Set("result_json", string(payload)).
		Set("evidence_json", string(evidence)).
		Set("updated_at", retirement.UpdatedAt.UTC().Format(time.RFC3339Nano)).
		Set("error_code", "").
		Set("failure_class", "")
	if retirement.State == operationsmodel.DatabaseRetirementBlocked {
		builder.Set("error_code", "runtime.database_retirement_blocked").Set("failure_class", string(operationsmodel.OperationsFailureManualIntervention))
	}
	if retirement.State == operationsmodel.DatabaseRetirementDropped || retirement.State == operationsmodel.DatabaseRetirementCodeRemoved {
		builder.Set("finished_at", retirement.UpdatedAt.UTC().Format(time.RFC3339Nano))
	}
	queryValue, args, buildErr := builder.Where(databaseRetirementPredicate(query.Equal("id", retirement.ID), query.Equal("status", string(expected)))).Build()
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
	queryValue, args, buildErr := query.NewSelectBuilder(s.store.SQLRenderer, "_operations").Columns("result_json").Where(databaseRetirementPredicate(query.Equal("id", strings.TrimSpace(id)))).Build()
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
	update, updateArgs, buildErr := query.NewUpdateBuilder(s.store.SQLRenderer, "_operations").Set("result_json", string(updatedPayload)).Set("updated_at", accessedAt.Format(time.RFC3339Nano)).Where(databaseRetirementPredicate(query.Equal("id", retirement.ID))).Build()
	if buildErr != nil {
		return buildErr
	}
	if _, err := tx.ExecContext(ctx, update, updateArgs...); err != nil {
		return err
	}
	return tx.Commit()
}

func databaseRetirementAccessSourceAllowed(source string) bool {
	switch source {
	case "runtime", "http", "worker", "migration", "report", "query_builder", "connector", "external":
		return true
	default:
		return false
	}
}

func databaseRetirementPredicate(extra ...query.Predicate) query.Predicate {
	predicates := []query.Predicate{
		query.Equal("workspace_id", ""),
		query.Equal("system_purpose", databaseRetirementSystemPurpose),
		query.Equal("owner", databaseRetirementOwner),
		query.Equal("kind", databaseRetirementKind),
	}
	return query.And(append(predicates, extra...)...)
}

func databaseRetirementIdentity(object operationsmodel.DatabaseObjectIdentity) string {
	payload, _ := json.Marshal(object)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func databaseRetirementEvidenceReferences(retirement operationsmodel.DatabaseRetirement) []string {
	if strings.TrimSpace(retirement.Evidence.AuditEventID) == "" {
		return []string{}
	}
	return []string{strings.TrimSpace(retirement.Evidence.AuditEventID)}
}
