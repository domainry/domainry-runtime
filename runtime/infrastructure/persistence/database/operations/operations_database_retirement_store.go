package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	sharedoperation "github.com/domainry/domainry-foundation/operation"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	operationsrepository "github.com/domainry/domainry-runtime/runtime/domain/operations/repository"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

var _ operationsrepository.DatabaseRetirementRepository = OperationsStore{}

const (
	databaseRetirementSystemPurpose = "database_retirement"
	databaseRetirementOwner         = "operations"
	databaseRetirementKind          = "database_retirement"
)

func (s OperationsStore) RegisterDatabaseRetirement(ctx context.Context, retirement operationsmodel.DatabaseRetirement) (bool, error) {
	ledger, err := s.ledger()
	if err != nil {
		return false, err
	}
	return ledger.InsertRecord(ctx, operationsRecord(databaseRetirementReceipt(retirement)))
}

func databaseRetirementReceipt(retirement operationsmodel.DatabaseRetirement) operationsmodel.OperationsReceipt {
	payload, _ := database.MarshalTimeJSON(retirement)
	metadata, _ := database.MarshalTimeJSON(retirement.Object)
	identity := databaseRetirementIdentity(retirement.Object)
	evidence := databaseRetirementEvidenceReferences(retirement)
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
	return receipt
}

func (s OperationsStore) GetDatabaseRetirement(ctx context.Context, id string) (operationsmodel.DatabaseRetirement, bool, error) {
	record, found, err := s.getDatabaseRetirementRecord(ctx, strings.TrimSpace(id))
	if err != nil || !found {
		return operationsmodel.DatabaseRetirement{}, found, err
	}
	var retirement operationsmodel.DatabaseRetirement
	if err := database.UnmarshalTimeJSON(record.ResultJSON, &retirement); err != nil {
		return operationsmodel.DatabaseRetirement{}, false, err
	}
	return retirement, true, nil
}

func (s OperationsStore) ListDatabaseRetirements(ctx context.Context, state operationsmodel.DatabaseRetirementState, limit int) ([]operationsmodel.DatabaseRetirement, error) {
	ledger, err := s.ledger()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	records, err := ledger.ListRecords(ctx, sharedoperation.RecordFilter{
		SystemPurpose: databaseRetirementSystemPurpose, Owner: databaseRetirementOwner, Kind: databaseRetirementKind,
		Status: string(state), Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	result := make([]operationsmodel.DatabaseRetirement, 0, len(records))
	for _, record := range records {
		var retirement operationsmodel.DatabaseRetirement
		if err := database.UnmarshalTimeJSON(record.ResultJSON, &retirement); err != nil {
			return nil, err
		}
		result = append(result, retirement)
	}
	return result, nil
}

func (s OperationsStore) TransitionDatabaseRetirement(ctx context.Context, retirement operationsmodel.DatabaseRetirement, expected operationsmodel.DatabaseRetirementState) (bool, error) {
	ledger, err := s.ledger()
	if err != nil {
		return false, err
	}
	record, found, err := s.getDatabaseRetirementRecord(ctx, retirement.ID)
	if err != nil || !found || record.Status != string(expected) {
		return false, err
	}
	record.Status = string(retirement.State)
	record.Reason = retirement.BlockedReason
	record.ResultJSON, _ = database.MarshalTimeJSON(retirement)
	record.EvidenceJSON, _ = database.MarshalTimeJSON(databaseRetirementEvidenceReferences(retirement))
	record.UpdatedAt = retirement.UpdatedAt.UTC().Format(time.RFC3339Nano)
	record.ErrorCode, record.FailureClass = "", ""
	if retirement.State == operationsmodel.DatabaseRetirementBlocked {
		record.ErrorCode = "runtime.database_retirement_blocked"
		record.FailureClass = string(operationsmodel.OperationsFailureManualIntervention)
	}
	if retirement.State == operationsmodel.DatabaseRetirementDropped || retirement.State == operationsmodel.DatabaseRetirementCodeRemoved {
		record.FinishedAt = record.UpdatedAt
	}
	return ledger.UpdateRecord(ctx, record, string(expected))
}

func (s OperationsStore) RecordDatabaseRetirementAccess(ctx context.Context, id, operation, source string, at time.Time) error {
	operation, source = strings.TrimSpace(operation), strings.TrimSpace(source)
	if operation != "read" && operation != "write" {
		return fmt.Errorf("database retirement access operation must be read or write")
	}
	if !databaseRetirementAccessSourceAllowed(source) || at.IsZero() {
		return fmt.Errorf("database retirement access source and time are required")
	}
	ledger, err := s.ledger()
	if err != nil {
		return err
	}
	tx, err := s.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txContext := sharedoperation.WithExecutor(ctx, tx)
	record, found, err := s.getDatabaseRetirementRecordWithLedger(txContext, ledger, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if !found {
		return sql.ErrNoRows
	}
	var retirement operationsmodel.DatabaseRetirement
	if err := database.UnmarshalTimeJSON(record.ResultJSON, &retirement); err != nil {
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
	record.ResultJSON, _ = database.MarshalTimeJSON(retirement)
	record.UpdatedAt = accessedAt.Format(time.RFC3339Nano)
	changed, err := ledger.UpdateRecord(txContext, record, record.Status)
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("database retirement access update conflicted")
	}
	return tx.Commit()
}

func (s OperationsStore) getDatabaseRetirementRecord(ctx context.Context, id string) (sharedoperation.Record, bool, error) {
	ledger, err := s.ledger()
	if err != nil {
		return sharedoperation.Record{}, false, err
	}
	return s.getDatabaseRetirementRecordWithLedger(ctx, ledger, id)
}

func (s OperationsStore) getDatabaseRetirementRecordWithLedger(ctx context.Context, ledger *sharedoperation.SQLStore, id string) (sharedoperation.Record, bool, error) {
	return ledger.GetRecord(ctx, sharedoperation.RecordFilter{
		SystemPurpose: databaseRetirementSystemPurpose, Owner: databaseRetirementOwner, Kind: databaseRetirementKind, ID: strings.TrimSpace(id),
	})
}

func databaseRetirementAccessSourceAllowed(source string) bool {
	switch source {
	case "runtime", "http", "worker", "migration", "report", "query_builder", "connector", "external":
		return true
	default:
		return false
	}
}

func databaseRetirementIdentity(object operationsmodel.DatabaseObjectIdentity) string {
	payload, _ := database.MarshalTimeJSON(object)
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func databaseRetirementEvidenceReferences(retirement operationsmodel.DatabaseRetirement) []string {
	if strings.TrimSpace(retirement.Evidence.AuditEventID) == "" {
		return []string{}
	}
	return []string{strings.TrimSpace(retirement.Evidence.AuditEventID)}
}
